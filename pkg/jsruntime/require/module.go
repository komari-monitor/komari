package require

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"text/template"

	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	n "github.com/komari-monitor/komari/pkg/jsruntime/native"
)

type ModuleLoader func(*moejs.Runtime, *engine.Object)

// SourceLoader represents a function that returns a file data at a given path.
// The function should return ModuleFileDoesNotExistError if the file either doesn't exist or is a directory.
// This error will be ignored by the resolver and the search will continue. Any other errors will be propagated.
type SourceLoader func(path string) ([]byte, error)

// PathResolver is a function that should return a canonical path of the path parameter relative to the base. The base
// is expected to be already canonical as it would be a result of a previous call to the PathResolver for all cases
// except for the initial evaluation, but it's a responsibility of the caller to ensure that the name of the script
// is a canonical path. To match Node JS behaviour, it should resolve symlinks.
// The path parameter is the argument of the require() call. The returned value will be supplied to the SourceLoader.
type PathResolver func(base, path string) string

var (
	InvalidModuleError          = errors.New("Invalid module")
	IllegalModuleNameError      = errors.New("Illegal module name")
	NoSuchBuiltInModuleError    = errors.New("No such built-in module")
	ModuleFileDoesNotExistError = errors.New("module file does not exist")
)

var native, builtin map[string]ModuleLoader

// Registry contains a cache of compiled modules which can be used by multiple Runtimes
type Registry struct {
	sync.Mutex
	native   map[string]ModuleLoader
	compiled map[string]*moejs.Script

	srcLoader     SourceLoader
	pathResolver  PathResolver
	globalFolders []string
}

type RequireModule struct {
	r           *Registry
	runtime     *moejs.Runtime
	modules     map[string]*engine.Object
	nodeModules map[string]*engine.Object
}

func NewRegistry(opts ...Option) *Registry {
	r := &Registry{}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

func NewRegistryWithLoader(srcLoader SourceLoader) *Registry {
	return NewRegistry(WithLoader(srcLoader))
}

type Option func(*Registry)

// WithLoader sets a function which will be called by the require() function in order to get a source code for a
// module at the given path. Unlike the upstream goja_nodejs implementation,
// the moejs-backed runtime does not use this loader for external source maps.
// This only affects modules loaded by require(); it does not change runtime
// parser settings or relax the caller-provided module path restrictions.
func WithLoader(srcLoader SourceLoader) Option {
	return func(r *Registry) {
		r.srcLoader = srcLoader
	}
}

// WithPathResolver sets a function which will be used to resolve paths (see PathResolver). If not specified, the
// DefaultPathResolver is used.
func WithPathResolver(pathResolver PathResolver) Option {
	return func(r *Registry) {
		r.pathResolver = pathResolver
	}
}

// WithGlobalFolders appends the given paths to the registry's list of
// global folders to search if the requested module is not found
// elsewhere.  By default, a registry's global folders list is empty.
// In the reference Node.js implementation, the default global folders
// list is $NODE_PATH, $HOME/.node_modules, $HOME/.node_libraries and
// $PREFIX/lib/node, see
// https://nodejs.org/api/modules.html#modules_loading_from_the_global_folders.
func WithGlobalFolders(globalFolders ...string) Option {
	return func(r *Registry) {
		r.globalFolders = globalFolders
	}
}

// Enable adds the require() function to the specified runtime.
func (r *Registry) Enable(runtime *moejs.Runtime) *RequireModule {
	rrt := &RequireModule{
		r:           r,
		runtime:     runtime,
		modules:     make(map[string]*engine.Object),
		nodeModules: make(map[string]*engine.Object),
	}

	if err := runtime.SetGlobal("require", n.Bind(runtime, "require", 1, rrt.require)); err != nil {
		panic(err)
	}
	return rrt
}

func (r *Registry) RegisterNativeModule(name string, loader ModuleLoader) {
	r.Lock()
	defer r.Unlock()

	if r.native == nil {
		r.native = make(map[string]ModuleLoader)
	}
	name = filepathClean(name)
	r.native[name] = loader
}

// DefaultSourceLoader is used if none was set (see WithLoader()). It simply loads files from the host's filesystem.
func DefaultSourceLoader(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = ModuleFileDoesNotExistError
		} else if runtime.GOOS == "windows" {
			if errors.Is(err, syscall.Errno(0x7b)) { // ERROR_INVALID_NAME, The filename, directory name, or volume label syntax is incorrect.
				err = ModuleFileDoesNotExistError
			}
		}
		return nil, err
	}

	defer f.Close()
	// On some systems (e.g. plan9 and FreeBSD) it is possible to use the standard read() call on directories
	// which means we cannot rely on read() returning an error, we have to do stat() instead.
	if fi, err := f.Stat(); err == nil {
		if fi.IsDir() {
			return nil, ModuleFileDoesNotExistError
		}
	} else {
		return nil, err
	}
	return io.ReadAll(f)
}

// DefaultPathResolver is used if none was set (see WithPathResolver). It converts the path using filepath.FromSlash(),
// then joins it with base and resolves symlinks on the resulting path.
// Note, it does not make the path absolute, so to match nodejs behaviour, the initial script name should be set
// to an absolute path.
// The implementation is somewhat suboptimal because it runs filepath.EvalSymlinks() on the joint path, not using the
// fact that the base path is already resolved. This is because there is no way to resolve symlinks only in a portion
// of a path without re-implementing a significant part of filepath.FromSlash().
func DefaultPathResolver(base, path string) string {
	p := filepath.Join(base, filepath.FromSlash(path))
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p
}

func (r *Registry) getSource(p string) ([]byte, error) {
	srcLoader := r.srcLoader
	if srcLoader == nil {
		srcLoader = DefaultSourceLoader
	}
	return srcLoader(p)
}

func (r *Registry) getCompiledSource(p string) (*moejs.Script, error) {
	r.Lock()
	defer r.Unlock()

	prg := r.compiled[p]
	if prg == nil {
		buf, err := r.getSource(p)
		if err != nil {
			return nil, err
		}
		s := string(buf)

		if filepath.Ext(p) == ".json" {
			s = "module.exports = JSON.parse('" + template.JSEscapeString(s) + "')"
		}

		source := "(function(exports,require,module,__filename,__dirname){" + s + "\n})"
		// Native MoeJS compiles source directly; custom source-map loading
		// via WithLoader is not supported by this runtime. Module source loading
		// and the caller-provided path resolver remain unchanged.
		prg, err = moejs.CompileScript(p, source)
		if err == nil {
			if r.compiled == nil {
				r.compiled = make(map[string]*moejs.Script)
			}
			r.compiled[p] = prg
		}
		return prg, err
	}
	return prg, nil
}

func (r *RequireModule) require(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
	return r.Require(n.String(r.runtime, moejs.Arg(args, 0)))
}

func filepathClean(p string) string {
	return path.Clean(p)
}

// Require can be used to import modules from Go source (similar to JS require() function).
func (r *RequireModule) Require(p string) (ret moejs.Value, err error) {
	module, err := r.resolve(p)
	if err != nil {
		return
	}
	ret, err = r.runtime.Get(engine.ObjectValue(module), "exports")
	return
}

func Require(runtime *moejs.Runtime, name string) moejs.Value {
	v, err := runtime.CallFunction(n.Global(runtime, "require"), moejs.Undefined(), n.From(runtime, name))
	if err != nil {
		panic(err)
	}
	return v
}

// RegisterNativeModule registers a module that isn't loaded through a SourceLoader, but rather through
// a provided ModuleLoader. Typically, this will be a module implemented in Go (although theoretically
// it can be anything, depending on the ModuleLoader implementation).
// Such modules take precedence over modules loaded through a SourceLoader, i.e. if a module name resolves as
// native, the native module is loaded, and the SourceLoader is not consulted.
// The binding is global and affects all instances of Registry.
// It should be called from a package init() function as it may not be used concurrently with require() calls.
// For registry-specific bindings see Registry.RegisterNativeModule.
func RegisterNativeModule(name string, loader ModuleLoader) {
	if native == nil {
		native = make(map[string]ModuleLoader)
	}
	name = filepathClean(name)
	native[name] = loader
}

// RegisterCoreModule registers a nodejs core module. If the name does not start with "node:", the module
// will also be loadable as "node:<name>". Hence, for "builtin" modules (such as buffer, console, etc.)
// the name should not include the "node:" prefix, but for prefix-only core modules (such as "node:test")
// it should include the prefix.
func RegisterCoreModule(name string, loader ModuleLoader) {
	if builtin == nil {
		builtin = make(map[string]ModuleLoader)
	}
	name = filepathClean(name)
	builtin[name] = loader
}
