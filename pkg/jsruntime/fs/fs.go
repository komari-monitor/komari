package fs

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/filepathutil"
)

type nodeFileHandle struct {
	file       *os.File
	resourceID uint64
}

type nodeStat struct {
	info os.FileInfo
}

type nodePathMode uint8

const (
	nodePathFollowFinal nodePathMode = iota
	nodePathNoFollowFinal
)

// rootedDir is one confined filesystem root: the resolved absolute path and
// the os.Root handle used for actual file operations.
type rootedDir struct {
	path   string
	handle *os.Root
}

type Module struct {
	runtime            *bridge.Runtime
	nodeRoot           string
	allowAllFileAccess bool
	nodeFSRoot         *os.Root
	extraRoots         []rootedDir
	lifecycleMu        sync.RWMutex

	cwdMu   sync.RWMutex
	nodeCwd string

	fileMu       sync.Mutex
	fileID       int
	files        map[int]nodeFileHandle
	closed       bool
	onFileOpen   func(int, *os.File, uint64)
	onFileClose  func(int)
	externalFile func(int) (*os.File, bool)
}

// New creates the filesystem module confined to root (BaseDir) plus any extra
// roots. When allowAllFileAccess is true every root is opened but ignored so
// paths resolve against the real filesystem.
func New(runtime *bridge.Runtime, root, cwd string, extraRoots []string, allowAllFileAccess bool) (*Module, error) {
	m := &Module{
		runtime:            runtime,
		nodeRoot:           root,
		nodeCwd:            cwd,
		allowAllFileAccess: allowAllFileAccess,
		fileID:             2,
		files:              make(map[int]nodeFileHandle),
	}
	if root != "" && !allowAllFileAccess {
		rootHandle, err := os.OpenRoot(root)
		if err != nil {
			return nil, fmt.Errorf("open JavaScript BaseDir for fs: %w", err)
		}
		m.nodeFSRoot = rootHandle
	}
	if !allowAllFileAccess {
		for _, extra := range extraRoots {
			if extra == "" || extra == root {
				continue
			}
			handle, err := os.OpenRoot(extra)
			if err != nil {
				m.Close()
				return nil, fmt.Errorf("open JavaScript extra root %s for fs: %w", extra, err)
			}
			m.extraRoots = append(m.extraRoots, rootedDir{path: extra, handle: handle})
		}
	}
	return m, nil
}

func (m *Module) Cwd() string {
	m.cwdMu.RLock()
	cwd := m.nodeCwd
	m.cwdMu.RUnlock()
	return cwd
}

func (m *Module) Resolve(name string, allowMissing bool) (string, error) {
	return m.resolveNodePathAt(name, m.Cwd(), allowMissing, nodePathFollowFinal)
}

func (m *Module) Chdir(name string) error {
	resolved, err := m.Resolve(name, false)
	if err != nil {
		return err
	}
	info, err := m.nodeStat(resolved, true)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", name)
	}
	m.cwdMu.Lock()
	m.nodeCwd = resolved
	m.cwdMu.Unlock()
	return nil
}

func (m *Module) Close() {
	m.lifecycleMu.Lock()
	if m.closed {
		m.lifecycleMu.Unlock()
		return
	}
	m.closed = true
	if m.nodeFSRoot != nil {
		_ = m.nodeFSRoot.Close()
	}
	for _, extra := range m.extraRoots {
		if extra.handle != nil {
			_ = extra.handle.Close()
		}
	}
	m.lifecycleMu.Unlock()

	m.fileMu.Lock()
	handles := make([]nodeFileHandle, 0, len(m.files))
	for fd, handle := range m.files {
		handles = append(handles, handle)
		delete(m.files, fd)
	}
	m.fileMu.Unlock()
	for _, handle := range handles {
		m.runtime.RemoveResource(handle.resourceID)
		_ = handle.file.Close()
	}
}

func (m *Module) OpenFileCount() int {
	m.fileMu.Lock()
	count := len(m.files)
	m.fileMu.Unlock()
	return count
}

func (m *Module) SetFileHooks(onOpen func(int, *os.File, uint64), onClose func(int)) {
	m.fileMu.Lock()
	m.onFileOpen = onOpen
	m.onFileClose = onClose
	m.fileMu.Unlock()
}

func (m *Module) SetExternalFileLookup(lookup func(int) (*os.File, bool)) {
	m.fileMu.Lock()
	m.externalFile = lookup
	m.fileMu.Unlock()
}

func (m *Module) resolveNodePath(name string, allowMissing bool) (string, error) {
	return m.resolveNodePathAt(name, m.Cwd(), allowMissing, nodePathFollowFinal)
}

func (m *Module) resolveNodePathNoFollow(name string, allowMissing bool) (string, error) {
	return m.resolveNodePathAt(name, m.Cwd(), allowMissing, nodePathNoFollowFinal)
}

func (m *Module) resolveNodePathAt(name, cwd string, allowMissing bool, mode nodePathMode) (string, error) {
	if name == "" {
		return "", errors.New("path is empty")
	}
	resolved := filepath.FromSlash(name)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	if m.allowAllFileAccess || m.nodeRoot == "" {
		return resolved, nil
	}
	if !m.withinAnyRoot(resolved) {
		return "", fmt.Errorf("path escapes BaseDir: %s", name)
	}
	// No-follow operations on a confined root itself (for example
	// fs.mkdirSync(__storageDir__, {recursive:true})) operate through the
	// root handle with the "." relative path, so they never traverse the
	// parent directory. The parent-symlink check below would always reject
	// such paths because the parent lies outside the sandbox by definition.
	if mode == nodePathNoFollowFinal && m.isConfinedRoot(resolved) {
		return resolved, nil
	}
	pathToResolve := resolved
	if mode == nodePathNoFollowFinal {
		pathToResolve = filepath.Dir(resolved)
	}
	actual, err := filepath.EvalSymlinks(pathToResolve)
	if err == nil {
		if !m.withinAnyRoot(actual) {
			return "", fmt.Errorf("path symlink escapes BaseDir: %s", name)
		}
		if mode == nodePathNoFollowFinal {
			return resolved, nil
		}
		return actual, nil
	}
	if !allowMissing || !os.IsNotExist(err) {
		return "", err
	}
	parent := pathToResolve
	for {
		next := filepath.Dir(parent)
		if next == parent {
			return resolved, nil
		}
		parent = next
		actualParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr == nil {
			if !m.withinAnyRoot(actualParent) {
				return "", fmt.Errorf("path parent symlink escapes BaseDir: %s", name)
			}
			return resolved, nil
		}
		if !os.IsNotExist(parentErr) {
			return "", parentErr
		}
	}
}

// isConfinedRoot reports whether path is exactly one of the confined roots
// (BaseDir or an extra root) after cleaning.
func (m *Module) isConfinedRoot(path string) bool {
	if m.nodeRoot != "" && filepath.Clean(m.nodeRoot) == path {
		return true
	}
	for _, extra := range m.extraRoots {
		if filepath.Clean(extra.path) == path {
			return true
		}
	}
	return false
}

// withinAnyRoot reports whether path stays inside BaseDir or one of the
// extra confined roots.
func (m *Module) withinAnyRoot(path string) bool {
	if filepathutil.WithinBase(m.nodeRoot, path) {
		return true
	}
	for _, extra := range m.extraRoots {
		if filepathutil.WithinBase(extra.path, path) {
			return true
		}
	}
	return false
}

func (m *Module) Load(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	syncMethods := make(map[string]goja.Callable)
	setSync := func(name string, function any) {
		value := vm.ToValue(function)
		_ = exports.Set(name, value)
		callable, _ := goja.AssertFunction(value)
		syncMethods[name] = callable
	}

	setSync("readFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePath(call.Argument(0).String(), false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		data, err := m.nodeReadFile(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return encodeFSData(vm, data, fsEncoding(call.Argument(1)))
	})
	setSync("writeFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePathNoFollow(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		data := buffer.Bytes(vm, call.Argument(1))
		if err := m.nodeWriteFile(path, data, fsMode(call.Argument(2), 0o666)); err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("appendFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePath(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		file, err := m.nodeOpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsMode(call.Argument(2), 0o666))
		if err == nil {
			_, writeErr := file.Write(buffer.Bytes(vm, call.Argument(1)))
			err = errors.Join(writeErr, file.Close())
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("existsSync", func(name string) bool {
		path, err := m.resolveNodePath(name, false)
		if err != nil {
			return false
		}
		_, err = m.nodeStat(path, true)
		return err == nil
	})
	setSync("accessSync", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		mode := fsAccessMode(call.Argument(1))
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeAccess(path, mode)
		}
		if err != nil {
			panic(nodeErrorObject(vm, err, "access"))
		}
		return goja.Undefined()
	})
	setSync("statSync", func(call goja.FunctionCall) goja.Value { return m.fsStat(vm, call.Argument(0).String(), true) })
	setSync("lstatSync", func(call goja.FunctionCall) goja.Value { return m.fsStat(vm, call.Argument(0).String(), false) })
	setSync("readdirSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePath(call.Argument(0).String(), false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		entries, err := m.nodeReadDir(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		withTypes := fsWithFileTypes(call.Argument(1))
		result := make([]any, 0, len(entries))
		for _, entry := range entries {
			if withTypes {
				result = append(result, fsDirent(vm, entry))
			} else {
				result = append(result, entry.Name())
			}
		}
		return vm.ToValue(result)
	})
	setSync("mkdirSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePathNoFollow(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		recursive := fsBooleanOption(call.Argument(1), "recursive")
		if recursive {
			err = m.nodeMkdir(path, fsMode(call.Argument(1), 0o777), true)
		} else {
			err = m.nodeMkdir(path, fsMode(call.Argument(1), 0o777), false)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("rmSync", func(call goja.FunctionCall) goja.Value {
		path, err := m.resolveNodePathNoFollow(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if fsBooleanOption(call.Argument(1), "recursive") {
			err = m.nodeRemove(path, true)
		} else {
			err = m.nodeRemove(path, false)
		}
		if err != nil && !fsBooleanOption(call.Argument(1), "force") {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("unlinkSync", func(name string) {
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = m.nodeRemove(path, false)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("rmdirSync", func(name string) {
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = m.nodeRemove(path, false)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("renameSync", func(oldName, newName string) {
		oldPath, err := m.resolveNodePathNoFollow(oldName, false)
		if err == nil {
			var newPath string
			newPath, err = m.resolveNodePathNoFollow(newName, true)
			if err == nil {
				err = m.nodeRename(oldPath, newPath)
			}
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("copyFileSync", func(sourceName, targetName string) {
		if err := m.copyNodeFile(sourceName, targetName); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("realpathSync", func(name string) string {
		path, err := m.resolveNodePath(name, false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("readlinkSync", func(name string) string {
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			path, err = m.nodeReadlink(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("symlinkSync", func(targetName, linkName string) {
		target, err := m.resolveNodePath(targetName, true)
		if err == nil {
			var link string
			link, err = m.resolveNodePathNoFollow(linkName, true)
			if err == nil {
				err = m.nodeSymlink(target, link)
			}
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("truncateSync", func(name string, size int64) {
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeTruncate(path, size)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("chmodSync", func(name string, mode uint32) {
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeChmod(path, os.FileMode(mode))
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("utimesSync", func(name string, accessTime, modifyTime any) {
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeChtimes(path, fsTime(accessTime), fsTime(modifyTime))
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("mkdtempSync", func(prefix string) string {
		path, err := m.resolveNodePathNoFollow(prefix, true)
		if err == nil {
			path, err = m.nodeMkdirTemp(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("openSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(m.fsOpen(vm, call)) })
	setSync("closeSync", func(fd int) {
		if err := m.fsClose(fd); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("fstatSync", func(fd int) goja.Value {
		file, err := m.fsFile(fd)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		info, err := file.Stat()
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return fsStatObject(vm, info)
	})
	setSync("fsyncSync", func(fd int) {
		file, err := m.fsFile(fd)
		if err == nil {
			err = file.Sync()
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("readSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(m.fsRead(vm, call)) })
	setSync("writeSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(m.fsWrite(vm, call)) })

	for _, asyncName := range []string{
		"readFile", "writeFile", "appendFile", "access", "stat", "lstat", "readdir", "mkdir", "rm",
		"unlink", "rmdir", "rename", "copyFile", "realpath", "readlink", "symlink", "truncate",
		"chmod", "utimes", "mkdtemp", "open", "close", "fstat", "fsync", "read", "write",
	} {
		_ = exports.Set(asyncName, m.fsAsync(vm, asyncName))
	}
	_ = exports.Set("exists", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		name := call.Argument(0).String()
		go func() {
			path, err := m.resolveNodePath(name, false)
			if err == nil {
				_, err = m.nodeStat(path, true)
			}
			m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "fs.exists", func() error {
					_, callbackErr := callback(goja.Undefined(), vm.ToValue(err == nil))
					return callbackErr
				})
			})
		}()
		return goja.Undefined()
	})
	_ = exports.Set("constants", map[string]int{"F_OK": 0, "R_OK": 4, "W_OK": 2, "X_OK": 1, "COPYFILE_EXCL": 1})
	m.attachFSPromises(vm, exports)
	m.attachFSStreams(vm, exports)
	_ = module.Set("exports", exports)
}

func (m *Module) fsAsync(vm *goja.Runtime, name string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("callback must be a function"))
		}
		callback, ok := goja.AssertFunction(call.Arguments[len(call.Arguments)-1])
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		arguments := append([]goja.Value(nil), call.Arguments[:len(call.Arguments)-1]...)
		operation := m.prepareFSAsync(vm, name, arguments)
		go func() {
			result, err := operation.run()
			if !m.runtime.RunOnLoop(func(vm *goja.Runtime) {
				_ = m.runtime.RunJob(vm, "fs."+name, func() error {
					if err != nil {
						_, callbackErr := callback(goja.Undefined(), nodeErrorObject(vm, err, "fs."+name))
						return callbackErr
					}
					values := append([]goja.Value{goja.Null()}, operation.values(vm, result)...)
					_, callbackErr := callback(goja.Undefined(), values...)
					return callbackErr
				})
			}) {
				return
			}
		}()
		return goja.Undefined()
	}
}

func (m *Module) attachFSPromises(vm *goja.Runtime, exports *goja.Object) {
	factory, err := vm.RunString(fsPromisesSource)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	call, _ := goja.AssertFunction(factory)
	promises, err := call(goja.Undefined(), exports)
	if err != nil {
		panic(err)
	}
	_ = exports.Set("promises", promises)
}

func (m *Module) fsStat(vm *goja.Runtime, name string, follow bool) goja.Value {
	path, err := m.resolveNodePathAt(name, m.Cwd(), false, map[bool]nodePathMode{true: nodePathFollowFinal, false: nodePathNoFollowFinal}[follow])
	if err != nil {
		panic(vm.NewGoError(err))
	}
	var info os.FileInfo
	info, err = m.nodeStat(path, follow)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return fsStatObject(vm, info)
}

func fsStatObject(vm *goja.Runtime, info os.FileInfo) *goja.Object {
	object := vm.NewObject()
	mode := info.Mode()
	_ = object.Set("dev", 0)
	_ = object.Set("ino", 0)
	_ = object.Set("mode", uint32(mode))
	_ = object.Set("nlink", 1)
	_ = object.Set("uid", 0)
	_ = object.Set("gid", 0)
	_ = object.Set("rdev", 0)
	_ = object.Set("size", info.Size())
	_ = object.Set("blksize", 4096)
	_ = object.Set("blocks", (info.Size()+511)/512)
	modified := info.ModTime()
	_ = object.Set("atimeMs", float64(modified.UnixMilli()))
	_ = object.Set("mtimeMs", float64(modified.UnixMilli()))
	_ = object.Set("ctimeMs", float64(modified.UnixMilli()))
	_ = object.Set("birthtimeMs", float64(modified.UnixMilli()))
	_ = object.Set("atime", fsDate(vm, modified))
	_ = object.Set("mtime", fsDate(vm, modified))
	_ = object.Set("ctime", fsDate(vm, modified))
	_ = object.Set("birthtime", fsDate(vm, modified))
	_ = object.Set("isFile", func() bool { return mode.IsRegular() })
	_ = object.Set("isDirectory", func() bool { return mode.IsDir() })
	_ = object.Set("isSymbolicLink", func() bool { return mode&os.ModeSymlink != 0 })
	_ = object.Set("isBlockDevice", func() bool { return mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0 })
	_ = object.Set("isCharacterDevice", func() bool { return mode&os.ModeCharDevice != 0 })
	_ = object.Set("isFIFO", func() bool { return mode&os.ModeNamedPipe != 0 })
	_ = object.Set("isSocket", func() bool { return mode&os.ModeSocket != 0 })
	return object
}

func fsDirent(vm *goja.Runtime, entry fs.DirEntry) *goja.Object {
	object := vm.NewObject()
	mode := entry.Type()
	_ = object.Set("name", entry.Name())
	_ = object.Set("parentPath", "")
	_ = object.Set("path", "")
	_ = object.Set("isFile", func() bool { return mode.IsRegular() })
	_ = object.Set("isDirectory", func() bool { return mode.IsDir() })
	_ = object.Set("isSymbolicLink", func() bool { return mode&os.ModeSymlink != 0 })
	_ = object.Set("isBlockDevice", func() bool { return mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0 })
	_ = object.Set("isCharacterDevice", func() bool { return mode&os.ModeCharDevice != 0 })
	_ = object.Set("isFIFO", func() bool { return mode&os.ModeNamedPipe != 0 })
	_ = object.Set("isSocket", func() bool { return mode&os.ModeSocket != 0 })
	return object
}

func fsDate(vm *goja.Runtime, value time.Time) goja.Value {
	object, err := vm.New(vm.Get("Date"), vm.ToValue(value.UnixMilli()))
	if err != nil {
		return vm.ToValue(value.UnixMilli())
	}
	return object
}

func fsEncoding(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}
	if object, ok := value.(*goja.Object); ok {
		encoding := object.Get("encoding")
		if goja.IsUndefined(encoding) || goja.IsNull(encoding) {
			return ""
		}
		return encoding.String()
	}
	return value.String()
}

func encodeFSData(vm *goja.Runtime, data []byte, encoding string) goja.Value {
	if encoding == "" || encoding == "buffer" {
		return buffer.WrapBytes(vm, data)
	}
	return vm.ToValue(buffer.EncodeBytes(vm, data, vm.ToValue(encoding)))
}

func fsMode(value goja.Value, fallback os.FileMode) os.FileMode {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return fallback
	}
	if object, ok := value.(*goja.Object); ok {
		value = object.Get("mode")
		if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
			return fallback
		}
	}
	if text, ok := value.Export().(string); ok {
		mode, err := strconv.ParseUint(text, 8, 32)
		if err == nil {
			return os.FileMode(mode)
		}
	}
	return os.FileMode(value.ToInteger())
}

func fsAccessMode(value goja.Value) int {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return 0
	}
	return int(value.ToInteger())
}

func fsBooleanOption(value goja.Value, name string) bool {
	if object, ok := value.(*goja.Object); ok {
		property := object.Get(name)
		return property != nil && property.ToBoolean()
	}
	return false
}

func fsWithFileTypes(value goja.Value) bool { return fsBooleanOption(value, "withFileTypes") }

func fsTime(value any) time.Time {
	switch converted := value.(type) {
	case time.Time:
		return converted
	case int64:
		return time.Unix(converted, 0)
	case float64:
		return time.Unix(int64(converted), int64((converted-float64(int64(converted)))*float64(time.Second)))
	case string:
		parsed, _ := time.Parse(time.RFC3339, converted)
		return parsed
	default:
		return time.Now()
	}
}

func (m *Module) copyNodeFile(sourceName, targetName string) error {
	source, err := m.resolveNodePath(sourceName, false)
	if err != nil {
		return err
	}
	target, err := m.resolveNodePath(targetName, true)
	if err != nil {
		return err
	}
	input, err := m.nodeOpenFile(source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := m.nodeOpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func (m *Module) fsOpen(vm *goja.Runtime, call goja.FunctionCall) int {
	path, err := m.resolveNodePath(call.Argument(0).String(), true)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	flags := fsOpenFlags(call.Argument(1).String())
	file, err := m.nodeOpenFile(path, flags, fsMode(call.Argument(2), 0o666))
	if err != nil {
		panic(vm.NewGoError(err))
	}
	fd, err := m.registerFile(file)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return fd
}

func (m *Module) registerFile(file *os.File) (int, error) {
	if file == nil {
		return 0, errors.New("file is nil")
	}
	m.lifecycleMu.RLock()
	if m.closed {
		m.lifecycleMu.RUnlock()
		_ = file.Close()
		return 0, errors.New("JavaScript runtime is closed")
	}
	m.fileMu.Lock()
	m.fileID++
	fd := m.fileID
	m.fileMu.Unlock()

	var resourceID uint64
	resourceID = m.runtime.AddResource(func() {
		m.removeFileResource(resourceID)
		_ = file.Close()
	})
	if resourceID == 0 {
		m.lifecycleMu.RUnlock()
		return 0, errors.New("JavaScript runtime is closed")
	}

	m.fileMu.Lock()
	m.files[fd] = nodeFileHandle{file: file, resourceID: resourceID}
	if m.onFileOpen != nil {
		m.onFileOpen(fd, file, resourceID)
	}
	m.fileMu.Unlock()
	m.lifecycleMu.RUnlock()
	return fd, nil
}

func (m *Module) removeFileResource(resourceID uint64) {
	if resourceID == 0 {
		return
	}
	m.fileMu.Lock()
	for fd, handle := range m.files {
		if handle.resourceID == resourceID {
			delete(m.files, fd)
			break
		}
	}
	m.fileMu.Unlock()
}

func fsOpenFlags(flags string) int {
	switch flags {
	case "r":
		return os.O_RDONLY
	case "r+":
		return os.O_RDWR
	case "w":
		return os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	case "wx":
		return os.O_CREATE | os.O_EXCL | os.O_TRUNC | os.O_WRONLY
	case "w+":
		return os.O_CREATE | os.O_TRUNC | os.O_RDWR
	case "a":
		return os.O_CREATE | os.O_APPEND | os.O_WRONLY
	case "ax":
		return os.O_CREATE | os.O_EXCL | os.O_APPEND | os.O_WRONLY
	case "a+":
		return os.O_CREATE | os.O_APPEND | os.O_RDWR
	default:
		return os.O_RDONLY
	}
}

func (m *Module) fsFile(fd int) (*os.File, error) {
	m.fileMu.Lock()
	handle, ok := m.files[fd]
	lookup := m.externalFile
	m.fileMu.Unlock()
	if ok {
		return handle.file, nil
	}
	if lookup != nil {
		if file, exists := lookup(fd); exists {
			return file, nil
		}
	}
	return nil, fmt.Errorf("bad file descriptor: %d", fd)
}

func (m *Module) fsClose(fd int) error {
	m.fileMu.Lock()
	handle, ok := m.files[fd]
	if ok {
		delete(m.files, fd)
	}
	m.fileMu.Unlock()
	if !ok {
		return fmt.Errorf("bad file descriptor: %d", fd)
	}
	m.runtime.RemoveResource(handle.resourceID)
	if m.onFileClose != nil {
		m.onFileClose(fd)
	}
	return handle.file.Close()
}

func (m *Module) WriteResolved(path string, data []byte, mode os.FileMode) error {
	return m.nodeWriteFile(path, data, mode)
}

func (m *Module) fsRead(vm *goja.Runtime, call goja.FunctionCall) int {
	file, err := m.fsFile(int(call.Argument(0).ToInteger()))
	if err != nil {
		panic(vm.NewGoError(err))
	}
	object := call.Argument(1).ToObject(vm)
	data := buffer.Bytes(vm, object)
	offset := int(call.Argument(2).ToInteger())
	length := int(call.Argument(3).ToInteger())
	position := call.Argument(4)
	if offset < 0 || length < 0 || offset+length > len(data) {
		panic(vm.NewTypeError("buffer range is out of bounds"))
	}
	if !goja.IsNull(position) && !goja.IsUndefined(position) {
		_, err = file.Seek(position.ToInteger(), io.SeekStart)
	}
	var count int
	if err == nil {
		count, err = file.Read(data[offset : offset+length])
	}
	if err != nil && !errors.Is(err, io.EOF) {
		panic(vm.NewGoError(err))
	}
	return count
}

func (m *Module) fsWrite(vm *goja.Runtime, call goja.FunctionCall) int {
	file, err := m.fsFile(int(call.Argument(0).ToInteger()))
	if err != nil {
		panic(vm.NewGoError(err))
	}
	data := buffer.Bytes(vm, call.Argument(1))
	if call.Argument(1).ExportType().Kind() == 0 {
		data = []byte(call.Argument(1).String())
	}
	if position := call.Argument(4); !goja.IsNull(position) && !goja.IsUndefined(position) {
		_, err = file.Seek(position.ToInteger(), io.SeekStart)
	}
	if err != nil {
		panic(vm.NewGoError(err))
	}
	count, err := file.Write(data)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return count
}

//go:embed promises.js
var fsPromisesSource string

//go:embed streams.js
var fsStreamsSource string

// attachFSStreams registers fs.createReadStream and fs.createWriteStream,
// implemented on top of the jsruntime stream module and the callback-based
// fs file operations.
func (m *Module) attachFSStreams(vm *goja.Runtime, exports *goja.Object) {
	factoryValue, err := vm.RunString(fsStreamsSource)
	if err != nil {
		panic(vm.NewGoError(fmt.Errorf("load fs streams: %w", err)))
	}
	factory, _ := goja.AssertFunction(factoryValue)
	value, err := factory(goja.Undefined(), exports, require.Require(vm, "stream"), require.Require(vm, "buffer"))
	if err != nil {
		panic(err)
	}
	object := value.ToObject(vm)
	for _, name := range []string{"createReadStream", "createWriteStream"} {
		_ = exports.Set(name, object.Get(name))
	}
}
