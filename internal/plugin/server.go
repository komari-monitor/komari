package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// registerServerModule registers the "server" native module for one plugin
// instance. The module exposes:
//
//	server.route(method, path, handler)   register an HTTP route on the host
//	                                      engine; handler receives (req, res)
//	                                      and must call res.end() to finish
//	server.static(path, dir, opts)        serve a folder from the plugin
//	                                      directory at a mount path; opts.spa
//	                                      makes unmatched paths fall back to
//	                                      index.html
//	server.hook(kind, fn)                 register a request or response hook;
//	                                      an optional "METHOD /path", "/path"
//	                                      or "/path/*" filter limits the hook
//	                                      to matching requests. The ws kinds
//	                                      (wsConnect / wsMessage / wsSend /
//	                                      wsClose) intercept WebSocket
//	                                      connections and frames on matching
//	                                      endpoints; they share the
//	                                      allowHooks permission and accept
//	                                      only path matchers
//	server.injectHTML(head, body)         register HTML fragments embedded
//	                                      into every text/html response:
//	                                      head before </head>, body before
//	                                      </body> (all pages, including the
//	                                      admin and terminal pages)
//	server.call(method, params...)        call a registered RPC method with
//	                                      admin authority; resolves to the RPC
//	                                      result or rejects with an Error
//	                                      carrying code/message/data
//	server.registerRPC(method, handler)   register a plugin-owned RPC method
//	server.getConfig()                    resolve the saved plugin configuration
//	server.cron(expr, fn)                 run fn on the plugin event loop each
//	                                      time the cron expression fires
//
// server.registerRPC, server.getConfig, server.cron and filesystem access
// confined to the plugin directory are always granted without a manifest
// declaration.
// server.route, server.hook, server.injectHTML and server.call require the
// allowRoutes, allowHooks, allowHTMLInject and allowSystemRPC permissions
// respectively; a missing permission fails at load time (route/hook/inject)
// or rejects the call.
//
// The host engine keeps a registered route slot after unload; requests then
// receive 404 until the plugin is loaded again.
func (m *Manager) registerServerModule(host *jsruntime.Host, registry *require.Registry, inst *Instance) {
	registry.RegisterNativeModule("server", func(vm *goja.Runtime, module *goja.Object) {
		exports := vm.NewObject()
		_ = exports.Set("route", func(call goja.FunctionCall) goja.Value {
			method := strings.ToUpper(strings.TrimSpace(call.Argument(0).String()))
			path := call.Argument(1).String()
			handler, ok := goja.AssertFunction(call.Argument(2))
			if method == "" {
				panic(vm.NewTypeError("server.route requires an HTTP method"))
			}
			if path == "" || !strings.HasPrefix(path, "/") {
				panic(vm.NewTypeError("server.route requires a path starting with /"))
			}
			if !ok {
				panic(vm.NewTypeError("server.route requires a function handler"))
			}
			if !inst.info.Permissions.AllowRoutes {
				panic(vm.NewTypeError("server.route requires the \"route\" permission (allowRoutes)"))
			}
			if err := m.registerRoute(inst.info.Short, method, path, handler); err != nil {
				panic(vm.NewGoError(err))
			}
			return goja.Undefined()
		})
		_ = exports.Set("static", func(call goja.FunctionCall) goja.Value {
			mount := strings.TrimSpace(call.Argument(0).String())
			mount = strings.TrimRight(mount, "/")
			dir := strings.TrimSpace(call.Argument(1).String())
			if mount == "" || mount == "/" || !strings.HasPrefix(mount, "/") {
				panic(vm.NewTypeError("server.static requires a mount path starting with / (and not \"/\")"))
			}
			if dir == "" {
				panic(vm.NewTypeError("server.static requires a folder name"))
			}
			if !filepath.IsLocal(dir) {
				panic(vm.NewTypeError("server.static folder must be a relative path inside the plugin directory"))
			}
			if !inst.info.Permissions.AllowRoutes {
				panic(vm.NewTypeError("server.static requires the \"route\" permission (allowRoutes)"))
			}
			var opts map[string]any
			if !goja.IsUndefined(call.Argument(2)) && !goja.IsNull(call.Argument(2)) {
				if err := vm.ExportTo(call.Argument(2), &opts); err != nil {
					panic(vm.NewTypeError("server.static options must be an object like { spa: true }"))
				}
			}
			spa := false
			if v, ok := opts["spa"]; ok {
				spa, _ = v.(bool)
			}
			if err := m.registerStatic(inst.info.Short, mount, dir, spa); err != nil {
				panic(vm.NewGoError(err))
			}
			return goja.Undefined()
		})
		_ = exports.Set("call", func(call goja.FunctionCall) goja.Value {
			method := strings.TrimSpace(call.Argument(0).String())
			if method == "" {
				panic(vm.NewTypeError("server.call requires an RPC method name"))
			}
			params := callParams(call)
			promise, resolve, reject := vm.NewPromise()
			go func() {
				if !inst.info.Permissions.AllowSystemRPC {
					host.RunOnLoop(func(vm *goja.Runtime) {
						_ = reject(vm.NewGoError(errors.New("server.call requires the \"system RPC\" permission (allowSystemRPC)")))
					})
					return
				}
				meta := &rpc.ContextMeta{Permission: rpc.RoleAdmin, Principal: rpc.PrincipalFromRole(rpc.RoleAdmin)}
				ctx := rpc.NewContextWithMeta(context.Background(), meta)
				resp := rpc.CallWithContext(ctx, nil, method, params)
				host.RunOnLoop(func(vm *goja.Runtime) {
					if resp.Error != nil {
						_ = reject(jsRPCError(vm, resp.Error))
						return
					}
					result, err := normalizeRPCValue(resp.Result)
					if err != nil {
						_ = reject(vm.NewGoError(err))
						return
					}
					_ = resolve(result)
				})
			}()
			return vm.ToValue(promise)
		})
		_ = exports.Set("hook", func(call goja.FunctionCall) goja.Value {
			kind := strings.ToLower(strings.TrimSpace(call.Argument(0).String()))
			switch kind {
			case "request", "response", string(hookWSConnect), string(hookWSMessage), string(hookWSSend), string(hookWSClose):
				if !inst.info.Permissions.AllowHooks {
					panic(vm.NewTypeError("server.hook requires the \"hook\" permission (allowHooks)"))
				}
			default:
				panic(vm.NewTypeError("server.hook kind must be \"request\", \"response\", \"wsConnect\", \"wsMessage\", \"wsSend\" or \"wsClose\""))
			}
			// server.hook(kind, fn) or server.hook(kind, matcher, fn) where
			// matcher is "METHOD /path", "/path" or "/path/*". The ws kinds
			// only accept a path matcher: every upgrade is a GET request.
			fnValue := call.Argument(1)
			var matcher *hookMatcher
			if goja.IsString(fnValue) {
				parsed, err := parseHookMatcher(fnValue.String())
				if err != nil {
					panic(vm.NewTypeError(err.Error()))
				}
				if isWSHookKind(kind) && parsed.method != "" {
					panic(vm.NewTypeError("server.hook \"" + kind + "\" only accepts a path matcher like \"/api/*\""))
				}
				matcher = parsed
				fnValue = call.Argument(2)
			}
			fn, ok := goja.AssertFunction(fnValue)
			if !ok {
				panic(vm.NewTypeError("server.hook requires a function handler"))
			}
			m.registerHook(inst.info.Short, hookKind(kind), fn, matcher)
			return goja.Undefined()
		})
		_ = exports.Set("injectHTML", func(call goja.FunctionCall) goja.Value {
			if !inst.info.Permissions.AllowHTMLInject {
				panic(vm.NewTypeError("server.injectHTML requires the \"HTML inject\" permission (allowHTMLInject)"))
			}
			m.registerInject(inst.info.Short, call.Argument(0).String(), call.Argument(1).String())
			return goja.Undefined()
		})
		_ = exports.Set("cron", func(call goja.FunctionCall) goja.Value {
			spec := strings.TrimSpace(call.Argument(0).String())
			fn, ok := goja.AssertFunction(call.Argument(1))
			if spec == "" {
				panic(vm.NewTypeError("server.cron requires a cron expression"))
			}
			if !ok {
				panic(vm.NewTypeError("server.cron requires a function handler"))
			}
			if err := m.registerCron(inst.info.Short, spec, fn); err != nil {
				panic(vm.NewGoError(err))
			}
			return goja.Undefined()
		})
		_ = exports.Set("registerRPC", func(call goja.FunctionCall) goja.Value {
			method := strings.TrimSpace(call.Argument(0).String())
			fn, ok := goja.AssertFunction(call.Argument(1))
			if method == "" {
				panic(vm.NewTypeError("server.registerRPC requires an RPC method name"))
			}
			if !ok {
				panic(vm.NewTypeError("server.registerRPC requires a function handler"))
			}
			if err := m.registerRPC(inst.info.Short, method, fn); err != nil {
				panic(vm.NewGoError(err))
			}
			return goja.Undefined()
		})
		_ = exports.Set("getConfig", func(call goja.FunctionCall) goja.Value {
			promise, resolve, reject := vm.NewPromise()
			go func() {
				data, err := GetConfiguration(inst.info.Short)
				host.RunOnLoop(func(vm *goja.Runtime) {
					if err != nil {
						_ = reject(vm.NewGoError(err))
						return
					}
					_ = resolve(data)
				})
			}()
			return vm.ToValue(promise)
		})
		_ = module.Set("exports", exports)
	})
}

// callParams converts JS arguments into JSON-RPC params: no params, a single
// value, or a positional array. Values that cannot be exported (for example
// circular objects) are skipped so the call rejects instead of panicking.
func callParams(call goja.FunctionCall) any {
	switch n := len(call.Arguments); {
	case n <= 1:
		return nil
	case n == 2:
		exported, _ := exportJSValue(call.Argument(1))
		return exported
	default:
		params := make([]any, 0, n-1)
		for i := 1; i < n; i++ {
			exported, err := exportJSValue(call.Argument(i))
			if err != nil {
				return nil
			}
			params = append(params, exported)
		}
		return params
	}
}

func jsRPCError(vm *goja.Runtime, e *rpc.JsonRpcError) *goja.Object {
	obj := vm.NewGoError(fmt.Errorf("%s", e.Message))
	_ = obj.Set("code", e.Code)
	if e.Data != nil {
		_ = obj.Set("data", e.Data)
	}
	return obj
}

func normalizeRPCValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("serialize RPC result: %w", err)
	}

	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return nil, fmt.Errorf("decode RPC result: %w", err)
	}
	return normalized, nil
}

// registerRoute registers (or reuses) a gin route slot for one plugin. It is
// called from the plugin's own event loop during script evaluation and takes
// the manager lock itself. The critical section is short and never waits on
// the event loop, so no lock cycle is possible.
func (m *Manager) registerRoute(short, method, path string, handler goja.Callable) (err error) {
	key := method + " " + path
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[short]
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	if m.engine == nil {
		return fmt.Errorf("plugin manager has no HTTP engine")
	}
	if m.routes[short] == nil {
		m.routes[short] = make(map[string]bool)
	}
	slotExists := m.routes[short][key]
	inst.mu.Lock()
	if !slotExists && inst.handlers[key] != nil {
		inst.mu.Unlock()
		return nil // already registered in this load
	}
	inst.handlers[key] = handler
	inst.mu.Unlock()
	if slotExists {
		return nil // gin slot from an earlier load stays; the handler slot is refreshed
	}
	defer func() {
		if r := recover(); r != nil {
			inst.mu.Lock()
			delete(inst.handlers, key)
			inst.mu.Unlock()
			err = fmt.Errorf("register route %s %s: %v", method, path, r)
		}
	}()
	m.engine.Handle(method, path, m.routeHandler(short))
	m.routes[short][key] = true
	return nil
}

// routeHandler bridges one gin request into the plugin event loop. The
// handler receives (req, res); the response is written once res.end() runs
// or the runtime timeout elapses.
func (m *Manager) routeHandler(short string) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst := m.instanceFor(short)
		if inst == nil {
			c.Status(http.StatusNotFound)
			return
		}
		key := c.Request.Method + " " + c.FullPath()
		inst.mu.RLock()
		handler := inst.handlers[key]
		host := inst.host
		alive := inst.runtime != nil
		limit := inst.info.Permissions.MaxHTTPBodyBytes
		inst.mu.RUnlock()
		if !alive || handler == nil || host == nil {
			c.Status(http.StatusNotFound)
			return
		}
		if limit < 1 {
			limit = defaultMaxHTTPBodyBytes
		}
		body, err := httpbody.ReadAll(c.Request.Body, limit)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			_, _ = c.Writer.WriteString(err.Error())
			return
		}

		state := &routeResponse{
			headers:    make(http.Header),
			statusCode: http.StatusOK,
			done:       make(chan struct{}),
			chunks:     make(chan []byte, 4),
			abortCh:    make(chan struct{}),
		}
		queued := host.RunOnLoop(func(vm *goja.Runtime) {
			req := routeRequest(vm, c, body)
			res := routeResponseObject(vm, state)
			runErr := host.RunJob(vm, "plugin route "+short, func() error {
				_, callErr := handler(goja.Undefined(), req, res)
				return callErr
			})
			if runErr != nil {
				state.finish(http.StatusInternalServerError, nil)
			}
		})
		if !queued {
			c.Status(http.StatusServiceUnavailable)
			return
		}

		writeFinal := func(status int, body []byte) {
			state.mu.Lock()
			if status != 0 {
				state.statusCode = status
			}
			if body != nil {
				_, _ = state.body.Write(body)
			}
			for name, values := range state.headers {
				for _, value := range values {
					c.Header(name, value)
				}
			}
			statusCode := state.statusCode
			responseBody := append([]byte(nil), state.body.Bytes()...)
			state.mu.Unlock()
			c.Status(statusCode)
			_, _ = c.Writer.Write(responseBody)
		}

		// Streaming pump: with res.streaming = true every res.write is sent back
		// to the client and flushed immediately; normal mode buffers and writes
		// once at res.end(). When the client disconnects, aborted is set so the
		// plugin script can stop producing via res.isAborted().
		idle := time.NewTimer(host.Timeout())
		defer idle.Stop()
		committed := false
		for {
			select {
			case chunk := <-state.chunks:
				if !committed {
					state.mu.Lock()
					for name, values := range state.headers {
						for _, value := range values {
							c.Header(name, value)
						}
					}
					statusCode := state.statusCode
					state.mu.Unlock()
					c.Status(statusCode)
					committed = true
				}
				_, _ = c.Writer.Write(chunk)
				if flusher, ok := c.Writer.(http.Flusher); ok {
					flusher.Flush()
				}
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(host.Timeout())
			case <-state.done:
				if !committed {
					writeFinal(0, nil)
				}
				return
			case <-c.Request.Context().Done():
				state.aborted.Store(true)
				close(state.abortCh)
				return
			case <-idle.C:
				if state.isStreaming() {
					// Streaming idle timeout: end the stream and notify JS
					state.aborted.Store(true)
					close(state.abortCh)
					return
				}
				writeFinal(http.StatusGatewayTimeout, []byte("plugin route handler timed out"))
				return
			}
		}
	}
}

// staticConfig is the current static folder config of one mount path. It is
// stored on the Instance so the gin slot (which survives unload) always reads
// the config of the latest load.
type staticConfig struct {
	dir string // absolute folder path inside the plugin directory
	spa bool   // fall back to index.html for unmatched paths
}

// registerStatic serves a folder from the plugin directory at a mount path.
// It registers GET and HEAD slots for both the mount path and its wildcard
// so /ui and /ui/anything both reach the folder. Like registerRoute, the gin
// slots survive unload (they return 404) and are reused on reload; the
// handler always resolves the current config from the instance.
func (m *Manager) registerStatic(short, mount, dir string, spa bool) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[short]
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	if m.engine == nil {
		return fmt.Errorf("plugin manager has no HTTP engine")
	}
	absDir := filepath.Join(inst.dir, dir)
	if !withinDir(absDir, inst.dir) {
		return fmt.Errorf("static folder %q must be inside the plugin directory", dir)
	}
	fi, statErr := os.Stat(absDir)
	if statErr != nil || !fi.IsDir() {
		return fmt.Errorf("static folder %q does not exist", dir)
	}
	if m.routes[short] == nil {
		m.routes[short] = make(map[string]bool)
	}
	inst.mu.Lock()
	inst.statics[mount] = &staticConfig{dir: absDir, spa: spa}
	inst.mu.Unlock()

	key := "STATIC " + mount
	if m.routes[short][key] {
		return nil // gin slots from an earlier load stay; the config was refreshed above
	}
	defer func() {
		if r := recover(); r != nil {
			inst.mu.Lock()
			delete(inst.statics, mount)
			inst.mu.Unlock()
			err = fmt.Errorf("register static %s: %v", mount, r)
		}
	}()
	handler := m.staticHandler(short, mount)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		m.engine.Handle(method, mount, handler)
		m.engine.Handle(method, mount+"/*filepath", handler)
	}
	m.routes[short][key] = true
	return nil
}

// staticHandler serves files from a static folder. Requests to the mount
// path resolve to index.html; requests into subpaths resolve to the matching
// file. With spa enabled, any unmatched path (including directories without
// an index.html) falls back to index.html so client-side routers work.
func (m *Manager) staticHandler(short, mount string) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst := m.instanceFor(short)
		if inst == nil {
			c.Status(http.StatusNotFound)
			return
		}
		inst.mu.RLock()
		cfg := inst.statics[mount]
		inst.mu.RUnlock()
		if cfg == nil {
			c.Status(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(c.Request.URL.Path, mount)
		name = strings.TrimPrefix(name, "/")
		target, err := resolveStaticFile(cfg.dir, name)
		if err != nil {
			if !cfg.spa {
				c.Status(http.StatusNotFound)
				return
			}
			target = filepath.Join(cfg.dir, "index.html")
		}
		c.File(target)
	}
}

// resolveStaticFile maps a request path to a file inside a static folder,
// rejecting traversal. A directory resolves to its index.html.
func resolveStaticFile(dir, name string) (string, error) {
	if name == "" {
		name = "index.html"
	}
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid static path %q", name)
	}
	full := filepath.Join(dir, name)
	if !withinDir(full, dir) {
		return "", fmt.Errorf("invalid static path %q", name)
	}
	fi, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		full = filepath.Join(full, "index.html")
		if _, err := os.Stat(full); err != nil {
			return "", err
		}
	}
	return full, nil
}

// routeResponse is the Go side of the JS response object. It mirrors the
// minimal nodeHTTPResponse pattern from the jsruntime http module. When
// res.streaming is enabled, chunks are pushed through a channel and written
// back to the client immediately instead of being buffered.
type routeResponse struct {
	mu         sync.Mutex
	headers    http.Header
	body       bytes.Buffer
	statusCode int
	ended      bool
	done       chan struct{}

	streaming bool
	chunks    chan []byte
	aborted   atomic.Bool
	abortCh   chan struct{}
}

func (state *routeResponse) markStreaming() {
	state.mu.Lock()
	state.streaming = true
	state.mu.Unlock()
}

func (state *routeResponse) isStreaming() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.streaming
}

// pushChunk delivers a streamed chunk to the HTTP pump. It returns false
// when the client connection is gone so the script can stop producing.
func (state *routeResponse) pushChunk(p []byte) bool {
	select {
	case state.chunks <- p:
		return true
	case <-state.abortCh:
		return false
	}
}

func (state *routeResponse) finish(status int, body []byte) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.ended {
		return
	}
	if status != 0 {
		state.statusCode = status
	}
	if body != nil {
		_, _ = state.body.Write(body)
	}
	state.ended = true
	close(state.done)
}

func routeRequest(vm *goja.Runtime, c *gin.Context, body []byte) *goja.Object {
	req := hookRequestObject(vm, c.Request, body)
	_ = req.Set("context", routeRequestContext(vm, c))
	return req
}

func routeResponseObject(vm *goja.Runtime, state *routeResponse) *goja.Object {
	res := vm.NewObject()
	_ = res.Set("statusCode", http.StatusOK)
	_ = res.Set("statusMessage", "")
	_ = res.Set("streaming", false)
	_ = res.Set("isAborted", func() bool { return state.aborted.Load() })
	_ = res.Set("setHeader", func(name string, value goja.Value) *goja.Object {
		state.mu.Lock()
		defer state.mu.Unlock()
		var values []string
		if err := vm.ExportTo(value, &values); err == nil {
			state.headers[name] = values
		} else {
			state.headers.Set(name, value.String())
		}
		return res
	})
	_ = res.Set("getHeader", func(name string) goja.Value {
		state.mu.Lock()
		defer state.mu.Unlock()
		values := state.headers.Values(name)
		if len(values) == 0 {
			return goja.Undefined()
		}
		if len(values) == 1 {
			return vm.ToValue(values[0])
		}
		return vm.ToValue(values)
	})
	_ = res.Set("removeHeader", func(name string) {
		state.mu.Lock()
		state.headers.Del(name)
		state.mu.Unlock()
	})
	_ = res.Set("write", func(call goja.FunctionCall) goja.Value {
		data := buffer.Bytes(vm, call.Argument(0))
		if value := res.Get("streaming"); value != nil && value.ToBoolean() {
			state.markStreaming()
			_ = state.pushChunk(data)
			return vm.ToValue(true)
		}
		state.mu.Lock()
		_, _ = state.body.Write(data)
		state.mu.Unlock()
		return vm.ToValue(true)
	})
	_ = res.Set("end", func(call goja.FunctionCall) goja.Value {
		state.mu.Lock()
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			_, _ = state.body.WriteString(call.Argument(0).String())
		}
		state.statusCode = int(res.Get("statusCode").ToInteger())
		if !state.ended {
			state.ended = true
			close(state.done)
		}
		state.mu.Unlock()
		return res
	})
	return res
}
