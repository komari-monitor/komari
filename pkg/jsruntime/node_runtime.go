package jsruntime

import (
	"fmt"
	"path/filepath"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
)

func newEventEmitter(vm *goja.Runtime) *goja.Object {
	constructor := require.Require(vm, "events")
	object, err := vm.New(constructor)
	if err != nil {
		panic(err)
	}
	return object
}

func emitEvent(vm *goja.Runtime, emitter *goja.Object, name string, values ...any) error {
	emit, ok := goja.AssertFunction(emitter.Get("emit"))
	if !ok {
		return fmt.Errorf("EventEmitter.emit is not callable")
	}
	arguments := make([]goja.Value, 1, len(values)+1)
	arguments[0] = vm.ToValue(name)
	for _, value := range values {
		if jsValue, ok := value.(goja.Value); ok {
			arguments = append(arguments, jsValue)
		} else {
			arguments = append(arguments, vm.ToValue(value))
		}
	}
	_, err := emit(emitter, arguments...)
	return err
}

func (r *Runtime) registerNodeModules(registry *require.Registry) {
	r.registerNodeModule(registry, "events", r.loadEventsModule)
	r.registerNodeModule(registry, "path", r.loadPathModule)
	r.registerNodeModule(registry, "os", r.loadOSModule)
	r.registerNodeModule(registry, "process", r.loadProcessModule)
	r.registerNodeModule(registry, "fs", r.loadFSModule)
	r.registerNodeModule(registry, "child_process", r.loadChildProcessModule)
	r.registerNodeModule(registry, "net", r.loadNetModule)
	r.registerNodeModule(registry, "http", r.loadHTTPModule)
}

func (r *Runtime) registerNodeModule(registry *require.Registry, name string, loader require.ModuleLoader) {
	registry.RegisterNativeModule(name, loader)
	registry.RegisterNativeModule("node:"+name, loader)
}

func (r *Runtime) injectNodeGlobals() error {
	buffer.Enable(r.vm)
	process := require.Require(r.vm, "process")
	if err := r.vm.Set("process", process); err != nil {
		return fmt.Errorf("inject process: %w", err)
	}
	if err := r.vm.Set("global", r.vm.GlobalObject()); err != nil {
		return fmt.Errorf("inject global: %w", err)
	}
	if err := r.vm.Set("__dirname", r.nodeCwd); err != nil {
		return fmt.Errorf("inject __dirname: %w", err)
	}
	if err := r.vm.Set("__filename", filepath.Join(r.nodeCwd, "script.js")); err != nil {
		return fmt.Errorf("inject __filename: %w", err)
	}
	return nil
}

func (r *Runtime) addNodeResource(close func()) uint64 {
	r.resourceMu.Lock()
	if r.resourcesClosed {
		r.resourceMu.Unlock()
		close()
		return 0
	}
	r.resourceID++
	id := r.resourceID
	r.resources[id] = close
	r.resourceMu.Unlock()
	return id
}

func (r *Runtime) removeNodeResource(id uint64) {
	r.resourceMu.Lock()
	delete(r.resources, id)
	r.resourceMu.Unlock()
}

func (r *Runtime) closeNodeResources() {
	r.resourceMu.Lock()
	r.resourcesClosed = true
	resources := make([]func(), 0, len(r.resources))
	for id, close := range r.resources {
		resources = append(resources, close)
		delete(r.resources, id)
	}
	r.resourceMu.Unlock()
	for _, close := range resources {
		close()
	}
}
