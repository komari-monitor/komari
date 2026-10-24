package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/api"
)

// registerServerModule registers the "server" native module for one plugin
// instance. The module exposes:
//
//	server.route(method, path, handler)   register an HTTP route on the host
//	                                      engine; handler receives (req, res)
//	                                      and finishes via res.end() or an
//	                                      awaited req.upgradeWebSocket()
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
//	server.registerNotificationChannel(id, configuration, handler)
//	                                      register a notification channel
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
	registry.RegisterNativeModule("server", func(vm *moejs.Runtime, module *engine.Object) {
		exports := vm.Realm().NewObject()
		_ = native.Function(vm, exports, "route", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			method := strings.ToUpper(strings.TrimSpace(native.String(vm, moejs.Arg(args, 0))))
			path := native.String(vm, moejs.Arg(args, 1))
			handler := moejs.Arg(args, 2)
			ok := engine.IsCallable(handler)
			if method == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.route requires an HTTP method")
			}
			if path == "" || !strings.HasPrefix(path, "/") {
				return moejs.Undefined(), native.TypeError(vm, "server.route requires a path starting with /")
			}
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "server.route requires a function handler")
			}
			if !inst.info.Permissions.AllowRoutes {
				return moejs.Undefined(), native.TypeError(vm, "server.route requires the \"route\" permission (allowRoutes)")
			}
			if err := m.registerRoute(inst.info.Short, method, path, handler); err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "static", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			mount := strings.TrimSpace(native.String(vm, moejs.Arg(args, 0)))
			mount = strings.TrimRight(mount, "/")
			dir := strings.TrimSpace(native.String(vm, moejs.Arg(args, 1)))
			if mount == "" || mount == "/" || !strings.HasPrefix(mount, "/") {
				return moejs.Undefined(), native.TypeError(vm, "server.static requires a mount path starting with / (and not \"/\")")
			}
			if dir == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.static requires a folder name")
			}
			if !filepath.IsLocal(dir) {
				return moejs.Undefined(), native.TypeError(vm, "server.static folder must be a relative path inside the plugin directory")
			}
			if !inst.info.Permissions.AllowRoutes {
				return moejs.Undefined(), native.TypeError(vm, "server.static requires the \"route\" permission (allowRoutes)")
			}
			var opts map[string]any
			if !moejs.Arg(args, 2).IsUndefined() && !moejs.Arg(args, 2).IsNull() {
				if err := native.ExportTo(vm, moejs.Arg(args, 2), &opts); err != nil {
					return moejs.Undefined(), native.TypeError(vm, "server.static options must be an object like { spa: true }")
				}
			}
			spa := false
			if v, ok := opts["spa"]; ok {
				spa, _ = v.(bool)
			}
			if err := m.registerStatic(inst.info.Short, mount, dir, spa); err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "call", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			method := strings.TrimSpace(native.String(vm, moejs.Arg(args, 0)))
			if method == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.call requires an RPC method name")
			}
			params := callParams(vm, args)
			promise, resolve, reject := vm.NewPromise()
			go func() {
				if !inst.info.Permissions.AllowSystemRPC {
					host.RunOnLoop(func(vm *moejs.Runtime) {
						_ = reject(native.GoError(vm, errors.New("server.call requires the \"system RPC\" permission (allowSystemRPC)")))
					})
					return
				}
				meta := &rpc.ContextMeta{Permission: rpc.RoleAdmin, Principal: rpc.PrincipalFromRole(rpc.RoleAdmin)}
				ctx := rpc.NewContextWithMeta(context.Background(), meta)
				resp := rpc.CallWithContext(ctx, nil, method, params)
				host.RunOnLoop(func(vm *moejs.Runtime) {
					if resp.Error != nil {
						_ = reject(engine.ObjectValue(jsRPCError(vm, resp.Error)))
						return
					}
					result, err := normalizeRPCValue(resp.Result)
					if err != nil {
						_ = reject(native.GoError(vm, err))
						return
					}
					_ = resolve(native.From(vm, result))
				})
			}()
			return promise, nil
		})
		_ = native.Function(vm, exports, "hook", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			kind := strings.ToLower(strings.TrimSpace(native.String(vm, moejs.Arg(args, 0))))
			switch kind {
			case "request", "response", string(hookWSConnect), string(hookWSMessage), string(hookWSSend), string(hookWSClose):
				if !inst.info.Permissions.AllowHooks {
					return moejs.Undefined(), native.TypeError(vm, "server.hook requires the \"hook\" permission (allowHooks)")
				}
			default:
				return moejs.Undefined(), native.TypeError(vm, "server.hook kind must be \"request\", \"response\", \"wsConnect\", \"wsMessage\", \"wsSend\" or \"wsClose\"")
			}
			// server.hook(kind, fn) or server.hook(kind, matcher, fn) where
			// matcher is "METHOD /path", "/path" or "/path/*". The ws kinds
			// only accept a path matcher: every upgrade is a GET request.
			fnValue := moejs.Arg(args, 1)
			var matcher *hookMatcher
			if fnValue.IsString() {
				parsed, err := parseHookMatcher(native.String(vm, fnValue))
				if err != nil {
					return moejs.Undefined(), native.TypeError(vm, "%s", err.Error())
				}
				if isWSHookKind(kind) && parsed.method != "" {
					return moejs.Undefined(), native.TypeError(vm, "server.hook %q only accepts a path matcher like /api/*", kind)
				}
				matcher = parsed
				fnValue = moejs.Arg(args, 2)
			}
			fn := fnValue
			ok := engine.IsCallable(fn)
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "server.hook requires a function handler")
			}
			m.registerHook(inst.info.Short, hookKind(kind), fn, matcher)
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "injectHTML", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			if !inst.info.Permissions.AllowHTMLInject {
				return moejs.Undefined(), native.TypeError(vm, "server.injectHTML requires the \"HTML inject\" permission (allowHTMLInject)")
			}
			m.registerInject(inst.info.Short, native.String(vm, moejs.Arg(args, 0)), native.String(vm, moejs.Arg(args, 1)))
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "cron", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			spec := strings.TrimSpace(native.String(vm, moejs.Arg(args, 0)))
			fn := moejs.Arg(args, 1)
			ok := engine.IsCallable(fn)
			if spec == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.cron requires a cron expression")
			}
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "server.cron requires a function handler")
			}
			if err := m.registerCron(inst.info.Short, spec, fn); err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "registerRPC", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			method := strings.TrimSpace(native.String(vm, moejs.Arg(args, 0)))
			fn := moejs.Arg(args, 1)
			ok := engine.IsCallable(fn)
			if method == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.registerRPC requires an RPC method name")
			}
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "server.registerRPC requires a function handler")
			}
			if err := m.registerRPC(inst.info.Short, method, fn); err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "registerNotificationChannel", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			id := strings.TrimSpace(native.String(vm, moejs.Arg(args, 0)))
			handler := moejs.Arg(args, 2)
			ok := engine.IsCallable(handler)
			if id == "" {
				return moejs.Undefined(), native.TypeError(vm, "server.registerNotificationChannel requires a channel id")
			}
			if !ok {
				return moejs.Undefined(), native.TypeError(vm, "server.registerNotificationChannel requires a function handler")
			}
			rawConfiguration, err := exportJSValue(vm, moejs.Arg(args, 1))
			if err != nil {
				return moejs.Undefined(), native.TypeError(vm, "server.registerNotificationChannel requires a managed configuration object")
			}
			configurationJSON, err := json.Marshal(rawConfiguration)
			if err != nil {
				return moejs.Undefined(), native.TypeError(vm, "server.registerNotificationChannel requires a managed configuration object")
			}
			var configuration models.Configuration
			if err := json.Unmarshal(configurationJSON, &configuration); err != nil {
				return moejs.Undefined(), native.TypeError(vm, "server.registerNotificationChannel requires a managed configuration object")
			}
			if err := m.registerNotificationChannel(inst.info.Short, id, configuration, handler, host); err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Undefined(), nil
		})
		_ = native.Function(vm, exports, "getConfig", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			promise, resolve, reject := vm.NewPromise()
			go func() {
				data, err := GetConfiguration(inst.info.Short)
				host.RunOnLoop(func(vm *moejs.Runtime) {
					if err != nil {
						_ = reject(native.GoError(vm, err))
						return
					}
					_ = resolve(native.From(vm, data))
				})
			}()
			return promise, nil
		})
		_ = native.Set(vm, module, "exports", exports)
	})
}

// callParams converts JS arguments into JSON-RPC params: no params, a single
// value, or a positional array. Values that cannot be exported (for example
// circular objects) are skipped so the call rejects instead of panicking.
func callParams(vm *moejs.Runtime, args []moejs.Value) any {
	switch n := len(args); {
	case n <= 1:
		return nil
	case n == 2:
		exported, _ := exportJSValue(vm, moejs.Arg(args, 1))
		return exported
	default:
		params := make([]any, 0, n-1)
		for i := 1; i < n; i++ {
			exported, err := exportJSValue(vm, moejs.Arg(args, i))
			if err != nil {
				return nil
			}
			params = append(params, exported)
		}
		return params
	}
}

func jsRPCError(vm *moejs.Runtime, e *rpc.JsonRpcError) *engine.Object {
	obj := native.Object(vm, native.GoError(vm, fmt.Errorf("%s", e.Message)))
	_ = native.Set(vm, obj, "code", e.Code)
	if e.Data != nil {
		_ = native.Set(vm, obj, "data", e.Data)
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
func (m *Manager) registerRoute(short, method, path string, handler moejs.Value) (err error) {
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
	// A missing map entry yields a zero moejs.Value, not JavaScript undefined.
	if _, registered := inst.handlers[key]; !slotExists && registered {
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
		if !alive || handler.IsUndefined() || host == nil {
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
		state.upgrades = make(chan *routeUpgrade, 1)
		defer state.seal()
		// Gin recycles c when the request returns, including on a timeout while
		// this turn is still queued. Only the HTTP pump uses the original.
		snapshot := c.Copy()
		snapshot.Request = c.Request.Clone(c.Request.Context())
		if principal := rpcPrincipalFromGin(c); principal != nil {
			copy := *principal
			copy.Roles = append([]string(nil), principal.Roles...)
			snapshot.Set(principalContextKey, &copy)
		}
		queued := host.RunOnLoop(func(vm *moejs.Runtime) {
			req := routeRequest(vm, snapshot, body)
			res := routeResponseObject(vm, state)
			bindRouteUpgrade(vm, req, res, state, host, snapshot.Request)
			runErr := host.RunJob(vm, "plugin route "+short, func() error {
				_, callErr := vm.CallFunction(handler, moejs.Undefined(), engine.ObjectValue(req), engine.ObjectValue(res))
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
			case upgrade := <-state.upgrades:
				state.mu.Lock()
				state.sealed = true
				state.upgradeClaimed = true
				state.mu.Unlock()
				idle.Stop()
				if c.Request.Context().Err() != nil || !host.ResourcesOpen() {
					upgrade.ws.abort()
					upgrade.complete(errors.New("WebSocket upgrade canceled"))
					return
				}
				stopCancel := context.AfterFunc(c.Request.Context(), upgrade.ws.abort)
				original := c.Writer
				c.Writer = &routeUpgradeWriter{ResponseWriter: original, ws: upgrade.ws}
				socket, upgradeErr := api.UpgradeSafeConn(c, func(upgrader *websocket.Upgrader) {
					upgrader.HandshakeTimeout = host.Timeout()
				})
				c.Writer = original
				stopCancel()
				if upgradeErr != nil {
					upgrade.ws.abort()
					upgrade.complete(upgradeErr)
					return
				}
				if !upgrade.ws.attach(socket) {
					upgrade.complete(errors.New("WebSocket upgrade canceled"))
					return
				}
				upgrade.complete(nil)
				return
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
				state.seal()
				if !committed {
					writeFinal(0, nil)
				}
				return
			case <-c.Request.Context().Done():
				state.seal()
				return
			case <-idle.C:
				state.seal()
				if state.isStreaming() {
					// Streaming idle timeout: end the stream and notify JS
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

	wrote          bool
	sealed         bool
	upgradeClaimed bool
	upgrade        *routeUpgrade
	upgrades       chan *routeUpgrade
}

// routeUpgrade keeps the Promise on its original loop. No network goroutine
// touches the resolver or socket object directly.
type routeUpgrade struct {
	ws       *routeWebSocket
	complete func(error)
}

type routeUpgradeWriter struct {
	gin.ResponseWriter
	ws *routeWebSocket
}

func (w *routeUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.ws.captureConn(conn)
	}
	return conn, rw, err
}

func bindRouteUpgrade(vm *moejs.Runtime, req, res *engine.Object, state *routeResponse, host *jsruntime.Host, request *http.Request) {
	_ = native.Function(vm, req, "upgradeWebSocket", func(_ *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
		promise, resolve, reject := vm.NewPromise()
		fail := func(message string) (moejs.Value, error) {
			_ = reject(native.GoError(vm, errors.New(message)))
			return native.From(vm, promise), nil
		}
		streaming := native.Bool(native.Get(vm, res, "streaming"))
		state.mu.Lock()
		if state.sealed || state.ended || state.upgrade != nil || state.wrote || state.streaming || streaming {
			state.mu.Unlock()
			return fail("HTTP response is no longer available for WebSocket upgrade")
		}
		if request.Method != http.MethodGet || !websocket.IsWebSocketUpgrade(request) {
			state.mu.Unlock()
			return fail("require a GET WebSocket upgrade request")
		}
		ws := newRouteWebSocket(host)
		u := &routeUpgrade{ws: ws}
		u.complete = func(upgradeErr error) {
			if !host.RunOnLoop(func(vm *moejs.Runtime) {
				if !host.ResourcesOpen() {
					ws.abort()
					return
				}
				err := host.RunJob(vm, "WebSocket upgrade", func() error {
					if upgradeErr != nil {
						return reject(native.GoError(vm, upgradeErr))
					}
					return resolve(engine.ObjectValue(ws.object(vm)))
				})
				if err != nil || upgradeErr != nil {
					ws.abort()
					return
				}
				// Promise continuations install handlers before a later loop turn
				// starts delivering messages (including an immediate peer send).
				if !host.RunOnLoop(func(_ *moejs.Runtime) { ws.start() }) {
					ws.abort()
				}
			}) {
				ws.abort()
			}
		}
		state.upgrade = u
		state.mu.Unlock()
		state.upgrades <- u
		return native.From(vm, promise), nil
	})
}

// seal prevents queued JavaScript from writing or upgrading a returned request.
func (state *routeResponse) seal() {
	state.mu.Lock()
	wasSealed := state.sealed
	state.sealed = true
	pending := state.upgrade
	claimed := state.upgradeClaimed
	state.mu.Unlock()
	if state.aborted.CompareAndSwap(false, true) {
		close(state.abortCh)
	}
	if pending != nil && !claimed && !wasSealed {
		pending.ws.abort()
		pending.complete(errors.New("WebSocket upgrade canceled"))
	}
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
	if state.ended || state.sealed || state.upgrade != nil {
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

func routeRequest(vm *moejs.Runtime, c *gin.Context, body []byte) *engine.Object {
	req := hookRequestObject(vm, c.Request, body)
	_ = native.Set(vm, req, "context", routeRequestContext(vm, c))
	return req
}

func routeResponseObject(vm *moejs.Runtime, state *routeResponse) *engine.Object {
	res := vm.Realm().NewObject()
	_ = native.Set(vm, res, "statusCode", http.StatusOK)
	_ = native.Set(vm, res, "statusMessage", "")
	_ = native.Set(vm, res, "streaming", false)
	_ = native.Function(vm, res, "isAborted", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, state.aborted.Load()), nil
	})
	_ = native.Function(vm, res, "setHeader", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name, value := native.String(vm, moejs.Arg(args, 0)), moejs.Arg(args, 1)
		var values []string
		if err := native.ExportTo(vm, value, &values); err != nil {
			values = []string{native.String(vm, value)}
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.upgrade != nil {
			return moejs.Undefined(), native.TypeError(vm, "HTTP response is unavailable after WebSocket upgrade")
		}
		if !state.sealed {
			state.headers[http.CanonicalHeaderKey(name)] = values
		}
		return engine.ObjectValue(res), nil
	})
	_ = native.Function(vm, res, "getHeader", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		values := state.headers.Values(native.String(vm, moejs.Arg(args, 0)))
		if len(values) == 0 {
			return moejs.Undefined(), nil
		}
		if len(values) == 1 {
			return native.From(vm, values[0]), nil
		}
		return native.From(vm, values), nil
	})
	_ = native.Function(vm, res, "removeHeader", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.upgrade != nil {
			return moejs.Undefined(), native.TypeError(vm, "HTTP response is unavailable after WebSocket upgrade")
		}
		if !state.sealed {
			state.headers.Del(name)
		}
		return moejs.Undefined(), nil
	})
	_ = native.Function(vm, res, "write", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := moejs.Arg(args, 0)
		var data []byte
		if err := native.ExportTo(vm, value, &data); err != nil {
			data = []byte(native.String(vm, value))
		}
		streaming := native.Bool(native.Get(vm, res, "streaming"))
		state.mu.Lock()
		if state.upgrade != nil {
			state.mu.Unlock()
			return moejs.Undefined(), native.TypeError(vm, "HTTP response is unavailable after WebSocket upgrade")
		}
		if state.sealed || state.ended {
			state.mu.Unlock()
			return native.From(vm, false), nil
		}
		state.wrote = true
		if streaming {
			state.streaming = true
			state.mu.Unlock()
			return native.From(vm, state.pushChunk(data)), nil
		}
		_, _ = state.body.Write(data)
		state.mu.Unlock()
		return native.From(vm, true), nil
	})
	_ = native.Function(vm, res, "end", func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		var tail string
		if len(args) > 0 && !native.IsNil(args[0]) {
			tail = native.String(vm, args[0])
		}
		status := int(native.Int(vm, native.Get(vm, res, "statusCode")))
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.upgrade != nil {
			return moejs.Undefined(), native.TypeError(vm, "HTTP response is unavailable after WebSocket upgrade")
		}
		if state.sealed || state.ended {
			return engine.ObjectValue(res), nil
		}
		_, _ = state.body.WriteString(tail)
		state.statusCode = status
		if !state.ended {
			state.ended = true
			close(state.done)
		}
		return engine.ObjectValue(res), nil
	})
	return res
}
