package jsruntime

import (
	"sync"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"
)

// Builder 构建 JsRuntime 的配置入口
type Builder struct {
	enableNodejs bool
	kv           *RamKv
	injectors    []Injector
}

// Injector 允许在构建时注入自定义能力
type Injector func(r *JsRuntime) error

// JsRuntime 封装了 JS 虚拟机环境
type JsRuntime struct {
	vm       *goja.Runtime
	mu       sync.Mutex // Goja 不是线程安全的
	registry *require.Registry
}

// NewBuilder 返回 JsRuntime 的 builder
func NewBuilder() *Builder {
	return &Builder{}
}

// WithNodejs 启用 Node.js 风格的模块和 console 支持
func (b *Builder) WithNodejs() *Builder {
	b.enableNodejs = true
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

// WithInjector 注册自定义注入函数，在 Build 时依次执行
func (b *Builder) WithInjector(inj Injector) *Builder {
	if inj != nil {
		b.injectors = append(b.injectors, inj)
	}
	return b
}

// Build 构建并返回 JsRuntime，同时返回可能的错误
func (b *Builder) Build() (*JsRuntime, error) {
	registry := new(require.Registry)
	vm := goja.New()

	r := &JsRuntime{
		vm:       vm,
		registry: registry,
	}

	if b.enableNodejs {
		b.enableNodeSupport(r)
	}

	if b.kv != nil {
		b.injectMemoryKv(r, b.kv)
	}

	for _, inj := range b.injectors {
		if err := inj(r); err != nil {
			return nil, err
		}
	}

	return r, nil
}

func (b *Builder) enableNodeSupport(r *JsRuntime) {
	r.registry.Enable(r.vm)
	console.Enable(r.vm)
}

func (b *Builder) injectMemoryKv(r *JsRuntime, kv *RamKv) {
	obj := r.vm.NewObject()

	// kv.set(key, value)
	obj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export()
		if err := kv.Set(key, val); err != nil {
			return r.vm.NewGoError(err)
		}
		return goja.Undefined()
	})

	// kv.get(key, defaultValue)
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
		return r.vm.ToValue(val)
	})

	// kv.del(key)
	obj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		kv.Del(key)
		return goja.Undefined()
	})

	// kv.has(key)
	obj.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		return r.vm.ToValue(kv.Has(key))
	})
}

func (r *JsRuntime) GetVM() *goja.Runtime {
	return r.vm
}

func (r *JsRuntime) RunScript(script string) (goja.Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	v, err := r.vm.RunString(script)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (r *JsRuntime) Call(functicon string, params ...any) (goja.Value, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fn, ok := goja.AssertFunction(r.vm.Get(functicon))
	if !ok {
		return nil, nil
	}
	vals := make([]goja.Value, len(params))
	for i, p := range params {
		vals[i] = r.vm.ToValue(p)
	}
	return fn(goja.Undefined(), vals...)
}

func (r *JsRuntime) HasFunction(functicon string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.vm.Get(functicon) != nil
}
