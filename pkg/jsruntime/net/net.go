package net

import (
	"context"
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
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/writequeue"
)

type Module struct {
	runtime     *bridge.Runtime
	allowListen bool
}

func New(runtime *bridge.Runtime, allowListen bool) *Module {
	return &Module{runtime: runtime, allowListen: allowListen}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	_ = exports.Set("createServer", func(call goja.FunctionCall) goja.Value {
		server := m.newNetServer(vm)
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
	connect := func(call goja.FunctionCall) goja.Value { return m.netConnect(vm, call) }
	_ = exports.Set("connect", connect)
	_ = exports.Set("createConnection", connect)
	_ = exports.Set("isIP", parseIPVersion)
	_ = exports.Set("isIPv4", func(value string) bool { ip := net.ParseIP(value); return ip != nil && ip.To4() != nil })
	_ = exports.Set("isIPv6", func(value string) bool { ip := net.ParseIP(value); return ip != nil && ip.To4() == nil })
	_ = exports.Set("getDefaultAutoSelectFamily", func() bool { return true })
	_ = exports.Set("setDefaultAutoSelectFamily", func(bool) {
		panic(vm.NewGoError(fmt.Errorf("net.setDefaultAutoSelectFamily is not supported by jsruntime; autoSelectFamily is always enabled")))
	})
	_ = module.Set("exports", exports)
}

func (m *Module) newNetServer(vm *goja.Runtime) *goja.Object {
	server := events.NewEmitter(vm)
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	var listenGeneration uint64
	var listenPending bool
	var connections atomic.Int64
	_ = server.Set("listening", false)
	_ = server.Set("maxConnections", 0)
	_ = server.Set("listen", func(call goja.FunctionCall) goja.Value {
		if !m.allowListen {
			panic(vm.NewGoError(fmt.Errorf("server.listen requires AllowListen")))
		}
		address, callback := netListenArguments(vm, call)
		serverMu.Lock()
		if listenPending || listener != nil {
			serverMu.Unlock()
			panic(vm.NewGoError(fmt.Errorf("server is already listening")))
		}
		listenGeneration++
		generation := listenGeneration
		listenPending = true
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
						_ = m.runtime.RunJob(vm, "net.Server error", func() error {
							return events.Emit(vm, server, "error", vm.NewGoError(err))
						})
					})
				}
				return
			}
			serverMu.Lock()
			if !listenPending || listenGeneration != generation {
				serverMu.Unlock()
				_ = created.Close()
				return
			}
			id := m.runtime.AddResource(func() { _ = created.Close() })
			if id == 0 {
				listenPending = false
				serverMu.Unlock()
				return
			}
			listener = created
			resourceID = id
			listenPending = false
			serverMu.Unlock()
			m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				serverMu.RLock()
				active := listenGeneration == generation && listener == created
				serverMu.RUnlock()
				if !active {
					return
				}
				if callback != nil {
					once, _ := goja.AssertFunction(server.Get("once"))
					_, _ = once(server, vm.ToValue("listening"), vm.ToValue(callback))
				}
				_ = m.runtime.RunJob(vm, "net.Server listening", func() error {
					_ = server.Set("listening", true)
					return events.Emit(vm, server, "listening")
				})
			})
			for {
				connection, acceptErr := created.Accept()
				if acceptErr != nil {
					serverMu.RLock()
					active := listenGeneration == generation && listener == created
					serverMu.RUnlock()
					if active {
						m.runtime.RunOnLoop(func(vm *goja.Runtime) {
							_ = m.runtime.RunJob(vm, "net.Server error", func() error {
								return events.Emit(vm, server, "error", vm.NewGoError(acceptErr))
							})
						})
					}
					return
				}
				connections.Add(1)
				if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					socket := m.newNetSocket(vm, connection, func() { connections.Add(-1) })
					if socket.Get("destroyed").ToBoolean() {
						return
					}
					_ = m.runtime.RunJob(vm, "net.Server connection", func() error { return events.Emit(vm, server, "connection", socket) })
				}) {
					connections.Add(-1)
					_ = connection.Close()
					return
				}
			}
		}()
		return server
	})
	_ = server.Set("close", func(call goja.FunctionCall) goja.Value {
		if callback, ok := goja.AssertFunction(call.Argument(0)); ok {
			once, _ := goja.AssertFunction(server.Get("once"))
			_, _ = once(server, vm.ToValue("close"), vm.ToValue(callback))
		}
		serverMu.Lock()
		wasActive := listenPending || listener != nil
		listenGeneration++
		listenPending = false
		currentListener, currentResourceID := listener, resourceID
		listener, resourceID = nil, 0
		serverMu.Unlock()
		if currentListener != nil {
			_ = currentListener.Close()
			m.runtime.RemoveResource(currentResourceID)
		}
		_ = server.Set("listening", false)
		if wasActive {
			m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "net.Server close", func() error { return events.Emit(vm, server, "close") })
			}, 0)
		}
		return server
	})
	_ = server.Set("closeAllConnections", func() {
		panic(vm.NewGoError(fmt.Errorf("net.Server.closeAllConnections is not supported by jsruntime; connections are not tracked individually")))
	})
	_ = server.Set("closeIdleConnections", func() {
		panic(vm.NewGoError(fmt.Errorf("net.Server.closeIdleConnections is not supported by jsruntime; connections are not tracked individually")))
	})
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
		m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
			_ = m.runtime.RunJob(vm, "net.Server.getConnections", func() error {
				_, err := callback(goja.Undefined(), goja.Null(), vm.ToValue(connections.Load()))
				return err
			})
		}, 0)
	})
	_ = server.Set("ref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Server.ref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = server.Set("unref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Server.unref is not supported by jsruntime; the event loop is host-driven")))
	})
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

func (m *Module) netConnect(vm *goja.Runtime, call goja.FunctionCall) *goja.Object {
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
	placeholder := events.NewEmitter(vm)
	_ = placeholder.Set("connecting", true)
	var pendingEncoding string
	_ = placeholder.Set("setEncoding", func(value string) *goja.Object { pendingEncoding = value; return placeholder })
	_ = placeholder.Set("setTimeout", func(milliseconds int64, callback goja.Value) *goja.Object {
		if function, ok := goja.AssertFunction(callback); ok {
			once, _ := goja.AssertFunction(placeholder.Get("once"))
			_, _ = once(placeholder, vm.ToValue("timeout"), vm.ToValue(function))
		}
		if milliseconds > 0 {
			m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "net.Socket timeout", func() error { return events.Emit(vm, placeholder, "timeout") })
			}, time.Duration(milliseconds)*time.Millisecond)
		}
		return placeholder
	})
	_ = placeholder.Set("setNoDelay", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.setNoDelay is not supported by jsruntime before the connection is established")))
	})
	_ = placeholder.Set("setKeepAlive", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.setKeepAlive is not supported by jsruntime before the connection is established")))
	})
	_ = placeholder.Set("ref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.ref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = placeholder.Set("unref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.unref is not supported by jsruntime; the event loop is host-driven")))
	})
	if callback, ok := goja.AssertFunction(call.Argument(callbackIndex)); ok {
		once, _ := goja.AssertFunction(placeholder.Get("once"))
		_, _ = once(placeholder, vm.ToValue("connect"), vm.ToValue(callback))
	}
	dialContext, cancelDial := context.WithCancel(context.Background())
	pendingResourceID := m.runtime.AddResource(cancelDial)
	if pendingResourceID == 0 {
		_ = placeholder.Set("connecting", false)
		_ = placeholder.Set("destroyed", true)
		return placeholder
	}
	go func() {
		connection, err := (&net.Dialer{Timeout: m.runtime.Timeout()}).DialContext(dialContext, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		cancelDial()
		m.runtime.RemoveResource(pendingResourceID)
		queued := m.runtime.RunOnLoop(func(vm *goja.Runtime) {
			attached := false
			defer func() {
				if !attached && connection != nil {
					_ = connection.Close()
				}
			}()
			_ = m.runtime.RunJob(vm, "net.Socket connect", func() error {
				if err != nil {
					_ = placeholder.Set("connecting", false)
					return events.Emit(vm, placeholder, "error", vm.NewGoError(err))
				}
				if !m.configureNetSocket(vm, placeholder, connection, nil) {
					return nil
				}
				attached = true
				if pendingEncoding != "" {
					if setEncoding, ok := goja.AssertFunction(placeholder.Get("setEncoding")); ok {
						_, _ = setEncoding(placeholder, vm.ToValue(pendingEncoding))
					}
				}
				_ = placeholder.Set("connecting", false)
				return events.Emit(vm, placeholder, "connect")
			})
		})
		if !queued && connection != nil {
			_ = connection.Close()
		}
	}()
	return placeholder
}

func (m *Module) newNetSocket(vm *goja.Runtime, connection net.Conn, onClose func()) *goja.Object {
	socket := events.NewEmitter(vm)
	if !m.configureNetSocket(vm, socket, connection, onClose) {
		_ = socket.Set("destroyed", true)
		_ = socket.Set("readyState", "closed")
	}
	return socket
}

func (m *Module) configureNetSocket(vm *goja.Runtime, socket *goja.Object, connection net.Conn, onClose func()) bool {
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
	resourceID := m.runtime.AddResource(func() { _ = connection.Close() })
	if resourceID == 0 {
		_ = connection.Close()
		if onClose != nil {
			onClose()
		}
		return false
	}
	var encoding atomic.Value
	encoding.Store("")
	var closed atomic.Bool
	var writes writequeue.Queue
	closeSocket := func() {
		if closed.CompareAndSwap(false, true) {
			m.runtime.RemoveResource(resourceID)
			_ = connection.Close()
			if onClose != nil {
				onClose()
			}
		}
	}
	_ = socket.Set("write", func(call goja.FunctionCall) goja.Value {
		data := append([]byte(nil), buffer.Bytes(vm, call.Argument(0))...)
		callback := netCallback(call)
		writes.Submit(func() {
			_, err := connection.Write(data)
			if callback != nil {
				m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket.write", func() error {
						if err != nil {
							_, callbackErr := callback(goja.Undefined(), vm.NewGoError(err))
							return callbackErr
						}
						_, callbackErr := callback(goja.Undefined())
						return callbackErr
					})
				})
			}
		})
		return vm.ToValue(true)
	})
	_ = socket.Set("end", func(call goja.FunctionCall) goja.Value {
		var data []byte
		_, firstArgumentIsCallback := goja.AssertFunction(call.Argument(0))
		if len(call.Arguments) > 0 && !firstArgumentIsCallback && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
			data = append([]byte(nil), buffer.Bytes(vm, call.Argument(0))...)
		}
		callback := netCallback(call)
		writes.Submit(func() {
			var err error
			if len(data) > 0 {
				_, err = connection.Write(data)
			}
			if err == nil {
				if tcp, ok := connection.(*net.TCPConn); ok {
					err = tcp.CloseWrite()
				} else {
					closeSocket()
				}
			}
			if err != nil {
				closeSocket()
			}
			if callback != nil {
				m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket.end", func() error {
						if err != nil {
							_, callbackErr := callback(goja.Undefined(), vm.NewGoError(err))
							return callbackErr
						}
						_, callbackErr := callback(goja.Undefined())
						return callbackErr
					})
				})
			}
		})
		return socket
	})
	_ = socket.Set("destroy", func() *goja.Object { closeSocket(); return socket })
	_ = socket.Set("setEncoding", func(value string) *goja.Object { encoding.Store(value); return socket })
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
	_ = socket.Set("pause", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.pause is not supported by jsruntime; no backpressure")))
	})
	_ = socket.Set("resume", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.resume is not supported by jsruntime; no backpressure")))
	})
	_ = socket.Set("ref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.ref is not supported by jsruntime; the event loop is host-driven")))
	})
	_ = socket.Set("unref", func() *goja.Object {
		panic(vm.NewGoError(fmt.Errorf("net.Socket.unref is not supported by jsruntime; the event loop is host-driven")))
	})
	go func() {
		data := make([]byte, 32*1024)
		for {
			count, err := connection.Read(data)
			if count > 0 {
				chunk := append([]byte(nil), data[:count]...)
				delivered := make(chan struct{})
				if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					defer close(delivered)
					_ = m.runtime.RunJob(vm, "net.Socket data", func() error {
						var value any = buffer.WrapBytes(vm, chunk)
						if currentEncoding := encoding.Load().(string); currentEncoding != "" {
							value = buffer.EncodeBytes(vm, chunk, vm.ToValue(currentEncoding))
						}
						return events.Emit(vm, socket, "data", value)
					})
				}) {
					closeSocket()
					return
				}
				<-delivered
			}
			if err != nil {
				closeSocket()
				m.runtime.RunOnLoop(func(vm *goja.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket close", func() error {
						_ = socket.Set("destroyed", true)
						_ = socket.Set("readyState", "closed")
						if !errors.Is(err, io.EOF) && !isNetClosed(err) {
							if emitErr := events.Emit(vm, socket, "error", vm.NewGoError(err)); emitErr != nil {
								return emitErr
							}
						}
						if emitErr := events.Emit(vm, socket, "end"); emitErr != nil {
							return emitErr
						}
						return events.Emit(vm, socket, "close", false)
					})
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

func netCallback(call goja.FunctionCall) goja.Callable {
	for index := len(call.Arguments) - 1; index >= 0; index-- {
		if callback, ok := goja.AssertFunction(call.Arguments[index]); ok {
			return callback
		}
	}
	return nil
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
