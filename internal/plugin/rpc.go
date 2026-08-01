package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// registerRPC registers a plugin-owned RPC method. The handler is a
// JavaScript function receiving the JSON-RPC params and returning the result
// (or throwing an Error with optional code/data). Called from the plugin's
// own event loop during script evaluation; it takes the manager lock itself.
func (m *Manager) registerRPC(short, method string, handler goja.Callable) error {
	method = strings.TrimSpace(method)
	if method == "" || strings.HasPrefix(method, "rpc.") {
		return fmt.Errorf("invalid RPC method name %q", method)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[short]
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	inst.mu.Lock()
	if inst.rpcMethods == nil {
		inst.rpcMethods = make(map[string]goja.Callable)
	}
	if _, exists := inst.rpcMethods[method]; exists {
		inst.mu.Unlock()
		return nil // already registered in this load
	}
	inst.rpcMethods[method] = handler
	inst.mu.Unlock()
	if err := rpc.Register(method, m.rpcHandler(short, method)); err != nil {
		inst.mu.Lock()
		delete(inst.rpcMethods, method)
		inst.mu.Unlock()
		return err
	}
	return nil
}

// rpcHandler bridges one registered RPC call into the plugin event loop.
func (m *Manager) rpcHandler(short, method string) rpc.Handler {
	return func(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		inst := m.instanceFor(short)
		if inst == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: fmt.Sprintf("plugin %q is not loaded", short)}
		}
		inst.mu.RLock()
		handler := inst.rpcMethods[method]
		host := inst.host
		alive := inst.runtime != nil
		inst.mu.RUnlock()
		if !alive || handler == nil || host == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.MethodNotFound, Message: "method not found", Data: method}
		}

		resultCh := make(chan any, 1)
		errCh := make(chan *rpc.JsonRpcError, 1)
		queued := host.RunOnLoop(func(vm *goja.Runtime) {
			_ = host.RunJob(vm, "plugin rpc "+method, func() error {
				params := vm.ToValue(req.Params)
				if params == nil {
					params = goja.Null()
				}
				value, callErr := handler(goja.Undefined(), params)
				if callErr != nil {
					errCh <- jsErrorToRPCError(vm, callErr)
					return nil // error is captured, do not double-report
				}
				exported, exportErr := exportJSValue(value)
				if exportErr != nil {
					errCh <- &rpc.JsonRpcError{Code: rpc.InternalError, Message: exportErr.Error()}
					return nil
				}
				resultCh <- exported
				return nil
			})
		})
		if !queued {
			return nil, &rpc.JsonRpcError{Code: rpc.Unavailable, Message: "plugin runtime is closed"}
		}
		timer := time.NewTimer(host.Timeout())
		defer timer.Stop()
		select {
		case result := <-resultCh:
			return result, nil
		case rpcErr := <-errCh:
			return nil, rpcErr
		case <-timer.C:
			return nil, &rpc.JsonRpcError{Code: rpc.DeadlineExceeded, Message: "plugin RPC handler timed out"}
		}
	}
}

// exportJSValue exports a goja value into a JSON-RPC result. Circular
// structures and other export failures become an error instead of panicking
// inside the event loop.
func exportJSValue(value goja.Value) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("export JavaScript value: %v", recovered)
		}
	}()
	return value.Export(), nil
}

// jsErrorToRPCError converts a thrown JavaScript error into a JSON-RPC error,
// honoring optional code/data properties.
func jsErrorToRPCError(vm *goja.Runtime, err error) *rpc.JsonRpcError {
	code := rpc.InternalError
	message := err.Error()
	var data any
	var exception *goja.Exception
	if errors.As(err, &exception) {
		if obj, ok := exception.Value().(*goja.Object); ok {
			if value := obj.Get("code"); value != nil && !goja.IsUndefined(value) {
				if n := value.ToInteger(); n != 0 {
					code = int(n)
				}
			}
			if value := obj.Get("message"); value != nil && !goja.IsUndefined(value) && value.String() != "" {
				message = value.String()
			}
			if value := obj.Get("data"); value != nil && !goja.IsUndefined(value) {
				if exported, exportErr := exportJSValue(value); exportErr == nil {
					data = exported
				}
			}
		}
	}
	return &rpc.JsonRpcError{Code: code, Message: message, Data: data}
}
