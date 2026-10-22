package tlsmodule

import (
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)
import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	netmodule "github.com/komari-monitor/komari/pkg/jsruntime/net"
	"net"
	"strconv"
)

type Module struct {
	runtime   *bridge.Runtime
	netModule *netmodule.Module
}

func New(runtime *bridge.Runtime, netModule *netmodule.Module) *Module {
	return &Module{runtime: runtime, netModule: netModule}
}
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()
	connect := func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.connect(vm, args)), nil
	}
	_ = native.Set(vm, exports, "connect", native.Bind(vm, "connect", 1, connect))
	_ = native.Set(vm, exports, "createConnection", native.Bind(vm, "createConnection", 1, connect))
	_ = native.Set(vm, module, "exports", exports)
}
func (m *Module) connect(vm *moejs.Runtime, args []moejs.Value) *engine.Object {
	options, ok := asObject(moejs.Arg(args, 0))
	if !ok {
		panic(native.TypeError(vm, "tls.connect requires an options object"))
	}
	host := stringOption(vm, options, "host")
	port := intOption(vm, options, "port")
	serverName := stringOption(vm, options, "servername")
	if serverName == "" {
		serverName = host
	}
	var sourceSocket *engine.Object
	if value := native.Get(vm, options, "socket"); !native.IsNil(value) && !value.IsUndefined() && !value.IsNull() {
		sourceSocket, ok = asObject(value)
		if !ok {
			panic(native.TypeError(vm, "tls.connect socket must be a net.Socket"))
		}
	}
	if sourceSocket == nil && (host == "" || port < 1) {
		panic(native.TypeError(vm, "tls.connect requires host and port"))
	}
	if serverName == "" {
		panic(native.TypeError(vm, "tls.connect requires servername when upgrading a socket"))
	}
	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if ca := stringOption(vm, options, "ca"); ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			panic(native.TypeError(vm, "tls.connect ca does not contain a certificate"))
		}
		config.RootCAs = roots
	}
	socket := events.NewEmitter(vm)
	_ = native.Set(vm, socket, "connecting", true)
	_ = native.Set(vm, socket, "encrypted", true)
	_ = native.Set(vm, socket, "authorized", false)
	_ = native.Set(vm, socket, "servername", serverName)
	ctx, cancel := context.WithTimeout(context.Background(), m.runtime.Timeout())
	_ = native.Set(vm, socket, "destroy", native.Bind(vm, "destroy", 0, func(r *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		cancel()
		return native.From(vm, socket), nil
	}))
	if callback, ok := callable(moejs.Arg(args, 1)); ok {
		once, _ := callable(native.Get(vm, socket, "once"))
		_, _ = native.Call(vm, once, native.From(vm, socket), native.From(vm, "secureConnect"), native.From(vm, callback))
	}
	go m.establish(ctx, cancel, socket, sourceSocket, host, port, config)
	return socket
}
func (m *Module) establish(ctx context.Context, cancel context.CancelFunc, socket *engine.Object, sourceSocket *engine.Object, host string, port int, config *tls.Config) {
	defer cancel()
	var connection net.Conn
	var err error
	if sourceSocket != nil {
		connection, err = m.netModule.DetachSocket(sourceSocket)
	} else {
		connection, err = (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	}
	if err != nil {
		m.emitError(socket, err)
		return
	}
	resourceID := m.runtime.AddResource(func() {
		_ = connection.Close()
	})
	if resourceID == 0 {
		return
	}
	secureConnection := tls.Client(connection, config)
	if err := secureConnection.HandshakeContext(ctx); err != nil {
		m.runtime.RemoveResource(resourceID)
		_ = connection.Close()
		m.emitError(socket, fmt.Errorf("TLS handshake failed: %w", err))
		return
	}
	queued := m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
		_ = m.runtime.RunJob(vm, "tls secureConnect", func() error {
			if !m.netModule.AttachSocket(vm, socket, secureConnection, nil) {
				return fmt.Errorf("TLS socket runtime is closed")
			}
			m.runtime.RemoveResource(resourceID)
			_ = native.Set(vm, socket, "authorized", true)
			return events.Emit(vm, socket, "secureConnect")
		})
	})
	if !queued {
		m.runtime.RemoveResource(resourceID)
		_ = secureConnection.Close()
	}
}
func (m *Module) emitError(socket *engine.Object, err error) {
	m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
		_ = m.runtime.RunJob(vm, "tls error", func() error {
			_ = native.Set(vm, socket, "connecting", false)
			return events.Emit(vm, socket, "error", native.GoError(vm, err))
		})
	})
}
func stringOption(vm *moejs.Runtime, options *engine.Object, name string) string {
	value := native.Get(vm, options, name)
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return ""
	}
	return native.String(vm, value)
}
func intOption(vm *moejs.Runtime, options *engine.Object, name string) int {
	value := native.Get(vm, options, name)
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return 0
	}
	return int(native.Int(vm, value))
}
func callable(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
func asObject(v moejs.Value) (*engine.Object, bool) {
	if !v.IsObject() {
		return nil, false
	}
	return v.AsObject(), true
}
