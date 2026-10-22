package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// registerRPC registers a plugin-owned RPC method. The handler is a
// JavaScript function receiving the JSON-RPC params and returning the result
// (or throwing an Error with optional code/data). Called from the plugin's
// own event loop during script evaluation; it takes the manager lock itself.
func (m *Manager) registerRPC(short, method string, handler moejs.Value) error {
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
		inst.rpcMethods = make(map[string]moejs.Value)
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
		if !alive || handler.IsUndefined() || host == nil {
			return nil, &rpc.JsonRpcError{Code: rpc.MethodNotFound, Message: "method not found", Data: method}
		}

		resultCh := make(chan any, 1)
		errCh := make(chan *rpc.JsonRpcError, 1)
		queued := host.RunOnLoop(func(vm *moejs.Runtime) {
			_ = host.RunJob(vm, "plugin rpc "+method, func() error {
				params := native.From(vm, req.Params)
				if params.IsUndefined() {
					params = moejs.Null()
				}
				value, callErr := vm.CallFunction(handler, moejs.Undefined(), params)
				if callErr != nil {
					errCh <- jsErrorToRPCError(vm, callErr)
					return nil // error is captured, do not double-report
				}
				exported, exportErr := exportJSValue(vm, value)
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

// exportJSValue exports a native JavaScript value into a JSON-RPC result. Circular
// structures and other export failures become an error instead of panicking
// inside the event loop.
func exportJSValue(vm *moejs.Runtime, value moejs.Value) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("export JavaScript value: %v", recovered)
		}
	}()
	return vm.ToGo(value)
}

// jsErrorToRPCError converts a thrown JavaScript error into a JSON-RPC error,
// honoring optional code/data properties.
func jsErrorToRPCError(vm *moejs.Runtime, err error) *rpc.JsonRpcError {
	code := rpc.InternalError
	message := err.Error()
	var data any
	var exception *moejs.Exception
	if errors.As(err, &exception) {
		if exception.Value.IsObject() {
			obj := exception.Value.AsObject()
			if value := native.Get(vm, obj, "code"); !value.IsUndefined() {
				if n := native.Int(vm, value); n != 0 {
					code = int(n)
				}
			}
			if value := native.Get(vm, obj, "message"); !value.IsUndefined() && native.String(vm, value) != "" {
				message = native.String(vm, value)
			}
			if value := native.Get(vm, obj, "data"); !value.IsUndefined() {
				if exported, exportErr := exportJSValue(vm, value); exportErr == nil {
					data = exported
				}
			}
		}
	}
	return &rpc.JsonRpcError{Code: code, Message: message, Data: data}
}
