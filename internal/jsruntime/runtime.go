package jsruntime

import (
	"sync"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/require"
)

// JsRuntime 封装了 JS 虚拟机环境
type JsRuntime struct {
	vm       *goja.Runtime
	mu       sync.Mutex // Goja 不是线程安全的
	registry *require.Registry
}

func init() {

}

// NewJsRuntime 初始化运行时
func NewJsRuntime() *JsRuntime {
	registry := new(require.Registry)
	vm := goja.New()

	r := &JsRuntime{
		vm:       vm,
		registry: registry,
	}

	return r
}

// 启用 Node.js 支持
func (r *JsRuntime) WithNodejs() *JsRuntime {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.registry.Enable(r.vm)
	console.Enable(r.vm)
	return r
}

// 注入内存 KV 存储
func (r *JsRuntime) WithMemoryKv(name string) *JsRuntime {
	if name == "" {
		name = "kv"
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	kvStore := NewRamKv()
	kvObj := Inject(r.vm, kvStore)
	r.vm.Set(name, kvObj)
	return r
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
