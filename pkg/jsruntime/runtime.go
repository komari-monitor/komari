// Package jsruntime provides the JavaScript runtime used by server-side
// scripts.
//
// JavaScript compatibility: ECMAScript and Promise support provided by goja,
// CommonJS require and the event loop provided by goja_nodejs, and the host
// APIs assembled in globals.go. This is not a complete browser environment.
package jsruntime

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	_ "github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/dop251/goja_nodejs/require"
	_ "github.com/dop251/goja_nodejs/url"
	_ "github.com/dop251/goja_nodejs/util"
	childprocess "github.com/komari-monitor/komari/pkg/jsruntime/child_process"
	"github.com/komari-monitor/komari/pkg/jsruntime/console"
	cryptomodule "github.com/komari-monitor/komari/pkg/jsruntime/crypto"
	"github.com/komari-monitor/komari/pkg/jsruntime/fetch"
	"github.com/komari-monitor/komari/pkg/jsruntime/fs"
	httpmodule "github.com/komari-monitor/komari/pkg/jsruntime/http"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	netmodule "github.com/komari-monitor/komari/pkg/jsruntime/net"
	pathmodule "github.com/komari-monitor/komari/pkg/jsruntime/path"
	processmodule "github.com/komari-monitor/komari/pkg/jsruntime/process"
	"github.com/komari-monitor/komari/pkg/jsruntime/timers"
)

const defaultTimeout = 30 * time.Second

const (
	defaultMaxHTTPBodyBytes    int64 = 32 << 20
	defaultMaxChildOutputBytes       = 1 << 20
)

// Options controls the host capabilities exposed to a JavaScript runtime.
type Options struct {
	// HTTPClient is used by fetch and XMLHttpRequest. If nil, a client with
	// the configured timeout is created.
	HTTPClient *http.Client
	// Timeout limits a function call and defaults to 30 seconds.
	Timeout time.Duration
	// Console receives console.log and console.error output. If nil, the
	// application logger is used.
	Console io.Writer
	// RequireLoader loads CommonJS module source. If nil, files are loaded from
	// disk. Unless AllowAllFileAccess is true, every path passed to this loader
	// is confined to BaseDir, or the current directory when BaseDir is empty.
	RequireLoader require.SourceLoader
	// ConfigureRequire registers approved native modules on the runtime's
	// private CommonJS registry before the script is evaluated.
	ConfigureRequire func(*require.Registry)
	// ConfigureHost runs after the standard modules are registered and before
	// ConfigureRequire. It receives the runtime's host services and private
	// CommonJS registry so the host can register native modules (for example
	// a host-injected server object) and schedule JavaScript turns on the
	// event loop from other goroutines.
	ConfigureHost func(*Host, *require.Registry)
	// BaseDir is the root used to resolve relative CommonJS modules and
	// node_modules. The directory must exist. Module paths cannot escape it
	// unless AllowAllFileAccess is true. When BaseDir is empty, the current
	// directory is used.
	BaseDir string
	// NodeJS enables the Node.js compatibility modules. Filesystem operations
	// are confined to BaseDir, or the current directory when BaseDir is empty.
	NodeJS bool
	// AllowExec enables child_process. It has no effect unless NodeJS is true.
	AllowExec bool
	// AllowListen allows net and http servers to bind local ports. Outbound
	// client connections do not require this permission.
	AllowListen bool
	// AllowAllFileAccess allows require and fs paths to escape BaseDir. The
	// default confines both module and filesystem access to BaseDir.
	AllowAllFileAccess bool
	// ExtraRoots adds additional filesystem roots confined like BaseDir:
	// scripts may read and write under them, and paths cannot escape them
	// unless AllowAllFileAccess is true. Each root must exist and is resolved
	// with symlinks. Module (require) resolution stays confined to BaseDir.
	ExtraRoots []string
	// StorageDir is an additional confined filesystem root for long-term
	// data that survives plugin reinstallation. It must exist and is exposed
	// to scripts as the __storageDir__ global when NodeJS is enabled.
	StorageDir string
	// MaxHTTPBodyBytes limits buffered fetch responses and HTTP server request
	// bodies. Values less than one use a 32 MiB default.
	MaxHTTPBodyBytes int64
	// MaxChildOutputBytes limits each buffered stdout or stderr stream returned
	// by exec, execFile, and their synchronous variants. Values less than one
	// use Node.js' 1 MiB default.
	MaxChildOutputBytes int
}

// Runtime owns one isolated JavaScript VM and its event loop. Public
// operations are serialized, and all VM access runs on the event-loop
// goroutine because goja runtimes are not goroutine-safe.
type Runtime struct {
	mu                  sync.Mutex
	loop                *eventloop.EventLoop
	host                *bridge.Runtime
	vm                  *goja.Runtime
	timeout             time.Duration
	closed              bool
	nodeJS              bool
	maxHTTPBodyBytes    int64
	maxChildOutputBytes int
	storageDir          string
	consoleMod          *console.Module
	timersMod           *timers.Module
	fetchMod            *fetch.Module
	fsModule            *fs.Module
	pathModule          *pathmodule.Module
	processModule       *processmodule.Module
	childProcessModule  *childprocess.Module
	netModule           *netmodule.Module
	httpModule          *httpmodule.Module
	cryptoModule        *cryptomodule.Module
	resourceMu          sync.Mutex
	resourceID          uint64
	resources           map[uint64]func()
	resourcesClosed     bool
	fileMu              sync.Mutex
	fileID              int
	files               map[int]nodeFileHandle
}

// New creates and initializes a runtime from script.
func New(script string, options Options) (*Runtime, error) {
	if script == "" {
		return nil, errors.New("JavaScript script is empty")
	}

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	baseDir, err := resolveBaseDir(options.BaseDir)
	if err != nil {
		return nil, err
	}
	fileRoot := baseDir
	if fileRoot == "" {
		fileRoot, err = resolveBaseDir(".")
		if err != nil {
			return nil, fmt.Errorf("resolve JavaScript filesystem root: %w", err)
		}
	}
	extraRoots, storageDir, err := resolveExtraRoots(options.ExtraRoots, options.StorageDir)
	if err != nil {
		return nil, err
	}
	maxHTTPBodyBytes := options.MaxHTTPBodyBytes
	if maxHTTPBodyBytes < 1 {
		maxHTTPBodyBytes = defaultMaxHTTPBodyBytes
	}
	maxChildOutputBytes := options.MaxChildOutputBytes
	if maxChildOutputBytes < 1 {
		maxChildOutputBytes = defaultMaxChildOutputBytes
	}
	loader := options.RequireLoader
	if loader == nil {
		loader = require.DefaultSourceLoader
	}
	if fileRoot != "" && !options.AllowAllFileAccess {
		loader = confinedSourceLoader(fileRoot, loader)
	}
	registryOptions := make([]require.Option, 0, 3)
	if fileRoot != "" {
		registryOptions = append(registryOptions,
			require.WithPathResolver(baseDirPathResolver(fileRoot)),
			require.WithGlobalFolders(filepath.Join(fileRoot, "node_modules")),
		)
	}
	// Keep the loader indirection so the filesystem module can become the
	// canonical rooted loader after it owns the os.Root handle.
	var activeLoader require.SourceLoader = loader
	registryOptions = append(registryOptions, require.WithLoader(func(modulePath string) ([]byte, error) {
		return activeLoader(modulePath)
	}))
	registry := require.NewRegistry(registryOptions...)
	runtimeLoop := eventloop.NewEventLoop(
		eventloop.EnableConsole(false),
		eventloop.WithRegistry(registry),
	)
	host := bridge.New(runtimeLoop, timeout)
	publicHost := &Host{runtime: host}
	startedAt := time.Now()
	runtime := &Runtime{
		loop:                runtimeLoop,
		host:                host,
		timeout:             timeout,
		nodeJS:              options.NodeJS,
		maxHTTPBodyBytes:    maxHTTPBodyBytes,
		maxChildOutputBytes: maxChildOutputBytes,
		storageDir:          storageDir,
		resources:           make(map[uint64]func()),
		fileID:              2,
		files:               make(map[int]nodeFileHandle),
	}
	runtime.consoleMod = console.New(options.Console)
	runtime.timersMod = timers.New(host)
	runtime.fetchMod = fetch.New(host, client, maxHTTPBodyBytes)
	filesystem, fsErr := fs.New(host, fileRoot, fileRoot, extraRoots, options.AllowAllFileAccess)
	if fsErr != nil {
		runtimeLoop.Terminate()
		return nil, fsErr
	}
	runtime.fsModule = filesystem
	if options.RequireLoader == nil && fileRoot != "" && !options.AllowAllFileAccess {
		activeLoader = func(modulePath string) ([]byte, error) {
			data, readErr := filesystem.ReadSource(modulePath)
			if errors.Is(readErr, os.ErrNotExist) {
				return nil, require.ModuleFileDoesNotExistError
			}
			return data, readErr
		}
	}
	if options.NodeJS {
		runtime.pathModule = pathmodule.New(filesystem.Cwd)
		runtime.processModule = processmodule.New(host, filesystem, options.AllowExec, startedAt, func(vm *goja.Runtime, values []goja.Value) {
			runtime.consoleMod.WriteError(vm, values, false)
		})
		runtime.childProcessModule = childprocess.New(host, filesystem, options.AllowExec, maxChildOutputBytes)
		runtime.netModule = netmodule.New(host, options.AllowListen)
		runtime.httpModule = httpmodule.New(host, options.AllowListen, maxHTTPBodyBytes)
		runtime.cryptoModule = cryptomodule.New(host)
		runtime.registerNodeModules(registry)
	}
	host.SetTurnRunner(func(vm *goja.Runtime, job func() error) error {
		if runtime.processModule != nil {
			return runtime.processModule.RunTurn(vm, job)
		}
		return job()
	})
	host.SetErrorReporter(func(vm *goja.Runtime, name string, reportErr error) {
		runtime.consoleMod.Report(vm, name, reportErr)
	})
	if options.ConfigureHost != nil {
		options.ConfigureHost(publicHost, registry)
	}
	if options.ConfigureRequire != nil {
		options.ConfigureRequire(registry)
	}
	runtime.fsModule.SetFileHooks(func(fd int, file *os.File, resourceID uint64) {
		runtime.fileMu.Lock()
		runtime.fileID = max(runtime.fileID, fd)
		runtime.files[fd] = nodeFileHandle{file: file, resourceID: resourceID}
		runtime.fileMu.Unlock()
	}, func(fd int) {
		runtime.fileMu.Lock()
		delete(runtime.files, fd)
		runtime.fileMu.Unlock()
	})
	runtime.fsModule.SetExternalFileLookup(func(fd int) (*os.File, bool) {
		runtime.fileMu.Lock()
		handle, ok := runtime.files[fd]
		runtime.fileMu.Unlock()
		if !ok {
			return nil, false
		}
		return handle.file, true
	})
	runtime.loop.Start()

	initialized := make(chan error, 1)
	if !runtime.loop.RunOnLoop(func(vm *goja.Runtime) {
		runtime.vm = vm
		if err := runtime.injectGlobals(); err != nil {
			initialized <- err
			return
		}
		err := bridge.RunWithDeadline(vm, time.Now().Add(timeout), func() error {
			sourceName := "script.js"
			if baseDir != "" {
				sourceName = filepath.Join(baseDir, sourceName)
			}
			if _, err := vm.RunScript(sourceName, script); err != nil {
				return fmt.Errorf("failed to load JavaScript script: %v", err)
			}
			return nil
		})
		initialized <- err
	}) {
		runtime.closeHostResources()
		runtime.loop.Terminate()
		return nil, errors.New("JavaScript event loop is not running")
	}

	if err := <-initialized; err != nil {
		runtime.closeHostResources()
		runtime.loop.Terminate()
		return nil, err
	}
	return runtime, nil
}

func resolveBaseDir(path string) (string, error) {
	return resolveRoot("BaseDir", path)
}

func resolveRoot(label, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat JavaScript %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("JavaScript %s is not a directory: %s", label, resolved)
	}
	return filepath.Clean(resolved), nil
}

// resolveExtraRoots resolves the extra confined roots and the storage dir.
// The storage dir is appended to the roots so __storageDir__ access shares
// the same confinement.
func resolveExtraRoots(extraRoots []string, storageDir string) (roots []string, resolvedStorage string, err error) {
	if storageDir != "" {
		resolved, resolveErr := resolveRoot("storage dir", storageDir)
		if resolveErr != nil {
			return nil, "", resolveErr
		}
		resolvedStorage = resolved
		roots = append(roots, resolved)
	}
	for _, extra := range extraRoots {
		if extra == "" {
			continue
		}
		resolved, resolveErr := resolveRoot("extra root", extra)
		if resolveErr != nil {
			return nil, "", resolveErr
		}
		if resolved == resolvedStorage {
			continue
		}
		roots = append(roots, resolved)
	}
	return roots, resolvedStorage, nil
}

func baseDirPathResolver(baseDir string) require.PathResolver {
	return func(base, modulePath string) string {
		modulePath = filepath.FromSlash(modulePath)
		if filepath.IsAbs(modulePath) {
			return filepath.Clean(modulePath)
		}
		if base == "" || base == "." {
			return filepath.Join(baseDir, modulePath)
		}
		return filepath.Join(base, modulePath)
	}
}

func confinedSourceLoader(baseDir string, loader require.SourceLoader) require.SourceLoader {
	return func(modulePath string) ([]byte, error) {
		absolute, err := filepath.Abs(modulePath)
		if err != nil {
			return nil, fmt.Errorf("resolve JavaScript module path: %w", err)
		}
		absolute = filepath.Clean(absolute)
		if !pathWithinBaseDir(baseDir, absolute) {
			return nil, fmt.Errorf("JavaScript module path escapes BaseDir: %s", modulePath)
		}

		resolved, err := filepath.EvalSymlinks(absolute)
		if err == nil {
			resolved = filepath.Clean(resolved)
			if !pathWithinBaseDir(baseDir, resolved) {
				return nil, fmt.Errorf("JavaScript module symlink escapes BaseDir: %s", modulePath)
			}
			absolute = resolved
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("resolve JavaScript module symlinks: %w", err)
		}
		return loader(absolute)
	}
}

func pathWithinBaseDir(baseDir, path string) bool {
	relative, err := filepath.Rel(baseDir, path)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// Close stops the event loop and cancels its pending timers. A closed runtime
// cannot be called again.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.closeHostResources()
	r.loop.Terminate()
}

func (r *Runtime) closeHostResources() {
	if r.fetchMod != nil {
		r.fetchMod.Close()
	}
	if r.fsModule != nil {
		r.fsModule.Close()
	}
	if r.host != nil {
		r.host.CloseResources()
	}
	r.closeNodeResources()
}

// HasFunction reports whether name resolves to a callable JavaScript
// function.
func (r *Runtime) HasFunction(name string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}

	result := make(chan bool, 1)
	if !r.loop.RunOnLoop(func(vm *goja.Runtime) {
		value := vm.Get(name)
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			result <- false
			return
		}
		_, ok := goja.AssertFunction(value)
		result <- ok
	}) {
		return false
	}
	return <-result
}

// Call invokes a named JavaScript function and accepts either a truthy
// synchronous result or a Promise resolving to a truthy value.
func (r *Runtime) Call(name string, args ...any) error {
	return r.call(name, true, args...)
}

// CallVoid invokes a named JavaScript function and reports errors without
// requiring a truthy result. It is used for side-effect entry points such as
// plugin load()/unload() hooks.
func (r *Runtime) CallVoid(name string, args ...any) error {
	return r.call(name, false, args...)
}

func (r *Runtime) call(name string, requireTruthy bool, args ...any) error {
	if r == nil {
		return errors.New("JavaScript runtime is nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("JavaScript runtime is closed")
	}

	deadline := time.Now().Add(r.timeout)
	result := make(chan error, 1)
	var completed atomic.Bool
	finish := func(err error) {
		if completed.CompareAndSwap(false, true) {
			result <- err
		}
	}

	if !r.loop.RunOnLoop(func(vm *goja.Runtime) {
		if time.Now().After(deadline) {
			finish(bridge.ErrExecutionTimeout)
			return
		}
		r.callOnLoop(vm, name, args, deadline, finish, requireTruthy)
	}) {
		return errors.New("JavaScript event loop is not running")
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		finish(bridge.ErrExecutionTimeout)
		return fmt.Errorf("JavaScript execution timeout after %s", r.timeout)
	}
}

func (r *Runtime) callOnLoop(vm *goja.Runtime, name string, args []any, deadline time.Time, finish func(error), requireTruthy bool) {
	function := vm.Get(name)
	if function == nil || goja.IsUndefined(function) {
		finish(fmt.Errorf("%s function not defined in script", name))
		return
	}
	call, ok := goja.AssertFunction(function)
	if !ok {
		finish(fmt.Errorf("%s is not a function", name))
		return
	}

	values := make([]goja.Value, len(args))
	for i, arg := range args {
		values[i] = vm.ToValue(arg)
	}

	var callResult goja.Value
	err := bridge.RunWithDeadline(vm, deadline, func() error {
		return r.host.RunTurn(vm, func() error {
			var err error
			callResult, err = call(goja.Undefined(), values...)
			return err
		})
	})
	if err != nil {
		if errors.Is(err, bridge.ErrExecutionTimeout) {
			finish(bridge.ErrExecutionTimeout)
		} else if code, ok := bridge.ExitCodeFromError(err); ok {
			finish(exitCodeError(code))
		} else {
			finish(fmt.Errorf("JavaScript error: %v", err))
		}
		return
	}

	_, ok = callResult.Export().(*goja.Promise)
	if !ok {
		finish(okResult(name, requireTruthy, callResult))
		return
	}

	then, ok := goja.AssertFunction(callResult.ToObject(vm).Get("then"))
	if !ok {
		finish(errors.New("JavaScript Promise has no callable then method"))
		return
	}
	onFulfilled := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		finish(okResult(name, requireTruthy, call.Argument(0)))
		return goja.Undefined()
	})
	onRejected := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if code, ok := bridge.ExitCodeFromValue(call.Argument(0)); ok {
			finish(exitCodeError(code))
			return goja.Undefined()
		}
		finish(fmt.Errorf("Promise rejected: %v", call.Argument(0)))
		return goja.Undefined()
	})
	if _, err := then(callResult, onFulfilled, onRejected); err != nil {
		finish(fmt.Errorf("failed to observe Promise: %v", err))
		return
	}
}

// exitCodeError converts a script-requested process exit into a result.
// Exit code 0 is a normal termination; nonzero codes are failures.
func exitCodeError(code int64) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("JavaScript process exited with code %d", code)
}

func okResult(name string, requireTruthy bool, value goja.Value) error {
	if !requireTruthy {
		return nil
	}
	return truthyResult(name, value)
}

func truthyResult(name string, value goja.Value) error {
	if value != nil && value.ToBoolean() {
		return nil
	}
	return fmt.Errorf("%s returned false", name)
}
