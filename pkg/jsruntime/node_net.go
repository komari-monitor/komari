package jsruntime

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
)

func (r *Runtime) loadNetModule(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	_ = exports.Set("createServer", func(call goja.FunctionCall) goja.Value {
		server := r.newNetServer(vm)
		listener := call.Argument(0)
		if _, ok := goja.AssertFunction(listener); !ok {
			listener = call.Argument(1)
		}
		if _, ok := goja.AssertFunction(listener); ok {
			on, _ := goja.AssertFunction(server.Get("on"))
			_, _ = on(server, vm.ToValue("connection"), listener)
		}
		return server
	})
	connect := func(call goja.FunctionCall) goja.Value { return r.netConnect(vm, call) }
	_ = exports.Set("connect", connect)
	_ = exports.Set("createConnection", connect)
	_ = exports.Set("isIP", parseIPVersion)
	_ = exports.Set("isIPv4", func(value string) bool { ip := net.ParseIP(value); return ip != nil && ip.To4() != nil })
	_ = exports.Set("isIPv6", func(value string) bool { ip := net.ParseIP(value); return ip != nil && ip.To4() == nil })
	_ = exports.Set("getDefaultAutoSelectFamily", func() bool { return true })
	_ = exports.Set("setDefaultAutoSelectFamily", func(bool) {})
	_ = module.Set("exports", exports)
}

func (r *Runtime) newNetServer(vm *goja.Runtime) *goja.Object {
	server := newEventEmitter(vm)
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	var connections atomic.Int64
	var closed atomic.Bool
	_ = server.Set("listening", false)
	_ = server.Set("maxConnections", 0)
	_ = server.Set("listen", func(call goja.FunctionCall) goja.Value {
		if !r.allowListen {
			panic(vm.NewGoError(fmt.Errorf("server.listen requires AllowListen")))
		}
		address, callback := netListenArguments(vm, call)
		if callback != nil {
			once, _ := goja.AssertFunction(server.Get("once"))
			_, _ = once(server, vm.ToValue("listening"), vm.ToValue(callback))
		}
		go func() {
			created, err := net.Listen("tcp", address)
			if err != nil {
				r.loop.RunOnLoop(func(vm *goja.Runtime) { _ = emitEvent(vm, server, "error", vm.NewGoError(err)) })
				return
			}
			serverMu.Lock()
			listener = created
			resourceID = r.addNodeResource(func() { _ = created.Close() })
			serverMu.Unlock()
			if resourceID == 0 {
				return
			}
			r.loop.RunOnLoop(func(vm *goja.Runtime) {
				_ = server.Set("listening", true)
				_ = emitEvent(vm, server, "listening")
			})
			for {
				connection, acceptErr := created.Accept()
				if acceptErr != nil {
					if !closed.Load() {
						r.loop.RunOnLoop(func(vm *goja.Runtime) { _ = emitEvent(vm, server, "error", vm.NewGoError(acceptErr)) })
					}
					return
				}
				connections.Add(1)
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					socket := r.newNetSocket(vm, connection, func() { connections.Add(-1) })
					r.runAsyncJob(vm, "net.Server connection", func() error { return emitEvent(vm, server, "connection", socket) })
				})
			}
		}()
		return server
	})
	_ = server.Set("close", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			once, _ := goja.AssertFunction(server.Get("once"))
			_, _ = once(server, vm.ToValue("close"), vm.ToValue(callback))
		}
		closed.Store(true)
		serverMu.RLock()
		currentListener, currentResourceID := listener, resourceID
		serverMu.RUnlock()
		if currentListener != nil {
			_ = currentListener.Close()
			r.removeNodeResource(currentResourceID)
		}
		_ = server.Set("listening", false)
		r.loop.SetTimeout(func(vm *goja.Runtime) { _ = emitEvent(vm, server, "close") }, 0)
		return server
	})
	_ = server.Set("closeAllConnections", func() {})
	_ = server.Set("closeIdleConnections", func() {})
	_ = server.Set("address", func() goja.Value {
		serverMu.RLock()
		currentListener := listener
		serverMu.RUnlock()
		if currentListener == nil {
			return goja.Null()
		}
		host, port, _ := net.SplitHostPort(currentListener.Addr().String())
		portNumber, _ := strconv.Atoi(port)
		return vm.ToValue(map[string]any{"address": host, "family": netAddressFamily(host), "port": portNumber})
	})
	_ = server.Set("getConnections", func(callback goja.Callable) {
		r.loop.SetTimeout(func(vm *goja.Runtime) { _, _ = callback(goja.Undefined(), goja.Null(), vm.ToValue(connections.Load())) }, 0)
	})
	_ = server.Set("ref", func() *goja.Object { return server })
	_ = server.Set("unref", func() *goja.Object { return server })
	return server
}

func netListenArguments(vm *goja.Runtime, call goja.FunctionCall) (string, goja.Callable) {
	host := "127.0.0.1"
	port := int(call.Argument(0).ToInteger())
	callbackIndex := 1
	if object, ok := call.Argument(0).(*goja.Object); ok {
		port = int(object.Get("port").ToInteger())
		if value := object.Get("host"); !goja.IsUndefined(value) {
			host = value.String()
		}
		callbackIndex = 1
	} else if value := call.Argument(1); !goja.IsUndefined(value) {
		if _, ok := goja.AssertFunction(value); !ok {
			host = value.String()
			callbackIndex = 2
		}
	}
	callback, _ := goja.AssertFunction(call.Argument(callbackIndex))
	return net.JoinHostPort(host, strconv.Itoa(port)), callback
}

func (r *Runtime) netConnect(vm *goja.Runtime, call goja.FunctionCall) *goja.Object {
	host := "127.0.0.1"
	port := int(call.Argument(0).ToInteger())
	callbackIndex := 1
	if object, ok := call.Argument(0).(*goja.Object); ok {
		port = int(object.Get("port").ToInteger())
		if value := object.Get("host"); !goja.IsUndefined(value) {
			host = value.String()
		}
	} else if _, ok := goja.AssertFunction(call.Argument(1)); !ok && !goja.IsUndefined(call.Argument(1)) {
		host = call.Argument(1).String()
		callbackIndex = 2
	}
	placeholder := newEventEmitter(vm)
	_ = placeholder.Set("connecting", true)
	var pendingEncoding string
	_ = placeholder.Set("setEncoding", func(value string) *goja.Object { pendingEncoding = value; return placeholder })
	_ = placeholder.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		if function, ok := goja.AssertFunction(callback); ok {
			once, _ := goja.AssertFunction(placeholder.Get("once"))
			_, _ = once(placeholder, vm.ToValue("timeout"), vm.ToValue(function))
		}
		if milliseconds > 0 {
			r.loop.SetTimeout(func(vm *goja.Runtime) { _ = emitEvent(vm, placeholder, "timeout") }, time.Duration(milliseconds)*time.Millisecond)
		}
		return placeholder
	})
	_ = placeholder.Set("setNoDelay", func() *goja.Object { return placeholder })
	_ = placeholder.Set("setKeepAlive", func() *goja.Object { return placeholder })
	_ = placeholder.Set("ref", func() *goja.Object { return placeholder })
	_ = placeholder.Set("unref", func() *goja.Object { return placeholder })
	if callback, ok := goja.AssertFunction(call.Argument(callbackIndex)); ok {
		once, _ := goja.AssertFunction(placeholder.Get("once"))
		_, _ = once(placeholder, vm.ToValue("connect"), vm.ToValue(callback))
	}
	go func() {
		connection, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), r.timeout)
		queued := r.loop.RunOnLoop(func(vm *goja.Runtime) {
			if err != nil {
				_ = placeholder.Set("connecting", false)
				_ = emitEvent(vm, placeholder, "error", vm.NewGoError(err))
				return
			}
			if !r.configureNetSocket(vm, placeholder, connection, nil) {
				return
			}
			if pendingEncoding != "" {
				if setEncoding, ok := goja.AssertFunction(placeholder.Get("setEncoding")); ok {
					_, _ = setEncoding(placeholder, vm.ToValue(pendingEncoding))
				}
			}
			_ = placeholder.Set("connecting", false)
			_ = emitEvent(vm, placeholder, "connect")
		})
		if !queued && connection != nil {
			_ = connection.Close()
		}
	}()
	return placeholder
}

func (r *Runtime) newNetSocket(vm *goja.Runtime, connection net.Conn, onClose func()) *goja.Object {
	socket := newEventEmitter(vm)
	if !r.configureNetSocket(vm, socket, connection, onClose) {
		_ = socket.Set("destroyed", true)
		_ = socket.Set("readyState", "closed")
	}
	return socket
}

func (r *Runtime) configureNetSocket(vm *goja.Runtime, socket *goja.Object, connection net.Conn, onClose func()) bool {
	localHost, localPort := splitNetAddress(connection.LocalAddr())
	remoteHost, remotePort := splitNetAddress(connection.RemoteAddr())
	_ = socket.Set("connecting", false)
	_ = socket.Set("pending", false)
	_ = socket.Set("destroyed", false)
	_ = socket.Set("readyState", "open")
	_ = socket.Set("remoteAddress", remoteHost)
	_ = socket.Set("remotePort", remotePort)
	_ = socket.Set("remoteFamily", netAddressFamily(remoteHost))
	_ = socket.Set("localAddress", localHost)
	_ = socket.Set("localPort", localPort)
	_ = socket.Set("localFamily", netAddressFamily(localHost))
	resourceID := r.addNodeResource(func() { _ = connection.Close() })
	if resourceID == 0 {
		if onClose != nil {
			onClose()
		}
		return false
	}
	var encoding string
	var closed atomic.Bool
	closeSocket := func() {
		if closed.CompareAndSwap(false, true) {
			r.removeNodeResource(resourceID)
			_ = connection.Close()
			if onClose != nil {
				onClose()
			}
		}
	}
	_ = socket.Set("write", func(call goja.FunctionCall) goja.Value {
		data := append([]byte(nil), buffer.Bytes(vm, call.Argument(0))...)
		callback, _ := goja.AssertFunction(call.Argument(2))
		go func() {
			_, err := connection.Write(data)
			if callback != nil {
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					if err != nil {
						_, _ = callback(goja.Undefined(), vm.NewGoError(err))
					} else {
						_, _ = callback(goja.Undefined())
					}
				})
			}
		}()
		return vm.ToValue(true)
	})
	_ = socket.Set("end", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			_, _ = connection.Write(buffer.Bytes(vm, call.Argument(0)))
		}
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			closeSocket()
		}
		return socket
	})
	_ = socket.Set("destroy", func() *goja.Object { closeSocket(); return socket })
	_ = socket.Set("setEncoding", func(value string) *goja.Object { encoding = value; return socket })
	_ = socket.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		if milliseconds <= 0 {
			_ = connection.SetDeadline(time.Time{})
		} else {
			_ = connection.SetDeadline(time.Now().Add(time.Duration(milliseconds) * time.Millisecond))
		}
		if function, ok := goja.AssertFunction(callback); ok {
			once, _ := goja.AssertFunction(socket.Get("once"))
			_, _ = once(socket, vm.ToValue("timeout"), vm.ToValue(function))
		}
		return socket
	})
	_ = socket.Set("setNoDelay", func(enabled bool) *goja.Object {
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(enabled)
		}
		return socket
	})
	_ = socket.Set("setKeepAlive", func(enabled bool) *goja.Object {
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(enabled)
		}
		return socket
	})
	_ = socket.Set("address", func() map[string]any {
		return map[string]any{"address": localHost, "family": netAddressFamily(localHost), "port": localPort}
	})
	_ = socket.Set("pause", func() *goja.Object { return socket })
	_ = socket.Set("resume", func() *goja.Object { return socket })
	_ = socket.Set("ref", func() *goja.Object { return socket })
	_ = socket.Set("unref", func() *goja.Object { return socket })
	go func() {
		data := make([]byte, 32*1024)
		for {
			count, err := connection.Read(data)
			if count > 0 {
				chunk := append([]byte(nil), data[:count]...)
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					var value any = buffer.WrapBytes(vm, chunk)
					if encoding != "" {
						value = buffer.EncodeBytes(vm, chunk, vm.ToValue(encoding))
					}
					_ = emitEvent(vm, socket, "data", value)
				})
			}
			if err != nil {
				closeSocket()
				r.loop.RunOnLoop(func(vm *goja.Runtime) {
					_ = socket.Set("destroyed", true)
					_ = socket.Set("readyState", "closed")
					if !errors.Is(err, io.EOF) && !isNetClosed(err) {
						_ = emitEvent(vm, socket, "error", vm.NewGoError(err))
					}
					_ = emitEvent(vm, socket, "end")
					_ = emitEvent(vm, socket, "close", false)
				})
				return
			}
		}
	}()
	return true
}

func splitNetAddress(address net.Addr) (string, int) {
	if address == nil {
		return "", 0
	}
	host, port, _ := net.SplitHostPort(address.String())
	portNumber, _ := strconv.Atoi(port)
	return host, portNumber
}

func netAddressFamily(host string) string {
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return "IPv6"
	}
	return "IPv4"
}

func isNetClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "closed network connection")
}

func parseIPVersion(value string) int {
	ip := net.ParseIP(value)
	if ip == nil {
		return 0
	}
	if ip.To4() != nil {
		return 4
	}
	return 6
}
