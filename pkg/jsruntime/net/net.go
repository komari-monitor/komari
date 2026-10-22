package net

import (
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)
import (
	"context"
	"errors"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/writequeue"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Module struct {
	runtime     *bridge.Runtime
	allowListen bool
	sockets     sync.Map
}
type socketState struct {
	connection net.Conn
	resourceID uint64
	readerDone chan struct {
	}
	detaching atomic.Bool
	closed    atomic.Bool
}

func New(runtime *bridge.Runtime, allowListen bool) *Module {
	return &Module{runtime: runtime, allowListen: allowListen}
}
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()
	_ = native.Set(vm, exports, "createServer", native.Bind(vm, "createServer", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		server := m.newNetServer(vm)
		listener := moejs.Arg(args, 0)
		if _, ok := callable(listener); !ok {
			listener = moejs.Arg(args, 1)
		}
		if _, ok := callable(listener); ok {
			on, _ := callable(native.Get(vm, server, "on"))
			_, _ = native.Call(vm, on, native.From(vm, server), native.From(vm, "connection"), native.From(vm, listener))
		}
		return native.From(vm, server), nil
	}))
	connect := func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.netConnect(vm, args)), nil
	}
	_ = native.Set(vm, exports, "connect", native.Bind(vm, "connect", 0, connect))
	_ = native.Set(vm, exports, "createConnection", native.Bind(vm, "connect", 0, connect))
	_ = native.Set(vm, exports, "isIP", native.Bind(vm, "isIP", 1, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, parseIPVersion(native.String(vm, moejs.Arg(args, 0)))), nil
	}))
	_ = native.Set(vm, exports, "isIPv4", native.Bind(vm, "isIPv4", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := native.String(vm, moejs.Arg(args, 0))
		ip := net.ParseIP(value)
		return native.From(vm, ip != nil && ip.To4() != nil), nil
	}))
	_ = native.Set(vm, exports, "isIPv6", native.Bind(vm, "isIPv6", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := native.String(vm, moejs.Arg(args, 0))
		ip := net.ParseIP(value)
		return native.From(vm, ip != nil && ip.To4() == nil), nil
	}))
	_ = native.Set(vm, exports, "getDefaultAutoSelectFamily", native.Bind(vm, "getDefaultAutoSelectFamily", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, true), nil
	}))
	_ = native.Set(vm, exports, "setDefaultAutoSelectFamily", native.Bind(vm, "setDefaultAutoSelectFamily", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.setDefaultAutoSelectFamily is not supported by jsruntime; autoSelectFamily is always enabled")))
	}))
	_ = native.Set(vm, module, "exports", exports)
}
func (m *Module) newNetServer(vm *moejs.Runtime) *engine.Object {
	server := events.NewEmitter(vm)
	var listener net.Listener
	var resourceID uint64
	var serverMu sync.RWMutex
	var listenGeneration uint64
	var listenPending bool
	var connections atomic.Int64
	_ = native.Set(vm, server, "listening", false)
	_ = native.Set(vm, server, "maxConnections", 0)
	_ = native.Set(vm, server, "listen", native.Bind(vm, "listen", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if !m.allowListen {
			panic(native.GoError(vm, fmt.Errorf("server.listen requires AllowListen")))
		}
		address, callback := netListenArguments(vm, args)
		serverMu.Lock()
		if listenPending || listener != nil {
			serverMu.Unlock()
			panic(native.GoError(vm, fmt.Errorf("server is already listening")))
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
					m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
						_ = m.runtime.RunJob(vm, "net.Server error", func() error {
							return events.Emit(vm, server, "error", native.GoError(vm, err))
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
			id := m.runtime.AddResource(func() {
				_ = created.Close()
			})
			if id == 0 {
				listenPending = false
				serverMu.Unlock()
				return
			}
			listener = created
			resourceID = id
			listenPending = false
			serverMu.Unlock()
			m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
				serverMu.RLock()
				active := listenGeneration == generation && listener == created
				serverMu.RUnlock()
				if !active {
					return
				}
				if !native.IsNil(callback) {
					once, _ := callable(native.Get(vm, server, "once"))
					_, _ = native.Call(vm, once, native.From(vm, server), native.From(vm, "listening"), native.From(vm, callback))
				}
				_ = m.runtime.RunJob(vm, "net.Server listening", func() error {
					_ = native.Set(vm, server, "listening", true)
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
						m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
							_ = m.runtime.RunJob(vm, "net.Server error", func() error {
								return events.Emit(vm, server, "error", native.GoError(vm, acceptErr))
							})
						})
					}
					return
				}
				connections.Add(1)
				if !m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					socket := m.newNetSocket(vm, connection, func() {
						connections.Add(-1)
					})
					if native.Bool(native.Get(vm, socket, "destroyed")) {
						return
					}
					_ = m.runtime.RunJob(vm, "net.Server connection", func() error {
						return events.Emit(vm, server, "connection", socket)
					})
				}) {
					connections.Add(-1)
					_ = connection.Close()
					return
				}
			}
		}()
		return native.From(vm, server), nil
	}))
	_ = native.Set(vm, server, "close", native.Bind(vm, "close", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if callback, ok := callable(moejs.Arg(args, 0)); ok {
			once, _ := callable(native.Get(vm, server, "once"))
			_, _ = native.Call(vm, once, native.From(vm, server), native.From(vm, "close"), native.From(vm, callback))
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
		_ = native.Set(vm, server, "listening", false)
		if wasActive {
			m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "net.Server close", func() error {
					return events.Emit(vm, server, "close")
				})
			}, 0)
		}
		return native.From(vm, server), nil
	}))
	_ = native.Set(vm, server, "closeAllConnections", native.Bind(vm, "closeAllConnections", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Server.closeAllConnections is not supported by jsruntime; connections are not tracked individually")))
	}))
	_ = native.Set(vm, server, "closeIdleConnections", native.Bind(vm, "closeIdleConnections", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Server.closeIdleConnections is not supported by jsruntime; connections are not tracked individually")))
	}))
	_ = native.Set(vm, server, "address", native.Bind(vm, "address", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		serverMu.RLock()
		currentListener := listener
		serverMu.RUnlock()
		if currentListener == nil {
			return native.From(vm, moejs.Null()), nil
		}
		host, port, _ := net.SplitHostPort(currentListener.Addr().String())
		portNumber, _ := strconv.Atoi(port)
		return native.From(vm, map[string]any{"address": host, "family": netAddressFamily(host), "port": portNumber}), nil
	}))
	_ = native.Set(vm, server, "getConnections", native.Bind(vm, "getConnections", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		callback := moejs.Arg(args, 0)
		m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
			_ = m.runtime.RunJob(vm, "net.Server.getConnections", func() error {
				_, err := native.Call(vm, callback, native.From(vm, moejs.Undefined()), native.From(vm, moejs.Null()), native.From(vm, connections.Load()))
				return err
			})
		}, 0)
		return moejs.Undefined(), nil
	}))
	_ = native.Set(vm, server, "ref", native.Bind(vm, "ref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Server.ref is not supported by jsruntime; the event loop is host-driven")))
	}))
	_ = native.Set(vm, server, "unref", native.Bind(vm, "unref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Server.unref is not supported by jsruntime; the event loop is host-driven")))
	}))
	return server
}
func netListenArguments(vm *moejs.Runtime, args []moejs.Value) (string, moejs.Value) {
	host := "127.0.0.1"
	port := int(native.Int(vm, moejs.Arg(args, 0)))
	callbackIndex := 1
	if object, ok := asObject(moejs.Arg(args, 0)); ok {
		port = int(native.Int(vm, native.Get(vm, object, "port")))
		if value := native.Get(vm, object, "host"); !value.IsUndefined() {
			host = native.String(vm, value)
		}
		callbackIndex = 1
	} else if value := moejs.Arg(args, 1); !value.IsUndefined() {
		if _, ok := callable(value); !ok {
			host = native.String(vm, value)
			callbackIndex = 2
		}
	}
	callback, _ := callable(moejs.Arg(args, callbackIndex))
	return net.JoinHostPort(host, strconv.Itoa(port)), callback
}
func (m *Module) netConnect(vm *moejs.Runtime, args []moejs.Value) *engine.Object {
	host := "127.0.0.1"
	port := int(native.Int(vm, moejs.Arg(args, 0)))
	callbackIndex := 1
	if object, ok := asObject(moejs.Arg(args, 0)); ok {
		port = int(native.Int(vm, native.Get(vm, object, "port")))
		if value := native.Get(vm, object, "host"); !value.IsUndefined() {
			host = native.String(vm, value)
		}
	} else if _, ok := callable(moejs.Arg(args, 1)); !ok && !moejs.Arg(args, 1).IsUndefined() {
		host = native.String(vm, moejs.Arg(args, 1))
		callbackIndex = 2
	}
	placeholder := events.NewEmitter(vm)
	_ = native.Set(vm, placeholder, "connecting", true)
	var pendingEncoding string
	_ = native.Set(vm, placeholder, "setEncoding", native.Bind(vm, "setEncoding", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := native.String(vm, moejs.Arg(args, 0))
		pendingEncoding = value
		return native.From(vm, placeholder), nil
	}))
	_ = native.Set(vm, placeholder, "setTimeout", native.Bind(vm, "setTimeout", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		milliseconds := native.Int(vm, moejs.Arg(args, 0))
		callback := moejs.Arg(args, 1)
		if function, ok := callable(callback); ok {
			once, _ := callable(native.Get(vm, placeholder, "once"))
			_, _ = native.Call(vm, once, native.From(vm, placeholder), native.From(vm, "timeout"), native.From(vm, function))
		}
		if milliseconds > 0 {
			m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "net.Socket timeout", func() error {
					return events.Emit(vm, placeholder, "timeout")
				})
			}, time.Duration(milliseconds)*time.Millisecond)
		}
		return native.From(vm, placeholder), nil
	}))
	_ = native.Set(vm, placeholder, "setNoDelay", native.Bind(vm, "setNoDelay", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.setNoDelay is not supported by jsruntime before the connection is established")))
	}))
	_ = native.Set(vm, placeholder, "setKeepAlive", native.Bind(vm, "setKeepAlive", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.setKeepAlive is not supported by jsruntime before the connection is established")))
	}))
	_ = native.Set(vm, placeholder, "ref", native.Bind(vm, "ref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.ref is not supported by jsruntime; the event loop is host-driven")))
	}))
	_ = native.Set(vm, placeholder, "unref", native.Bind(vm, "unref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.unref is not supported by jsruntime; the event loop is host-driven")))
	}))
	if callback, ok := callable(moejs.Arg(args, callbackIndex)); ok {
		once, _ := callable(native.Get(vm, placeholder, "once"))
		_, _ = native.Call(vm, once, native.From(vm, placeholder), native.From(vm, "connect"), native.From(vm, callback))
	}
	dialContext, cancelDial := context.WithCancel(context.Background())
	pendingResourceID := m.runtime.AddResource(cancelDial)
	if pendingResourceID == 0 {
		_ = native.Set(vm, placeholder, "connecting", false)
		_ = native.Set(vm, placeholder, "destroyed", true)
		return placeholder
	}
	go func() {
		connection, err := (&net.Dialer{Timeout: m.runtime.Timeout()}).DialContext(dialContext, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		cancelDial()
		m.runtime.RemoveResource(pendingResourceID)
		queued := m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
			attached := false
			defer func() {
				if !attached && connection != nil {
					_ = connection.Close()
				}
			}()
			_ = m.runtime.RunJob(vm, "net.Socket connect", func() error {
				if err != nil {
					_ = native.Set(vm, placeholder, "connecting", false)
					return events.Emit(vm, placeholder, "error", native.GoError(vm, err))
				}
				if !m.configureNetSocket(vm, placeholder, connection, nil) {
					return nil
				}
				attached = true
				if pendingEncoding != "" {
					if setEncoding, ok := callable(native.Get(vm, placeholder, "setEncoding")); ok {
						_, _ = native.Call(vm, setEncoding, native.From(vm, placeholder), native.From(vm, pendingEncoding))
					}
				}
				_ = native.Set(vm, placeholder, "connecting", false)
				return events.Emit(vm, placeholder, "connect")
			})
		})
		if !queued && connection != nil {
			_ = connection.Close()
		}
	}()
	return placeholder
}
func (m *Module) newNetSocket(vm *moejs.Runtime, connection net.Conn, onClose func()) *engine.Object {
	socket := events.NewEmitter(vm)
	if !m.configureNetSocket(vm, socket, connection, onClose) {
		_ = native.Set(vm, socket, "destroyed", true)
		_ = native.Set(vm, socket, "readyState", "closed")
	}
	return socket
}

func (m *Module) AttachSocket(vm *moejs.Runtime, socket *engine.Object, connection net.Conn, onClose func()) bool {
	return m.configureNetSocket(vm, socket, connection, onClose)
}

func (m *Module) DetachSocket(socket *engine.Object) (net.Conn, error) {
	value, ok := m.sockets.Load(socket)
	if !ok {
		return nil, errors.New("socket is not connected")
	}
	state := value.(*socketState)
	if !state.detaching.CompareAndSwap(false, true) {
		return nil, errors.New("socket is already being upgraded")
	}
	if err := state.connection.SetReadDeadline(time.Now()); err != nil {
		state.detaching.Store(false)
		return nil, err
	}
	<-state.readerDone
	if state.closed.Load() {
		return nil, errors.New("socket closed during upgrade")
	}
	m.sockets.Delete(socket)
	m.runtime.RemoveResource(state.resourceID)
	if err := state.connection.SetDeadline(time.Time{}); err != nil {
		_ = state.connection.Close()
		return nil, err
	}
	return state.connection, nil
}
func (m *Module) configureNetSocket(vm *moejs.Runtime, socket *engine.Object, connection net.Conn, onClose func()) bool {
	localHost, localPort := splitNetAddress(connection.LocalAddr())
	remoteHost, remotePort := splitNetAddress(connection.RemoteAddr())
	_ = native.Set(vm, socket, "connecting", false)
	_ = native.Set(vm, socket, "pending", false)
	_ = native.Set(vm, socket, "destroyed", false)
	_ = native.Set(vm, socket, "readyState", "open")
	_ = native.Set(vm, socket, "remoteAddress", remoteHost)
	_ = native.Set(vm, socket, "remotePort", remotePort)
	_ = native.Set(vm, socket, "remoteFamily", netAddressFamily(remoteHost))
	_ = native.Set(vm, socket, "localAddress", localHost)
	_ = native.Set(vm, socket, "localPort", localPort)
	_ = native.Set(vm, socket, "localFamily", netAddressFamily(localHost))
	resourceID := m.runtime.AddResource(func() {
		_ = connection.Close()
	})
	if resourceID == 0 {
		_ = connection.Close()
		if onClose != nil {
			onClose()
		}
		return false
	}
	state := &socketState{connection: connection, resourceID: resourceID, readerDone: make(chan struct {
	})}
	m.sockets.Store(socket, state)
	var encoding atomic.Value
	encoding.Store("")
	var writes writequeue.Queue
	closeSocket := func() {
		if state.closed.CompareAndSwap(false, true) {
			m.sockets.Delete(socket)
			m.runtime.RemoveResource(resourceID)
			_ = connection.Close()
			if onClose != nil {
				onClose()
			}
		}
	}
	_ = native.Set(vm, socket, "write", native.Bind(vm, "write", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		data := append([]byte(nil), buffer.Bytes(vm, moejs.Arg(args, 0))...)
		callback := netCallback(args)
		writes.Submit(func() {
			_, err := connection.Write(data)
			if !native.IsNil(callback) {
				m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket.write", func() error {
						if err != nil {
							_, callbackErr := native.Call(vm, callback, native.From(vm, moejs.Undefined()), native.From(vm, native.GoError(vm, err)))
							return callbackErr
						}
						_, callbackErr := native.Call(vm, callback, native.From(vm, moejs.Undefined()))
						return callbackErr
					})
				})
			}
		})
		return native.From(vm, true), nil
	}))
	_ = native.Set(vm, socket, "end", native.Bind(vm, "end", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		var data []byte
		_, firstArgumentIsCallback := callable(moejs.Arg(args, 0))
		if len(args) > 0 && !firstArgumentIsCallback && !moejs.Arg(args, 0).IsUndefined() && !moejs.Arg(args, 0).IsNull() {
			data = append([]byte(nil), buffer.Bytes(vm, moejs.Arg(args, 0))...)
		}
		callback := netCallback(args)
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
			if !native.IsNil(callback) {
				m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket.end", func() error {
						if err != nil {
							_, callbackErr := native.Call(vm, callback, native.From(vm, moejs.Undefined()), native.From(vm, native.GoError(vm, err)))
							return callbackErr
						}
						_, callbackErr := native.Call(vm, callback, native.From(vm, moejs.Undefined()))
						return callbackErr
					})
				})
			}
		})
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "destroy", native.Bind(vm, "destroy", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		closeSocket()
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "setEncoding", native.Bind(vm, "setEncoding", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		value := native.String(vm, moejs.Arg(args, 0))
		encoding.Store(value)
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "setTimeout", native.Bind(vm, "setTimeout", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		milliseconds := native.Int(vm, moejs.Arg(args, 0))
		callback := moejs.Arg(args, 1)
		if milliseconds <= 0 {
			_ = connection.SetDeadline(time.Time{})
		} else {
			_ = connection.SetDeadline(time.Now().Add(time.Duration(milliseconds) * time.Millisecond))
		}
		if function, ok := callable(callback); ok {
			once, _ := callable(native.Get(vm, socket, "once"))
			_, _ = native.Call(vm, once, native.From(vm, socket), native.From(vm, "timeout"), native.From(vm, function))
		}
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "setNoDelay", native.Bind(vm, "setNoDelay", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		enabled := native.Bool(moejs.Arg(args, 0))
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(enabled)
		}
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "setKeepAlive", native.Bind(vm, "setKeepAlive", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		enabled := native.Bool(moejs.Arg(args, 0))
		if tcp, ok := connection.(*net.TCPConn); ok {
			_ = tcp.SetKeepAlive(enabled)
		}
		return native.From(vm, socket), nil
	}))
	_ = native.Set(vm, socket, "address", native.Bind(vm, "address", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, map[string]any{"address": localHost, "family": netAddressFamily(localHost), "port": localPort}), nil
	}))
	_ = native.Set(vm, socket, "pause", native.Bind(vm, "pause", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.pause is not supported by jsruntime; no backpressure")))
	}))
	_ = native.Set(vm, socket, "resume", native.Bind(vm, "resume", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.resume is not supported by jsruntime; no backpressure")))
	}))
	_ = native.Set(vm, socket, "ref", native.Bind(vm, "ref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.ref is not supported by jsruntime; the event loop is host-driven")))
	}))
	_ = native.Set(vm, socket, "unref", native.Bind(vm, "unref", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		panic(native.GoError(vm, fmt.Errorf("net.Socket.unref is not supported by jsruntime; the event loop is host-driven")))
	}))
	go func() {
		defer close(state.readerDone)
		data := make([]byte, 32*1024)
		for {
			count, err := connection.Read(data)
			if count > 0 {
				chunk := append([]byte(nil), data[:count]...)
				delivered := make(chan struct {
				})
				if !m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					defer close(delivered)
					_ = m.runtime.RunJob(vm, "net.Socket data", func() error {
						var value any = buffer.WrapBytes(vm, chunk)
						if currentEncoding := encoding.Load().(string); currentEncoding != "" {
							value = buffer.EncodeBytes(vm, chunk, native.From(vm, currentEncoding))
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
				if state.detaching.Load() {
					return
				}
				closeSocket()
				m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
					_ = m.runtime.RunJob(vm, "net.Socket close", func() error {
						_ = native.Set(vm, socket, "destroyed", true)
						_ = native.Set(vm, socket, "readyState", "closed")
						if !errors.Is(err, io.EOF) && !isNetClosed(err) {
							if emitErr := events.Emit(vm, socket, "error", native.GoError(vm, err)); emitErr != nil {
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
func netCallback(args []moejs.Value) moejs.Value {
	for index := len(args) - 1; index >= 0; index-- {
		if callback, ok := callable(args[index]); ok {
			return callback
		}
	}
	return moejs.Undefined()
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
func callable(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
func asObject(v moejs.Value) (*engine.Object, bool) {
	if !v.IsObject() {
		return nil, false
	}
	return v.AsObject(), true
}
