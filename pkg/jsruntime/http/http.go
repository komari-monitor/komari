package http

import (
	"bytes"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/httpbody"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
)

type nodeHTTPResponse struct {
	mu         sync.Mutex
	headers    http.Header
	body       bytes.Buffer
	statusCode int
	statusText string
	ended      bool
	done       chan struct{}
}

type Module struct {
	runtime      *bridge.Runtime
	allowListen  bool
	maxBodyBytes int64

	streamMu       sync.Mutex
	createIncoming goja.Callable
	createResponse goja.Callable
}

func New(runtime *bridge.Runtime, allowListen bool, maxBodyBytes int64) *Module {
	return &Module{runtime: runtime, allowListen: allowListen, maxBodyBytes: maxBodyBytes}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	_ = exports.Set("createServer", func(call goja.FunctionCall) goja.Value {
		server := m.newHTTPServer(vm)
		listener := call.Argument(0)
		if _, ok := goja.AssertFunction(listener); !ok {
			listener = call.Argument(1)
		}
		if _, ok := goja.AssertFunction(listener); ok {
			on, _ := goja.AssertFunction(server.Get("on"))
			_, _ = on(server, vm.ToValue("request"), listener)
		}
		return server
	})
	_ = exports.Set("METHODS", []string{"ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD", "LINK", "LOCK", "M-SEARCH", "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL", "MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST", "PROPFIND", "PROPPATCH", "PURGE", "PUT", "QUERY", "REBIND", "REPORT", "SEARCH", "SOURCE", "SUBSCRIBE", "TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE"})
	statusCodes := make(map[int]string)
	for code := 100; code <= 599; code++ {
		if text := http.StatusText(code); text != "" {
			statusCodes[code] = text
		}
	}
	_ = exports.Set("STATUS_CODES", statusCodes)
	_ = exports.Set("maxHeaderSize", 1<<20)
	_ = exports.Set("validateHeaderName", func(name string) {
		if !validHTTPToken(name) {
			panic(vm.NewTypeError("Invalid HTTP header name"))
		}
	})
	_ = exports.Set("validateHeaderValue", func(name, value string) {
		if strings.ContainsAny(value, "\x00\r\n") {
			panic(vm.NewTypeError("Invalid value for header %s", name))
		}
	})
	m.attachHTTPClient(vm, exports)
	_ = module.Set("exports", exports)
}

func (m *Module) newHTTPServer(vm *goja.Runtime) *goja.Object {
	serverObject := events.NewEmitter(vm)
	var server *http.Server
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	var listenGeneration uint64
	var listenPending bool
	var readTimeout time.Duration
	_ = serverObject.Set("listening", false)
	_ = serverObject.Set("timeout", 0)
	_ = serverObject.Set("keepAliveTimeout", 5000)
	_ = serverObject.Set("headersTimeout", 60000)
	_ = serverObject.Set("requestTimeout", 300000)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := httpbody.ReadAll(request.Body, m.maxBodyBytes)
		if err != nil {
			http.Error(response, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		state := &nodeHTTPResponse{headers: make(http.Header), statusCode: http.StatusOK, done: make(chan struct{})}
		queued := m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			incoming := m.httpIncomingMessage(vm, request, body)
			outgoing := m.httpServerResponse(vm, state)
			err := m.runtime.RunJob(vm, "http.Server request", func() error { return events.Emit(vm, serverObject, "request", incoming, outgoing) })
			if err != nil {
				state.finish(http.StatusInternalServerError, nil)
				return
			}
			m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "http.IncomingMessage body", func() error {
					push, _ := goja.AssertFunction(incoming.Get("push"))
					if len(body) > 0 {
						if _, pushErr := push(incoming, buffer.WrapBytes(vm, append([]byte(nil), body...))); pushErr != nil {
							return pushErr
						}
					}
					_, pushErr := push(incoming, goja.Null())
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
	_ = serverObject.Set("listen", func(call goja.FunctionCall) goja.Value {
		if !m.allowListen {
			panic(vm.NewGoError(fmt.Errorf("http.Server.listen requires AllowListen")))
		}
		address, callback := listenArguments(vm, call)
		serverMu.Lock()
		if listenPending || listener != nil {
			serverMu.Unlock()
			panic(vm.NewGoError(fmt.Errorf("server is already listening")))
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
					m.runtime.RunOnLoop(func(vm *goja.Runtime) {
						_ = m.runtime.RunJob(vm, "http.Server error", func() error {
							return events.Emit(vm, serverObject, "error", vm.NewGoError(err))
						})
					})
				}
				return
			}
			createdServer := &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: m.runtime.Timeout(),
				ReadTimeout:       configuredReadTimeout,
			}
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
			m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				serverMu.RLock()
				active := listenGeneration == generation && server == createdServer && listener == created
				serverMu.RUnlock()
				if !active {
					return
				}
				if callback != nil {
					once, _ := goja.AssertFunction(serverObject.Get("once"))
					_, _ = once(serverObject, vm.ToValue("listening"), vm.ToValue(callback))
				}
				_ = m.runtime.RunJob(vm, "http.Server listening", func() error {
					_ = serverObject.Set("listening", true)
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
				m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					_ = m.runtime.RunJob(vm, "http.Server error", func() error {
						return events.Emit(vm, serverObject, "error", vm.NewGoError(err))
					})
				})
			}
		}()
		return serverObject
	})
	_ = serverObject.Set("close", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			once, _ := goja.AssertFunction(serverObject.Get("once"))
			_, _ = once(serverObject, vm.ToValue("close"), vm.ToValue(callback))
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
		_ = serverObject.Set("listening", false)
		if wasActive {
			m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "http.Server close", func() error { return events.Emit(vm, serverObject, "close") })
			}, 0)
		}
		return serverObject
	})
	_ = serverObject.Set("closeAllConnections", func() {
		serverMu.RLock()
		current := server
		serverMu.RUnlock()
		if current != nil {
			_ = current.Close()
		}
	})
	_ = serverObject.Set("closeIdleConnections", func() {
		panic(vm.NewGoError(fmt.Errorf("http.Server.closeIdleConnections is not supported by jsruntime; net/http does not expose individual idle server connections")))
	})
	_ = serverObject.Set("address", func() goja.Value {
		serverMu.RLock()
		current := listener
		serverMu.RUnlock()
		if current == nil {
			return goja.Null()
		}
		host, port, _ := net.SplitHostPort(current.Addr().String())
		portNumber, _ := strconv.Atoi(port)
		return vm.ToValue(map[string]any{"address": host, "family": addressFamily(host), "port": portNumber})
	})
	_ = serverObject.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		serverMu.Lock()
		readTimeout = time.Duration(milliseconds) * time.Millisecond
		serverMu.Unlock()
		if function, ok := goja.AssertFunction(callback); ok {
			on, _ := goja.AssertFunction(serverObject.Get("on"))
			_, _ = on(serverObject, vm.ToValue("timeout"), vm.ToValue(function))
		}
		return serverObject
	})
	_ = serverObject.Set("ref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("http.Server.ref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = serverObject.Set("unref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("http.Server.unref is not supported by jsruntime; the event loop is host-driven")))
	})
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

func (m *Module) httpIncomingMessage(vm *goja.Runtime, request *http.Request, body []byte) *goja.Object {
	m.initHTTPStreams(vm)
	value, err := m.createIncoming(goja.Undefined())
	if err != nil {
		panic(err)
	}
	incoming := value.ToObject(vm)
	headers := make(map[string]string, len(request.Header))
	rawHeaders := make([]string, 0, len(request.Header)*2)
	for name, values := range request.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ", ")
		for _, value := range values {
			rawHeaders = append(rawHeaders, name, value)
		}
	}
	_ = incoming.Set("method", request.Method)
	_ = incoming.Set("url", request.URL.RequestURI())
	_ = incoming.Set("headers", headers)
	_ = incoming.Set("headersDistinct", request.Header.Clone())
	_ = incoming.Set("rawHeaders", rawHeaders)
	_ = incoming.Set("httpVersion", fmt.Sprintf("%d.%d", request.ProtoMajor, request.ProtoMinor))
	_ = incoming.Set("httpVersionMajor", request.ProtoMajor)
	_ = incoming.Set("httpVersionMinor", request.ProtoMinor)
	_ = incoming.Set("complete", true)
	_ = incoming.Set("aborted", false)
	_ = incoming.Set("socket", httpSocketInfo(vm, request))
	_ = incoming.Set("connection", incoming.Get("socket"))
	_ = incoming.Set("_destroy", func(err goja.Value, callback goja.Callable) {
		_ = request.Body.Close()
		_, _ = callback(goja.Undefined(), err)
	})
	return incoming
}

func httpSocketInfo(vm *goja.Runtime, request *http.Request) *goja.Object {
	socket := events.NewEmitter(vm)
	remoteHost, remotePort, _ := net.SplitHostPort(request.RemoteAddr)
	localHost, localPort := "", 0
	if local, ok := request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		localHost, localPort = splitAddress(local)
	}
	remotePortNumber, _ := strconv.Atoi(remotePort)
	_ = socket.Set("remoteAddress", remoteHost)
	_ = socket.Set("remotePort", remotePortNumber)
	_ = socket.Set("remoteFamily", addressFamily(remoteHost))
	_ = socket.Set("localAddress", localHost)
	_ = socket.Set("localPort", localPort)
	_ = socket.Set("localFamily", addressFamily(localHost))
	_ = socket.Set("encrypted", request.TLS != nil)
	return socket
}

func (m *Module) httpServerResponse(vm *goja.Runtime, state *nodeHTTPResponse) *goja.Object {
	m.initHTTPStreams(vm)
	hooks := vm.NewObject()
	_ = hooks.Set("write", func(response *goja.Object, chunk goja.Value, callback goja.Callable) {
		state.mu.Lock()
		_, _ = state.body.Write(buffer.Bytes(vm, chunk))
		state.mu.Unlock()
		_ = response.Set("headersSent", true)
		if callback != nil {
			_, _ = callback(goja.Undefined())
		}
	})
	_ = hooks.Set("finish", func(response *goja.Object, callback goja.Callable) {
		state.mu.Lock()
		state.statusCode = int(response.Get("statusCode").ToInteger())
		state.statusText = response.Get("statusMessage").String()
		if !state.ended {
			state.ended = true
			close(state.done)
		}
		state.mu.Unlock()
		_ = response.Set("headersSent", true)
		if callback != nil {
			_, _ = callback(goja.Undefined())
		}
	})
	_ = hooks.Set("destroy", func(*goja.Object) {
		state.finish(0, nil)
	})
	value, err := m.createResponse(goja.Undefined(), hooks)
	if err != nil {
		panic(err)
	}
	response := value.ToObject(vm)
	_ = response.Set("statusCode", http.StatusOK)
	_ = response.Set("statusMessage", "")
	_ = response.Set("headersSent", false)
	_ = response.Set("sendDate", true)
	_ = response.Set("setHeader", func(name string, value goja.Value) *goja.Object {
		state.mu.Lock()
		defer state.mu.Unlock()
		var values []string
		if err := vm.ExportTo(value, &values); err == nil {
			state.headers[name] = values
		} else {
			state.headers.Set(name, value.String())
		}
		return response
	})
	_ = response.Set("appendHeader", func(name, value string) *goja.Object {
		state.mu.Lock()
		state.headers.Add(name, value)
		state.mu.Unlock()
		return response
	})
	_ = response.Set("getHeader", func(name string) goja.Value {
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
	_ = response.Set("getHeaders", func() http.Header { state.mu.Lock(); defer state.mu.Unlock(); return state.headers.Clone() })
	_ = response.Set("getHeaderNames", func() []string {
		state.mu.Lock()
		defer state.mu.Unlock()
		names := make([]string, 0, len(state.headers))
		for name := range state.headers {
			names = append(names, strings.ToLower(name))
		}
		return names
	})
	_ = response.Set("hasHeader", func(name string) bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return len(state.headers.Values(name)) > 0
	})
	_ = response.Set("removeHeader", func(name string) { state.mu.Lock(); state.headers.Del(name); state.mu.Unlock() })
	_ = response.Set("writeHead", func(call goja.FunctionCall) goja.Value {
		_ = response.Set("statusCode", call.Argument(0).ToInteger())
		if text := call.Argument(1); !goja.IsUndefined(text) {
			if _, ok := text.(*goja.Object); ok {
				applyHTTPHeaders(vm, response, text)
			} else {
				_ = response.Set("statusMessage", text.String())
				applyHTTPHeaders(vm, response, call.Argument(2))
			}
		}
		_ = response.Set("headersSent", true)
		return response
	})
	_ = response.Set("flushHeaders", func() { _ = response.Set("headersSent", true) })
	_ = response.Set("writeContinue", func() {
		panic(vm.NewGoError(fmt.Errorf("http.ServerResponse.writeContinue is not supported by jsruntime; the Go server never sends 100 Continue")))
	})

	_ = response.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		if function, ok := goja.AssertFunction(callback); ok {
			m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "http.ServerResponse timeout", func() error {
					_, err := function(response)
					return err
				})
			}, time.Duration(milliseconds)*time.Millisecond)
		}
		return response
	})
	return response
}

func applyHTTPHeaders(vm *goja.Runtime, response *goja.Object, value goja.Value) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return
	}
	setHeader, _ := goja.AssertFunction(response.Get("setHeader"))
	object := value.ToObject(vm)
	for _, name := range object.Keys() {
		_, _ = setHeader(response, vm.ToValue(name), object.Get(name))
	}
}

func listenArguments(vm *goja.Runtime, call goja.FunctionCall) (string, goja.Callable) {
	host := "127.0.0.1"
	port := int(call.Argument(0).ToInteger())
	callbackIndex := 1
	if object, ok := call.Argument(0).(*goja.Object); ok {
		port = int(object.Get("port").ToInteger())
		if value := object.Get("host"); !goja.IsUndefined(value) {
			host = value.String()
		}
	} else if value := call.Argument(1); !goja.IsUndefined(value) {
		if _, ok := goja.AssertFunction(value); !ok {
			host = value.String()
			callbackIndex = 2
		}
	}
	callback, _ := goja.AssertFunction(call.Argument(callbackIndex))
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

func (m *Module) attachHTTPClient(vm *goja.Runtime, exports *goja.Object) {
	factoryValue, err := vm.RunString(httpClientSource)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	factory, _ := goja.AssertFunction(factoryValue)
	client, err := factory(goja.Undefined(), require.Require(vm, "events"), vm.Get("fetch"), vm.Get("AbortController"), vm.Get("Blob"))
	if err != nil {
		panic(err)
	}
	clientObject := client.ToObject(vm)
	for _, name := range clientObject.Keys() {
		_ = exports.Set(name, clientObject.Get(name))
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

//go:embed client.js
var httpClientSource string

//go:embed streams.js
var httpStreamsSource string

// initHTTPStreams loads the embedded factory that builds IncomingMessage
// (Readable) and ServerResponse (Writable) instances from the stream module.
func (m *Module) initHTTPStreams(vm *goja.Runtime) {
	m.streamMu.Lock()
	defer m.streamMu.Unlock()
	if m.createIncoming != nil {
		return
	}
	factoryValue, err := vm.RunString(httpStreamsSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load http streams: %w", err)))
	}
	factory, _ := goja.AssertFunction(factoryValue)
	value, err := factory(goja.Undefined(), require.Require(vm, "stream"))
	if err != nil {
		panic(err)
	}
	object := value.ToObject(vm)
	m.createIncoming, _ = goja.AssertFunction(object.Get("createIncomingMessage"))
	m.createResponse, _ = goja.AssertFunction(object.Get("createServerResponse"))
	if m.createIncoming == nil || m.createResponse == nil {
		panic(vm.NewGoError(fmt.Errorf("http streams factory is incomplete")))
	}
}
