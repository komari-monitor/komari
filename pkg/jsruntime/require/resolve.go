package require

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	n "github.com/komari-monitor/komari/pkg/jsruntime/native"
)

const NodePrefix = "node:"

func (r *RequireModule) resolvePath(base, name string) string {
	if r.r.pathResolver != nil {
		return r.r.pathResolver(base, name)
	}
	return DefaultPathResolver(base, name)
}

// NodeJS module search algorithm described by
// https://nodejs.org/api/modules.html#modules_all_together
func (r *RequireModule) resolve(modpath string) (*engine.Object, error) {
	return r.resolveFrom(modpath, r.getCurrentModulePath())
}
func (r *RequireModule) resolveFrom(modpath, start string) (module *engine.Object, err error) {
	if filepath.IsAbs(modpath) {
		start = ""
	}

	p := r.resolvePath(start, modpath)
	if isFileOrDirectoryPath(modpath) {
		if module = r.modules[p]; module != nil {
			return
		}
		module, err = r.loadAsFileOrDirectory(p)
		if err == nil && module != nil {
			r.modules[p] = module
		}
	} else {
		module, err = r.loadNative(modpath)
		if err == nil {
			return
		} else {
			if err == InvalidModuleError {
				err = nil
			} else {
				return
			}
		}
		if module = r.nodeModules[p]; module != nil {
			return
		}
		module, err = r.loadNodeModules(modpath, start)
		if err == nil && module != nil {
			r.nodeModules[p] = module
		}
	}

	if module == nil && err == nil {
		err = InvalidModuleError
	}
	return
}

func (r *RequireModule) loadNative(path string) (*engine.Object, error) {
	module := r.modules[path]
	if module != nil {
		return module, nil
	}

	var ldr ModuleLoader
	if ldr = r.r.native[path]; ldr == nil {
		ldr = native[path]
	}

	var isBuiltIn, withPrefix bool
	if ldr == nil {
		ldr = builtin[path]
		if ldr == nil && strings.HasPrefix(path, NodePrefix) {
			ldr = builtin[path[len(NodePrefix):]]
			if ldr == nil {
				return nil, NoSuchBuiltInModuleError
			}
			withPrefix = true
		}
		isBuiltIn = true
	}

	if ldr != nil {
		module = r.createModuleObject()
		r.modules[path] = module
		if isBuiltIn {
			if withPrefix {
				r.modules[path[len(NodePrefix):]] = module
			} else {
				if !strings.HasPrefix(path, NodePrefix) {
					r.modules[NodePrefix+path] = module
				}
			}
		}
		ldr(r.runtime, module)
		return module, nil
	}

	return nil, InvalidModuleError
}

func (r *RequireModule) loadAsFileOrDirectory(path string) (module *engine.Object, err error) {
	if module, err = r.loadAsFile(path); module != nil || err != nil {
		return
	}

	return r.loadAsDirectory(path)
}

func (r *RequireModule) loadAsFile(path string) (module *engine.Object, err error) {
	if module, err = r.loadModule(path); module != nil || err != nil {
		return
	}

	p := path + ".js"
	if module, err = r.loadModule(p); module != nil || err != nil {
		return
	}

	p = path + ".json"
	return r.loadModule(p)
}

func (r *RequireModule) loadIndex(modpath string) (module *engine.Object, err error) {
	p := r.resolvePath(modpath, "index.js")
	if module, err = r.loadModule(p); module != nil || err != nil {
		return
	}

	p = r.resolvePath(modpath, "index.json")
	return r.loadModule(p)
}

func (r *RequireModule) loadAsDirectory(modpath string) (module *engine.Object, err error) {
	p := r.resolvePath(modpath, "package.json")
	buf, err := r.r.getSource(p)
	if err != nil {
		if !errors.Is(err, ModuleFileDoesNotExistError) {
			return nil, err
		}
		return r.loadIndex(modpath)
	}
	var pkg struct {
		Main string
	}
	err = json.Unmarshal(buf, &pkg)
	if err != nil || len(pkg.Main) == 0 {
		return r.loadIndex(modpath)
	}

	m := r.resolvePath(modpath, pkg.Main)
	if module, err = r.loadAsFile(m); module != nil || err != nil {
		return
	}

	return r.loadIndex(m)
}

func (r *RequireModule) loadNodeModule(modpath, start string) (*engine.Object, error) {
	return r.loadAsFileOrDirectory(r.resolvePath(start, modpath))
}

func (r *RequireModule) loadNodeModules(modpath, start string) (module *engine.Object, err error) {
	for _, dir := range r.r.globalFolders {
		if module, err = r.loadNodeModule(modpath, dir); module != nil || err != nil {
			return
		}
	}
	for {
		var p string
		if filepath.Base(start) != "node_modules" {
			p = filepath.Join(start, "node_modules")
		} else {
			p = start
		}
		if module, err = r.loadNodeModule(modpath, p); module != nil || err != nil {
			return
		}
		if start == ".." { // Dir('..') is '.'
			break
		}
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}

	return
}

func (r *RequireModule) getCurrentModulePath() string {
	// Capture native engine frames, not a textual stack trace. The first bytecode
	// caller is the script that requested this module (native frames have no code).
	frames := r.runtime.Realm().NewError(engine.KindError, "").ErrorData().Frames()
	for _, f := range frames {
		if code := f.Code(); code != nil && code.Source != nil && code.Source.Name != "" {
			return filepath.Dir(code.Source.Name)
		}
	}
	return "."
}

func (r *RequireModule) createModuleObject() *engine.Object {
	module := r.runtime.Realm().NewObject()
	if err := n.Set(r.runtime, module, "exports", r.runtime.Realm().NewObject()); err != nil {
		panic(err)
	}
	return module
}

func (r *RequireModule) loadModule(path string) (*engine.Object, error) {
	module := r.modules[path]
	if module == nil {
		module = r.createModuleObject()
		r.modules[path] = module
		err := r.loadModuleFile(path, module)
		if err != nil {
			module = nil
			delete(r.modules, path)
			if errors.Is(err, ModuleFileDoesNotExistError) {
				err = nil
			}
		}
		return module, err
	}
	return module, nil
}

func (r *RequireModule) loadModuleFile(path string, module *engine.Object) error {
	script, err := r.r.getCompiledSource(path)
	if err != nil {
		return err
	}
	fn, err := r.runtime.RunScript(script)
	if err != nil {
		return err
	}
	if !engine.IsCallable(fn) {
		return InvalidModuleError
	}
	exports, err := r.runtime.Get(engine.ObjectValue(module), "exports")
	if err != nil {
		return err
	}
	// Each module gets a lexical require rooted at its own filename. This also
	// works when an exported function invokes require after initialization.
	req := n.Bind(r.runtime, "require", 1, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := n.String(r.runtime, moejs.Arg(args, 0))
		return r.requireFrom(name, filepath.Dir(path))
	})
	_, err = r.runtime.CallFunction(fn, exports, exports, req, engine.ObjectValue(module), n.From(r.runtime, path), n.From(r.runtime, filepath.Dir(path)))
	return err
}

func (r *RequireModule) requireFrom(name, base string) (moejs.Value, error) {
	module, err := r.resolveFrom(name, base)
	if err != nil {
		return moejs.Undefined(), err
	}
	return r.runtime.Get(engine.ObjectValue(module), "exports")
}

func isFileOrDirectoryPath(path string) bool {
	result := path == "." || path == ".." ||
		strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "./") ||
		strings.HasPrefix(path, "../")

	if runtime.GOOS == "windows" {
		result = result ||
			strings.HasPrefix(path, `.\`) ||
			strings.HasPrefix(path, `..\`) ||
			filepath.IsAbs(path)
	}

	return result
}
