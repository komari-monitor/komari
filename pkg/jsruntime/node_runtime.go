package jsruntime

import (
	"fmt"
	stdos "os"
	"path/filepath"

	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/pkg/jsruntime/events"
	osmodule "github.com/komari-monitor/komari/pkg/jsruntime/os"
	streammodule "github.com/komari-monitor/komari/pkg/jsruntime/stream"
)

type nodeFileHandle struct {
	file       *stdos.File
	resourceID uint64
}

func (r *Runtime) registerNodeModules(registry *require.Registry) {
	r.registerNodeModule(registry, "events", events.Load)
	r.registerNodeModule(registry, "path", r.pathModule.Load)
	r.registerNodeModule(registry, "os", osmodule.Load)
	r.registerNodeModule(registry, "process", r.processModule.Load)
	r.registerNodeModule(registry, "fs", r.fsModule.Load)
	r.registerNodeModule(registry, "child_process", r.childProcessModule.Load)
	r.registerNodeModule(registry, "net", r.netModule.Load)
	r.registerNodeModule(registry, "http", r.httpModule.Load)
	r.registerNodeModule(registry, "stream", streammodule.Load)
	r.registerNodeModule(registry, "stream/promises", streammodule.LoadPromises)
	r.registerNodeModule(registry, "crypto", r.cryptoModule.Load)
}

func (r *Runtime) registerNodeModule(registry *require.Registry, name string, loader require.ModuleLoader) {
	registry.RegisterNativeModule(name, loader)
	registry.RegisterNativeModule("node:"+name, loader)
}

func (r *Runtime) injectNodeGlobals() error {
	buffer.Enable(r.vm)
	processValue := require.Require(r.vm, "process")
	if err := r.vm.Set("process", processValue); err != nil {
		return fmt.Errorf("inject process: %w", err)
	}
	if err := r.vm.Set("global", r.vm.GlobalObject()); err != nil {
		return fmt.Errorf("inject global: %w", err)
	}
	cwd := r.fsModule.Cwd()
	if err := r.vm.Set("__dirname", cwd); err != nil {
		return fmt.Errorf("inject __dirname: %w", err)
	}
	if err := r.vm.Set("__filename", filepath.Join(cwd, "script.js")); err != nil {
		return fmt.Errorf("inject __filename: %w", err)
	}
	if r.storageDir != "" {
		if err := r.vm.Set("__storageDir__", r.storageDir); err != nil {
			return fmt.Errorf("inject __storageDir__: %w", err)
		}
	}
	return nil
}

func (r *Runtime) addNodeResource(close func()) uint64 {
	r.resourceMu.Lock()
	if r.resourcesClosed {
		r.resourceMu.Unlock()
		if close != nil {
			close()
		}
		return 0
	}
	r.resourceID++
	id := r.resourceID
	r.resources[id] = close
	r.resourceMu.Unlock()
	return id
}

func (r *Runtime) nodeResourcesOpen() bool {
	r.resourceMu.Lock()
	open := !r.resourcesClosed
	r.resourceMu.Unlock()
	return open
}

func (r *Runtime) removeNodeResource(id uint64) {
	if id == 0 {
		return
	}
	r.resourceMu.Lock()
	delete(r.resources, id)
	r.resourceMu.Unlock()
}

func (r *Runtime) closeNodeResources() {
	r.resourceMu.Lock()
	if r.resourcesClosed {
		r.resourceMu.Unlock()
		return
	}
	r.resourcesClosed = true
	resources := make([]func(), 0, len(r.resources))
	for id, close := range r.resources {
		resources = append(resources, close)
		delete(r.resources, id)
	}
	r.resourceMu.Unlock()
	for _, close := range resources {
		if close != nil {
			close()
		}
	}
	r.fileMu.Lock()
	clear(r.files)
	r.fileMu.Unlock()
}

func (r *Runtime) resolveNodePath(name string, allowMissing bool) (string, error) {
	if r.fsModule == nil {
		return "", fmt.Errorf("filesystem module is not initialized")
	}
	return r.fsModule.Resolve(name, allowMissing)
}

func (r *Runtime) nodeWriteFile(path string, data []byte, mode stdos.FileMode) error {
	if r.fsModule == nil {
		return fmt.Errorf("filesystem module is not initialized")
	}
	return r.fsModule.WriteResolved(path, data, mode)
}
