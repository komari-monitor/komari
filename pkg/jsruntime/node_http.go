package jsruntime

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
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

func (r *Runtime) loadHTTPModule(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	_ = exports.Set("createServer", func(call goja.FunctionCall) goja.Value {
		server := r.newHTTPServer(vm)
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
	r.attachHTTPClient(vm, exports)
	_ = module.Set("exports", exports)
}

func (r *Runtime) newHTTPServer(vm *goja.Runtime) *goja.Object {
	serverObject := newEventEmitter(vm)
	server := &http.Server{ReadHeaderTimeout: r.timeout}
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	_ = serverObject.Set("listening", false)
	_ = serverObject.Set("timeout", 0)
	_ = serverObject.Set("keepAliveTimeout", 5000)
	_ = serverObject.Set("headersTimeout", 60000)
	_ = serverObject.Set("requestTimeout", 300000)
	server.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		state := &nodeHTTPResponse{headers: make(http.Header), statusCode: http.StatusOK, done: make(chan struct{})}
		queued := r.loop.RunOnLoop(func(vm *goja.Runtime) {
			incoming := r.httpIncomingMessage(vm, request, body)
			outgoing := r.httpServerResponse(vm, state)
			err := r.runAsyncJob(vm, "http.Server request", func() error { return emitEvent(vm, serverObject, "request", incoming, outgoing) })
			if err != nil {
				state.finish(http.StatusInternalServerError, nil)
				return
			}
			r.loop.SetTimeout(func(vm *goja.Runtime) {
				if len(body) > 0 {
					_ = emitEvent(vm, incoming, "data", buffer.WrapBytes(vm, append([]byte(nil), body...)))
				}
				_ = emitEvent(vm, incoming, "end")
				_ = emitEvent(vm, incoming, "close")
			}, 0)
		})
		if !queued {
			http.Error(response, "JavaScript runtime is closed", http.StatusServiceUnavailable)
			return
		}
		timer := time.NewTimer(r.timeout)
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
		if !r.allowListen {
			panic(vm.NewGoError(fmt.Errorf("http.Server.listen requires AllowListen")))
		}
		address, callback := netListenArguments(vm, call)
		if callback != nil {
			once, _ := goja.AssertFunction(serverObject.Get("once"))
			_, _ = once(serverObject, vm.ToValue("listening"), vm.ToValue(callback))
		}
		go func() {
			created, err := net.Listen("tcp", address)
			if err != nil {
				r.loop.RunOnLoop(func(vm *goja.Runtime) { _ = emitEvent(vm, serverObject, "error", vm.NewGoError(err)) })
				return
			}
			serverMu.Lock()
			listener = created
			resourceID = r.addNodeResource(func() { _ = server.Close() })
			serverMu.Unlock()
			if resourceID == 0 {
				return
			}
			r.loop.RunOnLoop(func(vm *goja.Runtime) {
				_ = serverObject.Set("listening", true)
				_ = emitEvent(vm, serverObject, "listening")
			})
			err = server.Serve(created)
			if err != nil && err != http.ErrServerClosed {
				r.loop.RunOnLoop(func(vm *goja.Runtime) { _ = emitEvent(vm, serverObject, "error", vm.NewGoError(err)) })
			}
		}()
		return serverObject
	})
	_ = serverObject.Set("close", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			once, _ := goja.AssertFunction(serverObject.Get("once"))
			_, _ = once(serverObject, vm.ToValue("close"), vm.ToValue(callback))
		}
		serverMu.RLock()
		currentResourceID := resourceID
		serverMu.RUnlock()
		_ = server.Close()
		r.removeNodeResource(currentResourceID)
		_ = serverObject.Set("listening", false)
		r.loop.SetTimeout(func(vm *goja.Runtime) { _ = emitEvent(vm, serverObject, "close") }, 0)
		return serverObject
	})
	_ = serverObject.Set("closeAllConnections", server.Close)
	_ = serverObject.Set("closeIdleConnections", server.Close)
	_ = serverObject.Set("address", func() goja.Value {
		serverMu.RLock()
		current := listener
		serverMu.RUnlock()
		if current == nil {
			return goja.Null()
		}
		host, port, _ := net.SplitHostPort(current.Addr().String())
		portNumber, _ := strconv.Atoi(port)
		return vm.ToValue(map[string]any{"address": host, "family": netAddressFamily(host), "port": portNumber})
	})
	_ = serverObject.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		server.ReadTimeout = time.Duration(milliseconds) * time.Millisecond
		if function, ok := goja.AssertFunction(callback); ok {
			on, _ := goja.AssertFunction(serverObject.Get("on"))
			_, _ = on(serverObject, vm.ToValue("timeout"), vm.ToValue(function))
		}
		return serverObject
	})
	_ = serverObject.Set("ref", func() *goja.Object { return serverObject })
	_ = serverObject.Set("unref", func() *goja.Object { return serverObject })
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

func (r *Runtime) httpIncomingMessage(vm *goja.Runtime, request *http.Request, body []byte) *goja.Object {
	incoming := newEventEmitter(vm)
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
	_ = incoming.Set("readable", len(body) > 0)
	_ = incoming.Set("socket", httpSocketInfo(vm, request))
	_ = incoming.Set("connection", incoming.Get("socket"))
	_ = incoming.Set("setEncoding", func() *goja.Object { return incoming })
	_ = incoming.Set("pause", func() *goja.Object { return incoming })
	_ = incoming.Set("resume", func() *goja.Object { return incoming })
	_ = incoming.Set("destroy", func() { _ = request.Body.Close() })
	return incoming
}

func httpSocketInfo(vm *goja.Runtime, request *http.Request) *goja.Object {
	socket := newEventEmitter(vm)
	remoteHost, remotePort, _ := net.SplitHostPort(request.RemoteAddr)
	localHost, localPort := "", 0
	if local, ok := request.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		localHost, localPort = splitNetAddress(local)
	}
	remotePortNumber, _ := strconv.Atoi(remotePort)
	_ = socket.Set("remoteAddress", remoteHost)
	_ = socket.Set("remotePort", remotePortNumber)
	_ = socket.Set("remoteFamily", netAddressFamily(remoteHost))
	_ = socket.Set("localAddress", localHost)
	_ = socket.Set("localPort", localPort)
	_ = socket.Set("localFamily", netAddressFamily(localHost))
	_ = socket.Set("encrypted", request.TLS != nil)
	return socket
}

func (r *Runtime) httpServerResponse(vm *goja.Runtime, state *nodeHTTPResponse) *goja.Object {
	response := newEventEmitter(vm)
	_ = response.Set("statusCode", http.StatusOK)
	_ = response.Set("statusMessage", "")
	_ = response.Set("headersSent", false)
	_ = response.Set("writableEnded", false)
	_ = response.Set("writableFinished", false)
	_ = response.Set("destroyed", false)
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
	_ = response.Set("writeContinue", func() {})
	_ = response.Set("write", func(call goja.FunctionCall) goja.Value {
		state.mu.Lock()
		_, _ = state.body.Write(buffer.Bytes(vm, call.Argument(0)))
		state.mu.Unlock()
		_ = response.Set("headersSent", true)
		if callback, ok := goja.AssertFunction(call.Argument(2)); ok {
			_, _ = callback(goja.Undefined())
		}
		return vm.ToValue(true)
	})
	_ = response.Set("end", func(call goja.FunctionCall) goja.Value {
		state.mu.Lock()
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			_, _ = state.body.Write(buffer.Bytes(vm, call.Argument(0)))
		}
		state.statusCode = int(response.Get("statusCode").ToInteger())
		state.statusText = response.Get("statusMessage").String()
		if !state.ended {
			state.ended = true
			close(state.done)
		}
		state.mu.Unlock()
		_ = response.Set("headersSent", true)
		_ = response.Set("writableEnded", true)
		_ = response.Set("writableFinished", true)
		if callback, ok := goja.AssertFunction(call.Argument(2)); ok {
			_, _ = callback(goja.Undefined())
		}
		_ = emitEvent(vm, response, "finish")
		return response
	})
	_ = response.Set("destroy", func() {
		_ = response.Set("destroyed", true)
		state.finish(0, nil)
		_ = emitEvent(vm, response, "close")
	})
	_ = response.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		if function, ok := goja.AssertFunction(callback); ok {
			r.loop.SetTimeout(func(vm *goja.Runtime) { _, _ = function(response) }, time.Duration(milliseconds)*time.Millisecond)
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

func (r *Runtime) attachHTTPClient(vm *goja.Runtime, exports *goja.Object) {
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

const httpClientSource = `
(function (EventEmitter, fetch, AbortController, Blob) {
	"use strict";
	class Agent { constructor(options) { this.options = options || {}; this.keepAlive = Boolean(this.options.keepAlive); this.maxSockets = this.options.maxSockets || Infinity; } destroy() {} }
	class ClientRequest extends EventEmitter {
		constructor(input, options, callback) {
			super(); this._chunks = []; this._controller = new AbortController(); this.destroyed = false; this.finished = false;
			if (typeof input === "object" && input !== null) { options = input; input = undefined; }
			options = options || {}; this.method = String(options.method || "GET").toUpperCase(); this._headers = new Headers(options.headers);
			this.url = input === undefined ? String(options.protocol || "http:") + "//" + String(options.hostname || options.host || "localhost") + (options.port ? ":" + options.port : "") + String(options.path || "/") : String(input);
			if (typeof callback === "function") this.once("response", callback);
		}
		setHeader(name, value) { this._headers.set(name, value); }
		getHeader(name) { return this._headers.get(name); }
		getHeaders() { return Object.fromEntries(this._headers); }
		getHeaderNames() { return Array.from(this._headers.keys()); }
		hasHeader(name) { return this._headers.has(name); }
		removeHeader(name) { this._headers.delete(name); }
		flushHeaders() { return this; }
		write(chunk, encoding, callback) { this._chunks.push(chunk); if (typeof encoding === "function") callback = encoding; if (callback) callback(); return true; }
		end(chunk, encoding, callback) {
			if (chunk !== undefined && chunk !== null && typeof chunk !== "function") this._chunks.push(chunk);
			if (typeof chunk === "function") callback = chunk; else if (typeof encoding === "function") callback = encoding;
			this.finished = true;
			const body = this._chunks.length ? new Blob(this._chunks) : undefined;
			fetch(this.url, { method: this.method, headers: this._headers, body, signal: this._controller.signal }).then(async (response) => {
				const incoming = new EventEmitter(); incoming.statusCode = response.status; incoming.statusMessage = response.statusText;
				incoming.headers = Object.fromEntries(response.headers); incoming.rawHeaders = Array.from(response.headers).flat(); incoming.httpVersion = "1.1";
				incoming.complete = true; incoming.aborted = false; incoming.setEncoding = () => incoming; incoming.pause = () => incoming; incoming.resume = () => incoming;
				this.emit("response", incoming); const bytes = new Uint8Array(await response.arrayBuffer());
				setTimeout(() => { if (bytes.length) incoming.emit("data", bytes); incoming.emit("end"); incoming.emit("close"); this.emit("close"); }, 0);
			}, (error) => { this.emit("error", error); this.emit("close"); });
			if (callback) callback(); return this;
		}
		abort() { this.destroyed = true; this._controller.abort(); this.emit("abort"); }
		destroy(error) { this.destroyed = true; this._controller.abort(error); if (error) this.emit("error", error); this.emit("close"); return this; }
		setTimeout(ms, callback) { if (callback) this.once("timeout", callback); setTimeout(() => this.emit("timeout"), ms); return this; }
		setNoDelay() { return this; } setSocketKeepAlive() { return this; }
	}
	function request(input, options, callback) { if (typeof options === "function") { callback = options; options = {}; } return new ClientRequest(input, options, callback); }
	function get(input, options, callback) { const req = request(input, options, callback); req.end(); return req; }
	return { request, get, Agent, ClientRequest, globalAgent: new Agent(), IncomingMessage: EventEmitter, ServerResponse: EventEmitter };
})
`
