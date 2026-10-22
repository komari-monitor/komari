package http

import (
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)
import (
	"bytes"
	_ "embed"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nodeHTTPResponse struct {
	mu         sync.Mutex
	headers    http.Header
	body       bytes.Buffer
	statusCode int
	statusText string
	ended      bool
	done       chan struct {
	}
}
type Module struct {
	runtime        *bridge.Runtime
	allowListen    bool
	maxBodyBytes   int64
	streamMu       sync.Mutex
	createIncoming moejs.Value
	createResponse moejs.Value
}

func New(runtime *bridge.Runtime, allowListen bool, maxBodyBytes int64) *Module {
	return &Module{runtime: runtime, allowListen: allowListen, maxBodyBytes: maxBodyBytes}
}
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()
	_ = native.Set(vm, exports, "createServer", native.Bind(vm, "createServer", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		server := m.newHTTPServer(vm)
		listener := moejs.Arg(args, 0)
		if _, ok := callable(listener); !ok {
			listener = moejs.Arg(args, 1)
		}
		if _, ok := callable(listener); ok {
			on, _ := callable(native.Get(vm, server, "on"))
			_, _ = native.Call(vm, on, native.From(vm, server), native.From(vm, "request"), native.From(vm, listener))
		}
		return native.From(vm, server), nil
	}))
	_ = native.Set(vm, exports, "METHODS", []string{"ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD", "LINK", "LOCK", "M-SEARCH", "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL", "MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST", "PROPFIND", "PROPPATCH", "PURGE", "PUT", "QUERY", "REBIND", "REPORT", "SEARCH", "SOURCE", "SUBSCRIBE", "TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE"})
	statusCodes := make(map[int]string)
	for code := 100; code <= 599; code++ {
		if text := http.StatusText(code); text != "" {
			statusCodes[code] = text
		}
	}
	_ = native.Set(vm, exports, "STATUS_CODES", statusCodes)
	_ = native.Set(vm, exports, "maxHeaderSize", 1<<20)
	_ = native.Set(vm, exports, "validateHeaderName", native.Bind(vm, "validateHeaderName", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		if !validHTTPToken(name) {
			panic(native.TypeError(vm, "Invalid HTTP header name"))
		}
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, exports, "validateHeaderValue", native.Bind(vm, "validateHeaderValue", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		value := native.String(vm, moejs.Arg(args, 1))
		if strings.ContainsAny(value, "\x00\r\n") {
			panic(native.TypeError(vm, "Invalid value for header %s", name))
		}
		return moejs.Undefined(), nil
	}))
	m.attachHTTPClient(vm, exports)
	_ = native.Set(vm, module, "exports", exports)
}
func (m *Module) newHTTPServer(vm *moejs.Runtime) *engine.Object {
	serverObject := events.NewEmitter(vm)
	var server *http.Server
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	var listenGeneration uint64
	var listenPending bool
	var readTimeout time.Duration
	_ = native.Set(vm, serverObject, "listening", false)
	_ = native.Set(vm, serverObject, "timeout", 0)
	_ = native.Set(vm, serverObject, "keepAliveTimeout", 5000)
	_ = native.Set(vm, serverObject, "headersTimeout", 60000)
	_ = native.Set(vm, serverObject, "requestTimeout", 300000)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := httpbody.ReadAll(request.Body, m.maxBodyBytes)
		if err != nil {
			http.Error(response, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		state := &nodeHTTPResponse{headers: make(http.Header), statusCode: http.StatusOK, done: make(chan struct {
		})}
		queued := m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
			incoming := m.httpIncomingMessage(vm, request, body)
			outgoing := m.httpServerResponse(vm, state)
			err := m.runtime.RunJob(vm, "http.Server request", func() error {
				return events.Emit(vm, serverObject, "request", incoming, outgoing)
			})
			if err != nil {
				state.finish(http.StatusInternalServerError, nil)
				return
			}
			m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "http.IncomingMessage body", func() error {
					push, _ := callable(native.Get(vm, incoming, "push"))
					if len(body) > 0 {
						if _, pushErr := native.Call(vm, push, native.From(vm, incoming), native.From(vm, buffer.WrapBytes(vm, append([]byte(nil), body...)))); pushErr != nil {
							return pushErr
						}
					}
					_, pushErr := native.Call(vm, push, native.From(vm, incoming), native.From(vm, moejs.Null()))
					return pushErr
				})
			}, 0)
		})
		if !queued {
			http.Error(response, "JavaScript runtime is closed", http.StatusServiceUnavailable)
			return
		}
		timer := time.NewTimer(m.runtime.Timeout())
		defer timer.Stop()
		select {
		case <-state.done:
		case <-request.Context().Done():
			return
		case <-timer.C:
			state.finish(http.StatusGatewayTimeout, []byte("JavaScript request handler timed out"))
		}
		state.mu.Lock()
		for name, values := range state.headers {
			for _, value := range values {
				response.Header().Add(name, value)
			}
		}
		statusCode := state.statusCode
		responseBody := append([]byte(nil), state.body.Bytes()...)
		state.mu.Unlock()
		response.WriteHeader(statusCode)
		_, _ = response.Write(responseBody)
	})
	_ = native.Set(vm, serverObject, "listen", native.Bind(vm, "listen", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if !m.allowListen {
			panic(native.GoError(vm, fmt.Errorf("http.Server.listen requires AllowListen")))
		}
		address, callback := listenArguments(vm, args)
		serverMu.Lock()
		if listenPending || listener != nil {
			serverMu.Unlock()
			panic(native.GoError(vm, fmt.Errorf("server is already listening")))
		}
		listenGeneration++
		generation := listenGeneration
		listenPending = true
		configuredReadTimeout := readTimeout
		serverMu.Unlock()
		go func() {
			created, err := net.Listen("tcp", address)
			if err != nil {
				serverMu.Lock()
				active := listenPending && listenGeneration == generation
				if active {
					listenPending = false
				}
				serverMu.Unlock()
				if active {
					m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
						_ = m.runtime.RunJob(vm, "http.Server error", func() error {
							return events.Emit(vm, serverObject, "error", native.GoError(vm, err))
						})
					})
				}
				return
			}
			createdServer := &http.Server{Handler: handler, ReadHeaderTimeout: m.runtime.Timeout(), ReadTimeout: configuredReadTimeout}
			serverMu.Lock()
			if !listenPending || listenGeneration != generation {
				serverMu.Unlock()
				_ = created.Close()
				return
			}
			id := m.runtime.AddResource(func() {
				_ = createdServer.Close()
				_ = created.Close()
			})
			if id == 0 {
				listenPending = false
				serverMu.Unlock()
				return
			}
			server = createdServer
			listener = created
			resourceID = id
			listenPending = false
			serverMu.Unlock()
			m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
				serverMu.RLock()
				active := listenGeneration == generation && server == createdServer && listener == created
				serverMu.RUnlock()
				if !active {
					return
				}
				if !native.IsNil(callback) {
					once, _ := callable(native.Get(vm, serverObject, "once"))
					_, _ = native.Call(vm, once, native.From(vm, serverObject), native.From(vm, "listening"), native.From(vm, callback))
				}
				_ = m.runtime.RunJob(vm, "http.Server listening", func() error {
					_ = native.Set(vm, serverObject, "listening", true)
					return events.Emit(vm, serverObject, "listening")
				})
			})
			err = createdServer.Serve(created)
			serverMu.Lock()
			active := listenGeneration == generation && server == createdServer
			if active {
				server, listener, resourceID = nil, nil, 0
			}
			serverMu.Unlock()
			if active {
				m.runtime.RemoveResource(id)
			}
			if active && err != nil && err != http.ErrServerClosed {
				m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					_ = m.runtime.RunJob(vm, "http.Server error", func() error {
						return events.Emit(vm, serverObject, "error", native.GoError(vm, err))
					})
				})
			}
		}()
		return native.From(vm, serverObject), nil
	}))
	_ = native.Set(vm, serverObject, "close", native.Bind(vm, "close", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if callback, ok := callable(moejs.Arg(args, 0)); ok {
			once, _ := callable(native.Get(vm, serverObject, "once"))
			_, _ = native.Call(vm, once, native.From(vm, serverObject), native.From(vm, "close"), native.From(vm, callback))
		}
		serverMu.Lock()
		wasActive := listenPending || listener != nil
		listenGeneration++
		listenPending = false
		currentServer, currentListener, currentResourceID := server, listener, resourceID
		server, listener, resourceID = nil, nil, 0
		serverMu.Unlock()
		if currentServer != nil {
			_ = currentServer.Close()
		}
		if currentListener != nil {
			_ = currentListener.Close()
		}
		if currentResourceID != 0 {
			m.runtime.RemoveResource(currentResourceID)
		}
		_ = native.Set(vm, serverObject, "listening", false)
		if wasActive {
			m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "http.Server close", func() error {
					return events.Emit(vm, serverObject, "close")
				})
			}, 0)
		}
		return native.From(vm, serverObject), nil
	}))
	_ = native.Set(vm, serverObject, "closeAllConnections", native.Bind(vm, "closeAllConnections", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		serverMu.RLock()
		current := server
		serverMu.RUnlock()
		if current != nil {
			_ = current.Close()
		}
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, serverObject, "closeIdleConnections", native.Bind(vm, "closeIdleConnections", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("http.Server.closeIdleConnections is not supported by jsruntime; net/http does not expose individual idle server connections")))
	}))
	_ = native.Set(vm, serverObject, "address", native.Bind(vm, "address", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		serverMu.RLock()
		current := listener
		serverMu.RUnlock()
		if current == nil {
			return native.From(vm, moejs.Null()), nil
		}
		host, port, _ := net.SplitHostPort(current.Addr().String())
		portNumber, _ := strconv.Atoi(port)
		return native.From(vm, map[string]any{"address": host, "family": addressFamily(host), "port": portNumber}), nil
	}))
	_ = native.Set(vm, serverObject, "setTimeout", native.Bind(vm, "setTimeout", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		milliseconds := native.Int(vm, moejs.Arg(args, 0))
		callback := moejs.Arg(args, 1)
		serverMu.Lock()
		readTimeout = time.Duration(milliseconds) * time.Millisecond
		serverMu.Unlock()
		if function, ok := callable(callback); ok {
			on, _ := callable(native.Get(vm, serverObject, "on"))
			_, _ = native.Call(vm, on, native.From(vm, serverObject), native.From(vm, "timeout"), native.From(vm, function))
		}
		return native.From(vm, serverObject), nil
	}))
	_ = native.Set(vm, serverObject, "ref", native.Bind(vm, "ref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("http.Server.ref is not supported by jsruntime; the event loop is host-driven")))
	}))
	_ = native.Set(vm, serverObject, "unref", native.Bind(vm, "unref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("http.Server.unref is not supported by jsruntime; the event loop is host-driven")))
	}))
	return serverObject
}
func (state *nodeHTTPResponse) finish(status int, body []byte) {
	state.mu.Lock()
	if state.ended {
		state.mu.Unlock()
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
	state.mu.Unlock()
}
func (m *Module) httpIncomingMessage(vm *moejs.Runtime, request *http.Request, body []byte) *engine.Object {
	m.initHTTPStreams(vm)
	value, err := native.Call(vm, m.createIncoming, native.From(vm, moejs.Undefined()))
	if err != nil {
		panic(err)
	}
	incoming := native.Object(vm, value)
	headers := make(map[string]string, len(request.Header))
	rawHeaders := make([]string, 0, len(request.Header)*2)
	for name, values := range request.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
		for _, value := range values {
			rawHeaders = append(rawHeaders, name, value)
		}
	}
	_ = native.Set(vm, incoming, "method", request.Method)
	_ = native.Set(vm, incoming, "url", request.URL.RequestURI())
	_ = native.Set(vm, incoming, "headers", headers)
	_ = native.Set(vm, incoming, "headersDistinct", request.Header.Clone())
	_ = native.Set(vm, incoming, "rawHeaders", rawHeaders)
	_ = native.Set(vm, incoming, "httpVersion", fmt.Sprintf("%d.%d", request.ProtoMajor, request.ProtoMinor))
	_ = native.Set(vm, incoming, "httpVersionMajor", request.ProtoMajor)
	_ = native.Set(vm, incoming, "httpVersionMinor", request.ProtoMinor)
	_ = native.Set(vm, incoming, "complete", true)
	_ = native.Set(vm, incoming, "aborted", false)
	_ = native.Set(vm, incoming, "socket", httpSocketInfo(vm, request))
	_ = native.Set(vm, incoming, "connection", native.Get(vm, incoming, "socket"))
	_ = native.Set(vm, incoming, "_destroy", native.Bind(vm, "_destroy", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		err := moejs.Arg(args, 0)
		callback := moejs.Arg(args, 1)
		_ = request.Body.Close()
		_, _ = native.Call(vm, callback, native.From(vm, moejs.Undefined()), native.From(vm, err))
		return moejs.Undefined(), nil
	}))
	return incoming
}
func httpSocketInfo(vm *moejs.Runtime, request *http.Request) *engine.Object {
	socket := events.NewEmitter(vm)
	remoteHost, remotePort, _ := net.SplitHostPort(request.RemoteAddr)
	localHost, localPort := "", 0
	if local, ok := request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		localHost, localPort = splitAddress(local)
	}
	remotePortNumber, _ := strconv.Atoi(remotePort)
	_ = native.Set(vm, socket, "remoteAddress", remoteHost)
	_ = native.Set(vm, socket, "remotePort", remotePortNumber)
	_ = native.Set(vm, socket, "remoteFamily", addressFamily(remoteHost))
	_ = native.Set(vm, socket, "localAddress", localHost)
	_ = native.Set(vm, socket, "localPort", localPort)
	_ = native.Set(vm, socket, "localFamily", addressFamily(localHost))
	_ = native.Set(vm, socket, "encrypted", request.TLS != nil)
	return socket
}
func (m *Module) httpServerResponse(vm *moejs.Runtime, state *nodeHTTPResponse) *engine.Object {
	m.initHTTPStreams(vm)
	hooks := vm.Realm().NewObject()
	_ = native.Set(vm, hooks, "write", native.Bind(vm, "write", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		response := native.Object(vm, moejs.Arg(args, 0))
		chunk := moejs.Arg(args, 1)
		callback := moejs.Arg(args, 2)
		state.mu.Lock()
		_, _ = state.body.Write(buffer.Bytes(vm, chunk))
		state.mu.Unlock()
		_ = native.Set(vm, response, "headersSent", true)
		if !native.IsNil(callback) {
			_, _ = native.Call(vm, callback, native.From(vm, moejs.Undefined()))
		}
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, hooks, "finish", native.Bind(vm, "finish", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		response := native.Object(vm, moejs.Arg(args, 0))
		callback := moejs.Arg(args, 1)
		state.mu.Lock()
		state.statusCode = int(native.Int(vm, native.Get(vm, response, "statusCode")))
		state.statusText = native.String(vm, native.Get(vm, response, "statusMessage"))
		if !state.ended {
			state.ended = true
			close(state.done)
		}
		state.mu.Unlock()
		_ = native.Set(vm, response, "headersSent", true)
		if !native.IsNil(callback) {
			_, _ = native.Call(vm, callback, native.From(vm, moejs.Undefined()))
		}
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, hooks, "destroy", native.Bind(vm, "destroy", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state.finish(0, nil)
		return moejs.Undefined(), nil
	}))
	value, err := native.Call(vm, m.createResponse, native.From(vm, moejs.Undefined()), native.From(vm, hooks))
	if err != nil {
		panic(err)
	}
	response := native.Object(vm, value)
	_ = native.Set(vm, response, "statusCode", http.StatusOK)
	_ = native.Set(vm, response, "statusMessage", "")
	_ = native.Set(vm, response, "headersSent", false)
	_ = native.Set(vm, response, "sendDate", true)
	_ = native.Set(vm, response, "setHeader", native.Bind(vm, "setHeader", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		value := moejs.Arg(args, 1)
		state.mu.Lock()
		defer state.mu.Unlock()
		var values []string
		if err := native.ExportTo(vm, value, &values); err == nil {
			state.headers[name] = values
		} else {
			state.headers.Set(name, native.String(vm, value))
		}
		return native.From(vm, response), nil
	}))
	_ = native.Set(vm, response, "appendHeader", native.Bind(vm, "appendHeader", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		value := native.String(vm, moejs.Arg(args, 1))
		state.mu.Lock()
		state.headers.Add(name, value)
		state.mu.Unlock()
		return native.From(vm, response), nil
	}))
	_ = native.Set(vm, response, "getHeader", native.Bind(vm, "getHeader", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		state.mu.Lock()
		defer state.mu.Unlock()
		values := state.headers.Values(name)
		if len(values) == 0 {
			return native.From(vm, moejs.Undefined()), nil
		}
		if len(values) == 1 {
			return native.From(vm, values[0]), nil
		}
		return native.From(vm, values), nil
	}))
	_ = native.Set(vm, response, "getHeaders", native.Bind(vm, "getHeaders", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		return native.From(vm, state.headers.Clone()), nil
	}))
	_ = native.Set(vm, response, "getHeaderNames", native.Bind(vm, "getHeaderNames", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		state.mu.Lock()
		defer state.mu.Unlock()
		names := make([]string, 0, len(state.headers))
		for name := range state.headers {
			names = append(names, strings.ToLower(name))
		}
		return native.From(vm, names), nil
	}))
	_ = native.Set(vm, response, "hasHeader", native.Bind(vm, "hasHeader", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		state.mu.Lock()
		defer state.mu.Unlock()
		return native.From(vm, len(state.headers.Values(name)) > 0), nil
	}))
	_ = native.Set(vm, response, "removeHeader", native.Bind(vm, "removeHeader", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		state.mu.Lock()
		state.headers.Del(name)
		state.mu.Unlock()
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, response, "writeHead", native.Bind(vm, "writeHead", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		_ = native.Set(vm, response, "statusCode", native.Int(vm, moejs.Arg(args, 0)))
		if text := moejs.Arg(args, 1); !text.IsUndefined() {
			if _, ok := asObject(text); ok {
				applyHTTPHeaders(vm, response, text)
			} else {
				_ = native.Set(vm, response, "statusMessage", native.String(vm, text))
				applyHTTPHeaders(vm, response, moejs.Arg(args, 2))
			}
		}
		_ = native.Set(vm, response, "headersSent", true)
		return native.From(vm, response), nil
	}))
	_ = native.Set(vm, response, "flushHeaders", native.Bind(vm, "flushHeaders", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		_ = native.Set(vm, response, "headersSent", true)
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, response, "writeContinue", native.Bind(vm, "writeContinue", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("http.ServerResponse.writeContinue is not supported by jsruntime; the Go server never sends 100 Continue")))
	}))
	_ = native.Set(vm, response, "setTimeout", native.Bind(vm, "setTimeout", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		milliseconds := native.Int(vm, moejs.Arg(args, 0))
		callback := moejs.Arg(args, 1)
		if function, ok := callable(callback); ok {
			m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "http.ServerResponse timeout", func() error {
					_, err := native.Call(vm, function, native.From(vm, response))
					return err
				})
			}, time.Duration(milliseconds)*time.Millisecond)
		}
		return native.From(vm, response), nil
	}))
	return response
}
func applyHTTPHeaders(vm *moejs.Runtime, response *engine.Object, value moejs.Value) {
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return
	}
	setHeader, _ := callable(native.Get(vm, response, "setHeader"))
	object := native.Object(vm, value)
	for _, name := range objectKeys(object) {
		_, _ = native.Call(vm, setHeader, native.From(vm, response), native.From(vm, name), native.From(vm, native.Get(vm, object, name)))
	}
}
func listenArguments(vm *moejs.Runtime, args []moejs.Value) (string, moejs.Value) {
	host := "127.0.0.1"
	port := int(native.Int(vm, moejs.Arg(args, 0)))
	callbackIndex := 1
	if object, ok := asObject(moejs.Arg(args, 0)); ok {
		port = int(native.Int(vm, native.Get(vm, object, "port")))
		if value := native.Get(vm, object, "host"); !value.IsUndefined() {
			host = native.String(vm, value)
		}
	} else if value := moejs.Arg(args, 1); !value.IsUndefined() {
		if _, ok := callable(value); !ok {
			host = native.String(vm, value)
			callbackIndex = 2
		}
	}
	callback, _ := callable(moejs.Arg(args, callbackIndex))
	return net.JoinHostPort(host, strconv.Itoa(port)), callback
}
func splitAddress(address net.Addr) (string, int) {
	if address == nil {
		return "", 0
	}
	host, port, _ := net.SplitHostPort(address.String())
	portNumber, _ := strconv.Atoi(port)
	return host, portNumber
}
func addressFamily(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "IPv6"
	}
	return "IPv4"
}
func (m *Module) attachHTTPClient(vm *moejs.Runtime, exports *engine.Object) {
	factoryValue, err := native.Run(vm, httpClientSource)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	factory, _ := callable(factoryValue)
	client, err := native.Call(vm, factory, native.From(vm, moejs.Undefined()), native.From(vm, require.Require(vm, "events")), native.From(vm, native.Global(vm, "fetch")), native.From(vm, native.Global(vm, "AbortController")), native.From(vm, native.Global(vm, "Blob")))
	if err != nil {
		panic(err)
	}
	clientObject := native.Object(vm, client)
	for _, name := range objectKeys(clientObject) {
		_ = native.Set(vm, exports, name, native.Get(vm, clientObject, name))
	}
}
func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character <= 32 || character >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}\t", character) {
			return false
		}
	}
	return true
}

func (m *Module) initHTTPStreams(vm *moejs.Runtime) {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if engine.IsCallable(m.createIncoming) {
		return
	}
	factoryValue, err := native.Run(vm, httpStreamsSource)
	if err != nil {
		panic(native.GoError(vm, fmt.Errorf("load http streams: %w", err)))
	}
	factory, _ := callable(factoryValue)
	value, err := native.Call(vm, factory, native.From(vm, moejs.Undefined()), native.From(vm, require.Require(vm, "stream")))
	if err != nil {
		panic(err)
	}
	object := native.Object(vm, value)
	m.createIncoming, _ = callable(native.Get(vm, object, "createIncomingMessage"))
	m.createResponse, _ = callable(native.Get(vm, object, "createServerResponse"))
	if !engine.IsCallable(m.createIncoming) || !engine.IsCallable(m.createResponse) {
		panic(native.GoError(vm, fmt.Errorf("http streams factory is incomplete")))
	}
}
func callable(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
func asObject(v moejs.Value) (*engine.Object, bool) {
	if !v.IsObject() {
		return nil, false
	}
	return v.AsObject(), true
}
func objectKeys(o *engine.Object) []string {
	keys := o.OwnEnumerableStringKeys()
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.GoString()
	}
	return out
}

//go:embed client.js
var httpClientSource string

//go:embed streams.js
var httpStreamsSource string
