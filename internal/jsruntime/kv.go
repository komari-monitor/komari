package jsruntime

import (
	"sync"

	"github.com/dop251/goja"
)

// RamKv 简单的内存 KV 存储
type RamKv struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

func NewRamKv() *RamKv {
	return &RamKv{
		data: make(map[string]interface{}),
	}
}

func Inject(vm *goja.Runtime, store *RamKv) *goja.Object {
	return buildStoreProxy(vm, store)
}

// buildStoreProxy 构建暴露给 JS 的 kv 对象
func buildStoreProxy(vm *goja.Runtime, store *RamKv) *goja.Object {
	obj := vm.NewObject()

	// kv.set(key, value)
	obj.Set("set", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		val := call.Argument(1).Export() // 将 JS 值转换为 Go 值存储

		store.mu.Lock()
		store.data[key] = val
		store.mu.Unlock()

		return goja.Undefined()
	})

	// kv.get(key, defaultValue)
	obj.Set("get", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()
		defaultValue := call.Argument(1) // 可选参数

		store.mu.RLock()
		val, exists := store.data[key]
		store.mu.RUnlock()

		if !exists {
			if defaultValue != nil {
				return defaultValue
			}
			return goja.Null()
		}
		return vm.ToValue(val)
	})

	// kv.del(key)
	obj.Set("del", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()

		store.mu.Lock()
		delete(store.data, key)
		store.mu.Unlock()

		return goja.Undefined()
	})

	// kv.has(key)
	obj.Set("has", func(call goja.FunctionCall) goja.Value {
		key := call.Argument(0).String()

		store.mu.RLock()
		_, exists := store.data[key]
		store.mu.RUnlock()

		return vm.ToValue(exists)
	})

	return obj
}
