package fs

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/filepathutil"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
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
	cwdMu              sync.RWMutex
	nodeCwd            string
	fileMu             sync.Mutex
	fileID             int
	files              map[int]nodeFileHandle
	closed             bool
	onFileOpen         func(int, *os.File, uint64)
	onFileClose        func(int)
	externalFile       func(int) (*os.File, bool)
}

func New(runtime *bridge.Runtime, root, cwd string, extraRoots []string, allowAllFileAccess bool) (*Module, error) {
	m := &Module{runtime: runtime, nodeRoot: root, nodeCwd: cwd, allowAllFileAccess: allowAllFileAccess, fileID: 2, files: make(map[int]nodeFileHandle)}
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
func (m *Module) Load(vm *moejs.Runtime, module *engine.Object) {
	exports := vm.Realm().NewObject()
	syncMethods := make(map[string]moejs.Value)
	setSync := func(name string, value moejs.Value) {
		_ = native.Set(vm, exports, name, value)
		callable, _ := checkedFunction(value)
		syncMethods[name] = callable
	}
	setSync("readFileSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePath(native.String(vm, moejs.Arg(args, 0)), false)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		data, err := m.nodeReadFile(path)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, encodeFSData(vm, data, fsEncoding(vm, moejs.Arg(args, 1)))), nil
	}))
	setSync("writeFileSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePathNoFollow(native.String(vm, moejs.Arg(args, 0)), true)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		data := buffer.Bytes(vm, moejs.Arg(args, 1))
		if err := m.nodeWriteFile(path, data, fsMode(vm, moejs.Arg(args, 2), 0o666)); err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	setSync("appendFileSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePath(native.String(vm, moejs.Arg(args, 0)), true)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		file, err := m.nodeOpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsMode(vm, moejs.Arg(args, 2), 0o666))
		if err == nil {
			_, writeErr := file.Write(buffer.Bytes(vm, moejs.Arg(args, 1)))
			err = errors.Join(writeErr, file.Close())
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	setSync("existsSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePath(name, false)
		if err != nil {
			return native.From(vm, false), nil
		}
		_, err = m.nodeStat(path, true)
		return native.From(vm, err == nil), nil
	}))
	setSync("accessSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		mode := fsAccessMode(vm, moejs.Arg(args, 1))
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeAccess(path, mode)
		}
		if err != nil {
			panic(engine.ObjectValue(nodeErrorObject(vm, err, "access")))
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	setSync("statSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fsStat(vm, native.String(vm, moejs.Arg(args, 0)), true)), nil
	}))
	setSync("lstatSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fsStat(vm, native.String(vm, moejs.Arg(args, 0)), false)), nil
	}))
	setSync("readdirSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePath(native.String(vm, moejs.Arg(args, 0)), false)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		entries, err := m.nodeReadDir(path)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		withTypes := fsWithFileTypes(vm, moejs.Arg(args, 1))
		result := make([]any, 0, len(entries))
		for _, entry := range entries {
			if withTypes {
				result = append(result, fsDirent(vm, entry))
			} else {
				result = append(result, entry.Name())
			}
		}
		return native.From(vm, result), nil
	}))
	setSync("mkdirSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePathNoFollow(native.String(vm, moejs.Arg(args, 0)), true)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		recursive := fsBooleanOption(vm, moejs.Arg(args, 1), "recursive")
		if recursive {
			err = m.nodeMkdir(path, fsMode(vm, moejs.Arg(args, 1), 0o777), true)
		} else {
			err = m.nodeMkdir(path, fsMode(vm, moejs.Arg(args, 1), 0o777), false)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	setSync("rmSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		path, err := m.resolveNodePathNoFollow(native.String(vm, moejs.Arg(args, 0)), true)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		if fsBooleanOption(vm, moejs.Arg(args, 1), "recursive") {
			err = m.nodeRemove(path, true)
		} else {
			err = m.nodeRemove(path, false)
		}
		if err != nil && !fsBooleanOption(vm, moejs.Arg(args, 1), "force") {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, moejs.Undefined()), nil
	}))
	setSync("unlinkSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = m.nodeRemove(path, false)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("rmdirSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = m.nodeRemove(path, false)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("renameSync", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		oldName := native.String(vm, moejs.Arg(args, 0))
		newName := native.String(vm, moejs.Arg(args, 1))
		oldPath, err := m.resolveNodePathNoFollow(oldName, false)
		if err == nil {
			var newPath string
			newPath, err = m.resolveNodePathNoFollow(newName, true)
			if err == nil {
				err = m.nodeRename(oldPath, newPath)
			}
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("copyFileSync", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		sourceName := native.String(vm, moejs.Arg(args, 0))
		targetName := native.String(vm, moejs.Arg(args, 1))
		if err := m.copyNodeFile(sourceName, targetName); err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("realpathSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePath(name, false)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, path), nil
	}))
	setSync("readlinkSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePathNoFollow(name, false)
		if err == nil {
			path, err = m.nodeReadlink(path)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, path), nil
	}))
	setSync("symlinkSync", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		targetName := native.String(vm, moejs.Arg(args, 0))
		linkName := native.String(vm, moejs.Arg(args, 1))
		target, err := m.resolveNodePath(targetName, true)
		if err == nil {
			var link string
			link, err = m.resolveNodePathNoFollow(linkName, true)
			if err == nil {
				err = m.nodeSymlink(target, link)
			}
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("truncateSync", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		size := int64(native.Int(vm, moejs.Arg(args, 1)))
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeTruncate(path, size)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("chmodSync", native.Bind(vm, "", 2, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		mode := uint32(native.Int(vm, moejs.Arg(args, 1)))
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeChmod(path, os.FileMode(mode))
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("utimesSync", native.Bind(vm, "", 3, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		name := native.String(vm, moejs.Arg(args, 0))
		accessTime := native.Export(vm, moejs.Arg(args, 1))
		modifyTime := native.Export(vm, moejs.Arg(args, 2))
		path, err := m.resolveNodePath(name, false)
		if err == nil {
			err = m.nodeChtimes(path, fsTime(accessTime), fsTime(modifyTime))
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("mkdtempSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		prefix := native.String(vm, moejs.Arg(args, 0))
		path, err := m.resolveNodePathNoFollow(prefix, true)
		if err == nil {
			path, err = m.nodeMkdirTemp(path)
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, path), nil
	}))
	setSync("openSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fsOpen(vm, args)), nil
	}))
	setSync("closeSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		fd := int(native.Int(vm, moejs.Arg(args, 0)))
		if err := m.fsClose(fd); err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("fstatSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		fd := int(native.Int(vm, moejs.Arg(args, 0)))
		file, err := m.fsFile(fd)
		if err != nil {
			panic(native.GoError(vm, err))
		}
		info, err := file.Stat()
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return native.From(vm, fsStatObject(vm, info)), nil
	}))
	setSync("fsyncSync", native.Bind(vm, "", 1, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		fd := int(native.Int(vm, moejs.Arg(args, 0)))
		file, err := m.fsFile(fd)
		if err == nil {
			err = file.Sync()
		}
		if err != nil {
			panic(native.GoError(vm, err))
		}
		return moejs.Undefined(), nil
	}))
	setSync("readSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fsRead(vm, args)), nil
	}))
	setSync("writeSync", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, m.fsWrite(vm, args)), nil
	}))
	for _, asyncName := range []string{"readFile", "writeFile", "appendFile", "access", "stat", "lstat", "readdir", "mkdir", "rm", "unlink", "rmdir", "rename", "copyFile", "realpath", "readlink", "symlink", "truncate", "chmod", "utimes", "mkdtemp", "open", "close", "fstat", "fsync", "read", "write"} {
		_ = native.Set(vm, exports, asyncName, m.fsAsync(vm, asyncName))
	}
	_ = native.Set(vm, exports, "exists", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		callback, ok := checkedFunction(moejs.Arg(args, 1))
		if !ok {
			panic(native.TypeError(vm, "callback must be a function"))
		}
		name := native.String(vm, moejs.Arg(args, 0))
		go func() {
			path, err := m.resolveNodePath(name, false)
			if err == nil {
				_, err = m.nodeStat(path, true)
			}
			m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "fs.exists", func() error {
					_, callbackErr := native.Call(vm, callback, moejs.Undefined(), native.From(vm, err == nil))
					return callbackErr
				})
			})
		}()
		return native.From(vm, moejs.Undefined()), nil
	}))
	_ = native.Set(vm, exports, "constants", map[string]int{"F_OK": 0, "R_OK": 4, "W_OK": 2, "X_OK": 1, "COPYFILE_EXCL": 1})
	m.attachFSPromises(vm, exports)
	m.attachFSStreams(vm, exports)
	_ = native.Set(vm, module, "exports", exports)
}
func (m *Module) fsAsync(vm *moejs.Runtime, name string) moejs.Value {
	return native.Bind(vm, name, 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		if len(args) == 0 {
			panic(native.TypeError(vm, "callback must be a function"))
		}
		callback, ok := checkedFunction(args[len(args)-1])
		if !ok {
			panic(native.TypeError(vm, "callback must be a function"))
		}
		arguments := append([]moejs.Value(nil), args[:len(args)-1]...)
		operation := m.prepareFSAsync(vm, name, arguments)
		go func() {
			result, err := operation.run()
			if !m.runtime.RunOnLoop(func(vm *moejs.Runtime) {
				_ = m.runtime.RunJob(vm, "fs."+name, func() error {
					if err != nil {
						_, callbackErr := native.Call(vm, callback, moejs.Undefined(), native.From(vm, nodeErrorObject(vm, err, "fs."+name)))
						return callbackErr
					}
					values := append([]moejs.Value{moejs.Null()}, operation.values(vm, result)...)
					_, callbackErr := native.Call(vm, callback, moejs.Undefined(), values...)
					return callbackErr
				})
			}) {
				return
			}
		}()
		return moejs.Undefined(), nil
	})
}
func (m *Module) attachFSPromises(vm *moejs.Runtime, exports *engine.Object) {
	factory, err := native.Run(vm, fsPromisesSource)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	call, _ := checkedFunction(factory)
	promises, err := native.Call(vm, call, moejs.Undefined(), native.From(vm, exports))
	if err != nil {
		panic(err)
	}
	_ = native.Set(vm, exports, "promises", promises)
}
func (m *Module) fsStat(vm *moejs.Runtime, name string, follow bool) moejs.Value {
	path, err := m.resolveNodePathAt(name, m.Cwd(), false, map[bool]nodePathMode{true: nodePathFollowFinal, false: nodePathNoFollowFinal}[follow])
	if err != nil {
		panic(native.GoError(vm, err))
	}
	var info os.FileInfo
	info, err = m.nodeStat(path, follow)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	return native.From(vm, fsStatObject(vm, info))
}
func fsStatObject(vm *moejs.Runtime, info os.FileInfo) *engine.Object {
	object := vm.Realm().NewObject()
	mode := info.Mode()
	_ = native.Set(vm, object, "dev", 0)
	_ = native.Set(vm, object, "ino", 0)
	_ = native.Set(vm, object, "mode", uint32(mode))
	_ = native.Set(vm, object, "nlink", 1)
	_ = native.Set(vm, object, "uid", 0)
	_ = native.Set(vm, object, "gid", 0)
	_ = native.Set(vm, object, "rdev", 0)
	_ = native.Set(vm, object, "size", info.Size())
	_ = native.Set(vm, object, "blksize", 4096)
	_ = native.Set(vm, object, "blocks", (info.Size()+511)/512)
	modified := info.ModTime()
	_ = native.Set(vm, object, "atimeMs", float64(modified.UnixMilli()))
	_ = native.Set(vm, object, "mtimeMs", float64(modified.UnixMilli()))
	_ = native.Set(vm, object, "ctimeMs", float64(modified.UnixMilli()))
	_ = native.Set(vm, object, "birthtimeMs", float64(modified.UnixMilli()))
	_ = native.Set(vm, object, "atime", fsDate(vm, modified))
	_ = native.Set(vm, object, "mtime", fsDate(vm, modified))
	_ = native.Set(vm, object, "ctime", fsDate(vm, modified))
	_ = native.Set(vm, object, "birthtime", fsDate(vm, modified))
	_ = native.Set(vm, object, "isFile", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode.IsRegular()), nil
	}))
	_ = native.Set(vm, object, "isDirectory", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode.IsDir()), nil
	}))
	_ = native.Set(vm, object, "isSymbolicLink", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeSymlink != 0), nil
	}))
	_ = native.Set(vm, object, "isBlockDevice", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0), nil
	}))
	_ = native.Set(vm, object, "isCharacterDevice", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeCharDevice != 0), nil
	}))
	_ = native.Set(vm, object, "isFIFO", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeNamedPipe != 0), nil
	}))
	_ = native.Set(vm, object, "isSocket", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeSocket != 0), nil
	}))
	return object
}
func fsDirent(vm *moejs.Runtime, entry fs.DirEntry) *engine.Object {
	object := vm.Realm().NewObject()
	mode := entry.Type()
	_ = native.Set(vm, object, "name", entry.Name())
	_ = native.Set(vm, object, "parentPath", "")
	_ = native.Set(vm, object, "path", "")
	_ = native.Set(vm, object, "isFile", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode.IsRegular()), nil
	}))
	_ = native.Set(vm, object, "isDirectory", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode.IsDir()), nil
	}))
	_ = native.Set(vm, object, "isSymbolicLink", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeSymlink != 0), nil
	}))
	_ = native.Set(vm, object, "isBlockDevice", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0), nil
	}))
	_ = native.Set(vm, object, "isCharacterDevice", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeCharDevice != 0), nil
	}))
	_ = native.Set(vm, object, "isFIFO", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeNamedPipe != 0), nil
	}))
	_ = native.Set(vm, object, "isSocket", native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		return native.From(vm, mode&os.ModeSocket != 0), nil
	}))
	return object
}
func fsDate(vm *moejs.Runtime, value time.Time) moejs.Value {
	object, err := native.New(vm, native.Global(vm, "Date"), native.From(vm, value.UnixMilli()))
	if err != nil {
		return native.From(vm, value.UnixMilli())
	}
	return native.From(vm, object)
}
func fsEncoding(vm *moejs.Runtime, value moejs.Value) string {
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return ""
	}
	if value.IsObject() {
		object := value.AsObject()
		encoding := native.Get(vm, object, "encoding")
		if encoding.IsUndefined() || encoding.IsNull() {
			return ""
		}
		return native.String(vm, encoding)
	}
	return native.String(vm, value)
}
func encodeFSData(vm *moejs.Runtime, data []byte, encoding string) moejs.Value {
	if encoding == "" || encoding == "buffer" {
		return native.From(vm, buffer.WrapBytes(vm, data))
	}
	return native.From(vm, buffer.EncodeBytes(vm, data, native.From(vm, encoding)))
}
func fsMode(vm *moejs.Runtime, value moejs.Value, fallback os.FileMode) os.FileMode {
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return fallback
	}
	if value.IsObject() {
		object := value.AsObject()
		value = native.Get(vm, object, "mode")
		if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
			return fallback
		}
	}
	if text, ok := native.Export(vm, value).(string); ok {
		mode, err := strconv.ParseUint(text, 8, 32)
		if err == nil {
			return os.FileMode(mode)
		}
	}
	return os.FileMode(native.Int(vm, value))
}
func fsAccessMode(vm *moejs.Runtime, value moejs.Value) int {
	if native.IsNil(value) || value.IsUndefined() || value.IsNull() {
		return 0
	}
	return int(native.Int(vm, value))
}
func fsBooleanOption(vm *moejs.Runtime, value moejs.Value, name string) bool {
	if value.IsObject() {
		object := value.AsObject()
		property := native.Get(vm, object, name)
		return !native.IsNil(property) && native.Bool(property)
	}
	return false
}
func fsWithFileTypes(vm *moejs.Runtime, value moejs.Value) bool {
	return fsBooleanOption(vm, value, "withFileTypes")
}
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
func (m *Module) fsOpen(vm *moejs.Runtime, args []moejs.Value) int {
	path, err := m.resolveNodePath(native.String(vm, moejs.Arg(args, 0)), true)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	flags := fsOpenFlags(native.String(vm, moejs.Arg(args, 1)))
	file, err := m.nodeOpenFile(path, flags, fsMode(vm, moejs.Arg(args, 2), 0o666))
	if err != nil {
		panic(native.GoError(vm, err))
	}
	fd, err := m.registerFile(file)
	if err != nil {
		panic(native.GoError(vm, err))
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
func (m *Module) fsRead(vm *moejs.Runtime, args []moejs.Value) int {
	file, err := m.fsFile(int(native.Int(vm, moejs.Arg(args, 0))))
	if err != nil {
		panic(native.GoError(vm, err))
	}
	data := buffer.Bytes(vm, moejs.Arg(args, 1))
	offset := int(native.Int(vm, moejs.Arg(args, 2)))
	length := int(native.Int(vm, moejs.Arg(args, 3)))
	position := moejs.Arg(args, 4)
	if offset < 0 || length < 0 || offset+length > len(data) {
		panic(native.TypeError(vm, "buffer range is out of bounds"))
	}
	if !position.IsNull() && !position.IsUndefined() {
		_, err = file.Seek(native.Int(vm, position), io.SeekStart)
	}
	var count int
	if err == nil {
		count, err = file.Read(data[offset : offset+length])
	}
	if err != nil && !errors.Is(err, io.EOF) {
		panic(native.GoError(vm, err))
	}
	return count
}
func (m *Module) fsWrite(vm *moejs.Runtime, args []moejs.Value) int {
	file, err := m.fsFile(int(native.Int(vm, moejs.Arg(args, 0))))
	if err != nil {
		panic(native.GoError(vm, err))
	}
	data := buffer.Bytes(vm, moejs.Arg(args, 1))
	if moejs.Arg(args, 1).IsString() {
		data = []byte(native.String(vm, moejs.Arg(args, 1)))
	}
	if position := moejs.Arg(args, 4); !position.IsNull() && !position.IsUndefined() {
		_, err = file.Seek(native.Int(vm, position), io.SeekStart)
	}
	if err != nil {
		panic(native.GoError(vm, err))
	}
	count, err := file.Write(data)
	if err != nil {
		panic(native.GoError(vm, err))
	}
	return count
}

//go:embed promises.js
var fsPromisesSource string

//go:embed streams.js
var fsStreamsSource string

func (m *Module) attachFSStreams(vm *moejs.Runtime, exports *engine.Object) {
	factoryValue, err := native.Run(vm, fsStreamsSource)
	if err != nil {
		panic(native.GoError(vm, fmt.Errorf("load fs streams: %w", err)))
	}
	factory, _ := checkedFunction(factoryValue)
	value, err := native.Call(vm, factory, moejs.Undefined(), native.From(vm, exports), require.Require(vm, "stream"), require.Require(vm, "buffer"))
	if err != nil {
		panic(err)
	}
	object := native.Object(vm, value)
	for _, name := range []string{"createReadStream", "createWriteStream"} {
		_ = native.Set(vm, exports, name, native.Get(vm, object, name))
	}
}
func checkedFunction(v moejs.Value) (moejs.Value, bool) { return v, engine.IsCallable(v) }
