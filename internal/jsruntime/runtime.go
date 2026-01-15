package jsruntime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

// JsRuntime 封装了 JS 虚拟机环境
type JsRuntime struct {
	loop *eventloop.EventLoop

	stopped    uint32
	eventState *runtimeEventState
}

// RunScript 在 EventLoop 中执行脚本
func (r *JsRuntime) RunScript(script string) (goja.Value, error) {
	type result struct {
		val goja.Value
		err error
	}
	ch := make(chan result, 1)

	r.loop.RunOnLoop(func(vm *goja.Runtime) {
		v, err := vm.RunString(script)
		ch <- result{val: v, err: err}
	})

	res := <-ch
	return res.val, res.err
}

// RunScriptWithTimeout 带超时的脚本执行
func (r *JsRuntime) RunScriptWithTimeout(script string, timeout time.Duration) (goja.Value, error) {
	type result struct {
		val goja.Value
		err error
	}
	ch := make(chan result, 1)

	// 提交任务
	r.loop.RunOnLoop(func(vm *goja.Runtime) {
		v, err := vm.RunString(script)
		ch <- result{val: v, err: err}
	})

	select {
	case res := <-ch:
		return res.val, res.err
	case <-time.After(timeout):
		// 注意：如果不终止 Loop，之前的 RunString 还会继续占用资源
		// 真正的超时中断需要 vm.Interrupt，但这比较复杂，这里仅返回超时错误
		return nil, errors.New("execution timed out")
	}
}

// Call 调用 JS 全局函数
func (r *JsRuntime) Call(functionName string, params ...any) (goja.Value, error) {
	resCh := r.CallAsync(functionName, params...)
	res := <-resCh
	return res.Value, res.Err
}

type CallResult struct {
	Value goja.Value
	Err   error
}

// CallAsync 异步调用 JS 全局函数。
// - 如果函数返回普通值：channel 立刻返回结果
// - 如果函数返回 Promise/thenable：channel 在 resolve/reject 后返回结果
func (r *JsRuntime) CallAsync(functionName string, params ...any) <-chan CallResult {
	ch := make(chan CallResult, 1)
	var once sync.Once
	finish := func(v goja.Value, err error) {
		once.Do(func() {
			ch <- CallResult{Value: v, Err: err}
			close(ch)
		})
	}

	r.loop.RunOnLoop(func(vm *goja.Runtime) {
		fnObj := vm.Get(functionName)
		fn, ok := goja.AssertFunction(fnObj)
		if !ok {
			finish(nil, fmt.Errorf("function not found: %s", functionName))
			return
		}

		vals := make([]goja.Value, len(params))
		for i, p := range params {
			vals[i] = vm.ToValue(p)
		}

		v, err := fn(goja.Undefined(), vals...)
		if err != nil {
			finish(nil, err)
			return
		}

		if isThenable(vm, v) {
			thenObj := v.ToObject(vm)
			thenVal := thenObj.Get("then")
			thenFn, ok := goja.AssertFunction(thenVal)
			if !ok {
				finish(nil, fmt.Errorf("thenable missing then(): %s", functionName))
				return
			}

			onFulfilled := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				finish(call.Argument(0), nil)
				return goja.Undefined()
			})
			onRejected := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				finish(nil, promiseRejectionToError(call.Argument(0)))
				return goja.Undefined()
			})

			// 绑定 then(resolve, reject)
			if _, err := thenFn(v, onFulfilled, onRejected); err != nil {
				finish(nil, err)
				return
			}

			// 对“立即 resolve/reject”的 Promise，主动触发一次微任务队列推进
			_, _ = vm.RunString("void 0")
			return
		}

		finish(v, nil)
	})

	return ch
}

func isThenable(vm *goja.Runtime, v goja.Value) bool {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return false
	}
	obj := v.ToObject(vm)
	if obj == nil {
		return false
	}
	thenVal := obj.Get("then")
	_, ok := goja.AssertFunction(thenVal)
	return ok
}

func promiseRejectionToError(reason goja.Value) error {
	if reason == nil || goja.IsUndefined(reason) || goja.IsNull(reason) {
		return errors.New("promise rejected")
	}

	// Error 对象通常有 message
	if obj, ok := reason.(*goja.Object); ok {
		if msg := obj.Get("message"); msg != nil && !goja.IsUndefined(msg) && !goja.IsNull(msg) {
			m := strings.TrimSpace(msg.String())
			if m != "" {
				return errors.New(m)
			}
		}
	}

	return errors.New(reason.String())
}

// HasFunction 检查函数是否存在
func (r *JsRuntime) HasFunction(functionName string) bool {
	ch := make(chan bool, 1)
	r.loop.RunOnLoop(func(vm *goja.Runtime) {
		fn := vm.Get(functionName)
		_, ok := goja.AssertFunction(fn)
		ch <- ok
	})
	return <-ch
}

func (r *JsRuntime) RunOnLoop(fn func(vm *goja.Runtime)) {
	r.loop.RunOnLoop(func(vm *goja.Runtime) {
		fn(vm)
	})
}

// Stop 停止 EventLoop
func (r *JsRuntime) Stop() {
	if r == nil {
		return
	}
	if !atomic.CompareAndSwapUint32(&r.stopped, 0, 1) {
		return
	}
	if r.eventState != nil {
		r.eventState.cleanup()
	}
	if r.loop != nil {
		r.loop.Stop()
	}
}
