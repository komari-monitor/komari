package tlsmodule

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	netmodule "github.com/komari-monitor/komari/pkg/jsruntime/net"
)

// Module provides the subset of node:tls needed by notification plugins.
type Module struct {
	runtime   *bridge.Runtime
	netModule *netmodule.Module
}

func New(runtime *bridge.Runtime, netModule *netmodule.Module) *Module {
	return &Module{runtime: runtime, netModule: netModule}
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	connect := func(call goja.FunctionCall) goja.Value { return m.connect(vm, call) }
	_ = exports.Set("connect", connect)
	_ = exports.Set("createConnection", connect)
	_ = module.Set("exports", exports)
}

func (m *Module) connect(vm *goja.Runtime, call goja.FunctionCall) *goja.Object {
	options, ok := call.Argument(0).(*goja.Object)
	if !ok {
		panic(vm.NewTypeError("tls.connect requires an options object"))
	}

	host := stringOption(options, "host")
	port := intOption(options, "port")
	serverName := stringOption(options, "servername")
	if serverName == "" {
		serverName = host
	}
	var sourceSocket *goja.Object
	if value := options.Get("socket"); value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		sourceSocket, ok = value.(*goja.Object)
		if !ok {
			panic(vm.NewTypeError("tls.connect socket must be a net.Socket"))
		}
	}
	if sourceSocket == nil && (host == "" || port < 1) {
		panic(vm.NewTypeError("tls.connect requires host and port"))
	}
	if serverName == "" {
		panic(vm.NewTypeError("tls.connect requires servername when upgrading a socket"))
	}

	config := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if ca := stringOption(options, "ca"); ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			panic(vm.NewTypeError("tls.connect ca does not contain a certificate"))
		}
		config.RootCAs = roots
	}

	socket := events.NewEmitter(vm)
	_ = socket.Set("connecting", true)
	_ = socket.Set("encrypted", true)
	_ = socket.Set("authorized", false)
	_ = socket.Set("servername", serverName)

	ctx, cancel := context.WithTimeout(context.Background(), m.runtime.Timeout())
	_ = socket.Set("destroy", func() *goja.Object {
		cancel()
		return socket
	})
	if callback, ok := goja.AssertFunction(call.Argument(1)); ok {
		once, _ := goja.AssertFunction(socket.Get("once"))
		_, _ = once(socket, vm.ToValue("secureConnect"), vm.ToValue(callback))
	}

	go m.establish(ctx, cancel, socket, sourceSocket, host, port, config)
	return socket
}

func (m *Module) establish(
	ctx context.Context,
	cancel context.CancelFunc,
	socket *goja.Object,
	sourceSocket *goja.Object,
	host string,
	port int,
	config *tls.Config,
) {
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

	resourceID := m.runtime.AddResource(func() { _ = connection.Close() })
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

	queued := m.runtime.RunOnLoop(func(vm *goja.Runtime) {
		_ = m.runtime.RunJob(vm, "tls secureConnect", func() error {
			if !m.netModule.AttachSocket(vm, socket, secureConnection, nil) {
				return fmt.Errorf("TLS socket runtime is closed")
			}
			m.runtime.RemoveResource(resourceID)
			_ = socket.Set("authorized", true)
			return events.Emit(vm, socket, "secureConnect")
		})
	})
	if !queued {
		m.runtime.RemoveResource(resourceID)
		_ = secureConnection.Close()
	}
}

func (m *Module) emitError(socket *goja.Object, err error) {
	m.runtime.RunOnLoop(func(vm *goja.Runtime) {
		_ = m.runtime.RunJob(vm, "tls error", func() error {
			_ = socket.Set("connecting", false)
			return events.Emit(vm, socket, "error", vm.NewGoError(err))
		})
	})
}

func stringOption(options *goja.Object, name string) string {
	value := options.Get(name)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	return value.String()
}

func intOption(options *goja.Object, name string) int {
	value := options.Get(name)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	return int(value.ToInteger())
}
