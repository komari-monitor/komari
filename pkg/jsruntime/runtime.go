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
)

const defaultTimeout = 30 * time.Second

var errExecutionTimeout = errors.New("JavaScript execution timeout")

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
	// is confined to BaseDir, or the current directory in NodeJS mode.
	RequireLoader require.SourceLoader
	// ConfigureRequire registers approved native modules on the runtime's
	// private CommonJS registry before the script is evaluated.
	ConfigureRequire func(*require.Registry)
	// BaseDir is the root used to resolve relative CommonJS modules and
	// node_modules. The directory must exist. Module paths cannot escape it
	// unless AllowAllFileAccess is true.
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
}

// Runtime owns one isolated JavaScript VM and its event loop. Public
// operations are serialized, and all VM access runs on the event-loop
// goroutine because goja runtimes are not goroutine-safe.
type Runtime struct {
	mu                 sync.Mutex
	loop               *eventloop.EventLoop
	vm                 *goja.Runtime
	httpClient         *http.Client
	timeout            time.Duration
	console            io.Writer
	closed             bool
	abortMu            sync.Mutex
	abortIDs           int64
	abortState         map[int64]*abortSignalState
	nodeJS             bool
	allowExec          bool
	allowListen        bool
	allowAllFileAccess bool
	nodeRoot           string
	nodeCwd            string
	resourceMu         sync.Mutex
	resourceID         uint64
	resources          map[uint64]func()
	resourcesClosed    bool
	startedAt          time.Time
	fileMu             sync.Mutex
	fileID             int
	files              map[int]nodeFileHandle
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
	if options.NodeJS && fileRoot == "" {
		fileRoot, err = resolveBaseDir(".")
		if err != nil {
			return nil, fmt.Errorf("resolve Node.js filesystem root: %w", err)
		}
	}
	registryOptions := make([]require.Option, 0, 3)
	loader := options.RequireLoader
	if loader == nil {
		loader = require.DefaultSourceLoader
	}
	if fileRoot != "" {
		if !options.AllowAllFileAccess {
			loader = confinedSourceLoader(fileRoot, loader)
		}
		registryOptions = append(registryOptions,
			require.WithPathResolver(baseDirPathResolver(fileRoot)),
			require.WithGlobalFolders(filepath.Join(fileRoot, "node_modules")),
		)
	}
	registryOptions = append(registryOptions, require.WithLoader(loader))
	registry := require.NewRegistry(registryOptions...)
	runtime := &Runtime{
		httpClient:         client,
		timeout:            timeout,
		console:            options.Console,
		abortState:         make(map[int64]*abortSignalState),
		nodeJS:             options.NodeJS,
		allowExec:          options.AllowExec,
		allowListen:        options.AllowListen,
		allowAllFileAccess: options.AllowAllFileAccess,
		resources:          make(map[uint64]func()),
		startedAt:          time.Now(),
		fileID:             2,
		files:              make(map[int]nodeFileHandle),
	}
	if options.NodeJS {
		runtime.nodeRoot = fileRoot
		runtime.nodeCwd = fileRoot
		runtime.registerNodeModules(registry)
	}
	if options.ConfigureRequire != nil {
		options.ConfigureRequire(registry)
	}
	runtime.loop = eventloop.NewEventLoop(
		eventloop.EnableConsole(false),
		eventloop.WithRegistry(registry),
	)
	runtime.loop.Start()

	initialized := make(chan error, 1)
	deadline := time.Now().Add(timeout)
	if !runtime.loop.RunOnLoop(func(vm *goja.Runtime) {
		runtime.vm = vm
		err := runWithDeadline(vm, deadline, func() error {
			if err := runtime.injectGlobals(); err != nil {
				return err
			}
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
		runtime.loop.Terminate()
		return nil, errors.New("JavaScript event loop is not running")
	}

	if err := <-initialized; err != nil {
		runtime.loop.Terminate()
		return nil, err
	}
	return runtime, nil
}

func resolveBaseDir(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript BaseDir: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve JavaScript BaseDir: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat JavaScript BaseDir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("JavaScript BaseDir is not a directory: %s", resolved)
	}
	return filepath.Clean(resolved), nil
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
	r.cancelHTTPRequests()
	r.closeNodeResources()
	r.loop.Terminate()
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
			finish(errExecutionTimeout)
			return
		}
		r.callOnLoop(vm, name, args, deadline, finish)
	}) {
		return errors.New("JavaScript event loop is not running")
	}

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		finish(errExecutionTimeout)
		return fmt.Errorf("JavaScript execution timeout after %s", r.timeout)
	}
}

func (r *Runtime) callOnLoop(vm *goja.Runtime, name string, args []any, deadline time.Time, finish func(error)) {
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
	err := runWithDeadline(vm, deadline, func() error {
		var err error
		callResult, err = call(goja.Undefined(), values...)
		return err
	})
	if err != nil {
		if errors.Is(err, errExecutionTimeout) {
			finish(errExecutionTimeout)
		} else {
			finish(fmt.Errorf("JavaScript error: %v", err))
		}
		return
	}

	_, ok = callResult.Export().(*goja.Promise)
	if !ok {
		finish(truthyResult(name, callResult))
		return
	}

	then, ok := goja.AssertFunction(callResult.ToObject(vm).Get("then"))
	if !ok {
		finish(errors.New("JavaScript Promise has no callable then method"))
		return
	}
	onFulfilled := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		finish(truthyResult(name, call.Argument(0)))
		return goja.Undefined()
	})
	onRejected := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		finish(fmt.Errorf("Promise rejected: %v", call.Argument(0)))
		return goja.Undefined()
	})
	if _, err := then(callResult, onFulfilled, onRejected); err != nil {
		finish(fmt.Errorf("failed to observe Promise: %v", err))
		return
	}
}

func truthyResult(name string, value goja.Value) error {
	if value != nil && value.ToBoolean() {
		return nil
	}
	return fmt.Errorf("%s returned false", name)
}

// runWithDeadline interrupts synchronous JavaScript that exceeds deadline.
// Promise waiting is handled by Call without blocking the event loop.
func runWithDeadline(vm *goja.Runtime, deadline time.Time, fn func() error) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return errExecutionTimeout
	}

	var running atomic.Bool
	var timedOut atomic.Bool
	running.Store(true)
	timerDone := make(chan struct{})
	timer := time.AfterFunc(remaining, func() {
		defer close(timerDone)
		if running.CompareAndSwap(true, false) {
			timedOut.Store(true)
			vm.Interrupt(errExecutionTimeout)
		}
	})

	err := fn()
	if running.CompareAndSwap(true, false) {
		if !timer.Stop() {
			<-timerDone
		}
	} else {
		<-timerDone
		vm.ClearInterrupt()
	}
	if timedOut.Load() {
		return errExecutionTimeout
	}
	return err
}
