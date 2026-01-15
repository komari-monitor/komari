package jsruntime

import (
	"errors"
	"net/http"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/dop251/goja_nodejs/require"
)

// Builder 构建 JsRuntime 的配置入口
type Builder struct {
	enableNodejs bool
	enableFetch  bool
	fetchClient  *http.Client
	enableXHR    bool
	xhrClient    *http.Client
	kv           *RamKv
	injectors    []Injector
}

// Injector 允许在构建时注入自定义能力
// 此时 VM 已经初始化，且处于 EventLoop 线程中
type Injector func(vm *goja.Runtime) error

// NewBuilder 返回 JsRuntime 的 builder
func NewBuilder() *Builder {
	return &Builder{}
}

// WithNodejs 启用 Node.js 风格的模块和 console 支持
func (b *Builder) WithNodejs() *Builder {
	b.enableNodejs = true
	return b
}

// WithFetch 向运行时注入全局 fetch() 函数
func (b *Builder) WithFetch() *Builder {
	b.enableFetch = true
	return b
}

// WithXHR 向运行时注入全局 XMLHttpRequest 构造函数
func (b *Builder) WithXHR() *Builder {
	b.enableXHR = true
	return b
}

// WithMemoryKv 向运行时注入内存 KV 存储对象
func (b *Builder) WithMemoryKv(kv ...*RamKv) *Builder {
	if len(kv) > 0 && kv[0] != nil {
		b.kv = kv[0]
	} else {
		b.kv = NewRamKv()
	}
	return b
}

// WithInjector 注册自定义注入函数
func (b *Builder) WithInjector(inj Injector) *Builder {
	if inj != nil {
		b.injectors = append(b.injectors, inj)
	}
	return b
}

// Build 构建并返回 JsRuntime
func (b *Builder) Build() (*JsRuntime, error) {
	// 1. 创建 EventLoop
	loop := eventloop.NewEventLoop()

	// 2. 启动 EventLoop 后台协程
	loop.Start()

	// 3. 使用 channel 同步等待初始化完成
	// 因为 loop.RunOnLoop 是异步的，我们需要阻塞 Build 直到环境准备好
	initCh := make(chan error, 1)

	loop.RunOnLoop(func(vm *goja.Runtime) {
		// 在这里执行所有的初始化逻辑，确保它们在 Loop 线程内运行
		var err error
		defer func() {
			// 捕获 panic 防止 crash，虽然 goja 内部通常会处理
			if r := recover(); r != nil {
				initCh <- errors.New("panic during initialization")
			} else {
				initCh <- err
			}
		}()

		registry := new(require.Registry)

		if b.enableNodejs {
			registry.Enable(vm)
			console.Enable(vm)
		}

		if b.enableFetch {
			client := b.fetchClient
			if client == nil {
				client = &http.Client{Timeout: 30 * time.Second}
			}
			if err = injectFetch(vm, loop, client); err != nil {
				return
			}
		}

		if b.enableXHR {
			client := b.xhrClient
			if client == nil {
				client = &http.Client{Timeout: 30 * time.Second}
			}
			if err = injectXHR(vm, loop, client); err != nil {
				return
			}
		}

		if b.kv != nil {
			// 这里直接调用注入逻辑，注意传递的是 vm
			b.injectMemoryKv(vm, b.kv)
		}

		for _, inj := range b.injectors {
			if err = inj(vm); err != nil {
				return // 发生错误， defer 会发送给 channel
			}
		}
	})

	// 4. 等待初始化结果
	if err := <-initCh; err != nil {
		loop.Stop()
		return nil, err
	}

	return &JsRuntime{
		loop: loop,
	}, nil
}

// injectMemoryKv 将 RamKv 注入到 goja.Runtime 中
func (b *Builder) injectMemoryKv(vm *goja.Runtime, kv *RamKv) {
	obj := vm.NewObject()

	obj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		if err := kv.Set(key, val); err != nil {
			return vm.NewGoError(err)
		}
		return goja.Undefined()
	})

	obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		defaultValue := call.Argument(1)
		val, exists := kv.Get(key)
		if !exists {
			if defaultValue != nil {
				return defaultValue
			}
			return goja.Undefined()
		}
		return vm.ToValue(val)
	})

	obj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		kv.Del(key)
		return goja.Undefined()
	})

	obj.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		return vm.ToValue(kv.Has(key))
	})

	vm.Set("kv", obj)
}
