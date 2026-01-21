package app

import (
	"fmt"
	"sync"
)

var (
	factoryMu sync.RWMutex
	factories = make(map[string]func() Module)
)

// RegisterModuleFactory registers a factory used by App to auto-materialize missing dependencies.
// Intended for core modules (config/db) and plugin-provided modules.
func RegisterModuleFactory(name string, factory func() Module) {
	if name == "" || factory == nil {
		return
	}
	factoryMu.Lock()
	factories[name] = factory
	factoryMu.Unlock()
}

func getModuleFactory(name string) (func() Module, bool) {
	factoryMu.RLock()
	f, ok := factories[name]
	factoryMu.RUnlock()
	return f, ok
}

func (a *App) ensureDeps(mod Module, visiting map[string]bool) {
	if a.regErr != nil {
		return
	}
	if mod == nil {
		a.regErr = fmt.Errorf("module is nil")
		return
	}

	name := mod.Name()
	if visiting[name] {
		a.regErr = fmt.Errorf("dependency cycle while materializing: %s", name)
		return
	}
	visiting[name] = true
	defer func() { visiting[name] = false }()

	for _, dep := range mod.Depends() {
		if dep == "" {
			continue
		}
		if a.has(dep) {
			continue
		}
		f, ok := getModuleFactory(dep)
		if !ok {
			a.regErr = fmt.Errorf("missing module %q (no factory registered)", dep)
			return
		}
		depMod := f()
		if depMod == nil {
			a.regErr = fmt.Errorf("module factory for %q returned nil", dep)
			return
		}
		// Register dependency module and its dependencies recursively.
		a.regErr = a.register(depMod)
		if a.regErr != nil {
			return
		}
		a.moduleList = append(a.moduleList, depMod)
		a.ensureDeps(depMod, visiting)
		if a.regErr != nil {
			return
		}
	}
}
