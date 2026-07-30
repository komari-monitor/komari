package jsruntime

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
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

func (r *Runtime) resolveNodePath(name string, allowMissing bool) (string, error) {
	return r.resolveNodePathAt(name, r.nodeCwd, allowMissing, nodePathFollowFinal)
}

func (r *Runtime) resolveNodePathNoFollow(name string, allowMissing bool) (string, error) {
	return r.resolveNodePathAt(name, r.nodeCwd, allowMissing, nodePathNoFollowFinal)
}

func (r *Runtime) resolveNodePathAt(name, cwd string, allowMissing bool, mode nodePathMode) (string, error) {
	if name == "" {
		return "", errors.New("path is empty")
	}
	resolved := filepath.FromSlash(name)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	if r.allowAllFileAccess {
		return resolved, nil
	}
	if !pathWithinBaseDir(r.nodeRoot, resolved) {
		return "", fmt.Errorf("path escapes BaseDir: %s", name)
	}
	pathToResolve := resolved
	if mode == nodePathNoFollowFinal {
		pathToResolve = filepath.Dir(resolved)
	}
	actual, err := filepath.EvalSymlinks(pathToResolve)
	if err == nil {
		if !pathWithinBaseDir(r.nodeRoot, actual) {
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
			if !pathWithinBaseDir(r.nodeRoot, actualParent) {
				return "", fmt.Errorf("path parent symlink escapes BaseDir: %s", name)
			}
			return resolved, nil
		}
		if !os.IsNotExist(parentErr) {
			return "", parentErr
		}
	}
}

func (r *Runtime) loadFSModule(vm *goja.Runtime, module *goja.Object) {
	exports := vm.NewObject()
	setSync := func(name string, function any) {
		_ = exports.Set(name, function)
	}

	setSync("readFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := r.resolveNodePath(call.Argument(0).String(), false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return encodeFSData(vm, data, fsEncoding(call.Argument(1)))
	})
	setSync("writeFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := r.resolveNodePath(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		data := buffer.Bytes(vm, call.Argument(1))
		if err := os.WriteFile(path, data, fsMode(call.Argument(2), 0o666)); err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("appendFileSync", func(call goja.FunctionCall) goja.Value {
		path, err := r.resolveNodePath(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, fsMode(call.Argument(2), 0o666))
		if err == nil {
			_, err = file.Write(buffer.Bytes(vm, call.Argument(1)))
			_ = file.Close()
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("existsSync", func(name string) bool {
		path, err := r.resolveNodePath(name, false)
		if err != nil {
			return false
		}
		_, err = os.Stat(path)
		return err == nil
	})
	setSync("accessSync", func(name string) {
		path, err := r.resolveNodePath(name, false)
		if err == nil {
			_, err = os.Stat(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("statSync", func(call goja.FunctionCall) goja.Value { return r.fsStat(vm, call.Argument(0).String(), true) })
	setSync("lstatSync", func(call goja.FunctionCall) goja.Value { return r.fsStat(vm, call.Argument(0).String(), false) })
	setSync("readdirSync", func(call goja.FunctionCall) goja.Value {
		path, err := r.resolveNodePath(call.Argument(0).String(), false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		entries, err := os.ReadDir(path)
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
		path, err := r.resolveNodePath(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		recursive := fsBooleanOption(call.Argument(1), "recursive")
		if recursive {
			err = os.MkdirAll(path, fsMode(call.Argument(1), 0o777))
		} else {
			err = os.Mkdir(path, fsMode(call.Argument(1), 0o777))
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("rmSync", func(call goja.FunctionCall) goja.Value {
		path, err := r.resolveNodePathNoFollow(call.Argument(0).String(), true)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		if fsBooleanOption(call.Argument(1), "recursive") {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil && !fsBooleanOption(call.Argument(1), "force") {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	})
	setSync("unlinkSync", func(name string) {
		path, err := r.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("rmdirSync", func(name string) {
		path, err := r.resolveNodePathNoFollow(name, false)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("renameSync", func(oldName, newName string) {
		oldPath, err := r.resolveNodePathNoFollow(oldName, false)
		if err == nil {
			var newPath string
			newPath, err = r.resolveNodePathNoFollow(newName, true)
			if err == nil {
				err = os.Rename(oldPath, newPath)
			}
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("copyFileSync", func(sourceName, targetName string) {
		if err := r.copyNodeFile(sourceName, targetName); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("realpathSync", func(name string) string {
		path, err := r.resolveNodePath(name, false)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("readlinkSync", func(name string) string {
		path, err := r.resolveNodePathNoFollow(name, false)
		if err == nil {
			path, err = os.Readlink(path)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("symlinkSync", func(targetName, linkName string) {
		target, err := r.resolveNodePath(targetName, true)
		if err == nil {
			var link string
			link, err = r.resolveNodePathNoFollow(linkName, true)
			if err == nil {
				err = os.Symlink(target, link)
			}
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("truncateSync", func(name string, size int64) {
		path, err := r.resolveNodePath(name, false)
		if err == nil {
			err = os.Truncate(path, size)
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("chmodSync", func(name string, mode uint32) {
		path, err := r.resolveNodePath(name, false)
		if err == nil {
			err = os.Chmod(path, os.FileMode(mode))
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("utimesSync", func(name string, accessTime, modifyTime any) {
		path, err := r.resolveNodePath(name, false)
		if err == nil {
			err = os.Chtimes(path, fsTime(accessTime), fsTime(modifyTime))
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("mkdtempSync", func(prefix string) string {
		path, err := r.resolveNodePath(prefix, true)
		if err == nil {
			path, err = os.MkdirTemp(filepath.Dir(path), filepath.Base(path)+"*")
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return path
	})
	setSync("openSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(r.fsOpen(vm, call)) })
	setSync("closeSync", func(fd int) {
		if err := r.fsClose(fd); err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("fstatSync", func(fd int) goja.Value {
		file, err := r.fsFile(fd)
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
		file, err := r.fsFile(fd)
		if err == nil {
			err = file.Sync()
		}
		if err != nil {
			panic(vm.NewGoError(err))
		}
	})
	setSync("readSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(r.fsRead(vm, call)) })
	setSync("writeSync", func(call goja.FunctionCall) goja.Value { return vm.ToValue(r.fsWrite(vm, call)) })

	for _, asyncName := range []string{
		"readFile", "writeFile", "appendFile", "access", "stat", "lstat", "readdir", "mkdir", "rm",
		"unlink", "rmdir", "rename", "copyFile", "realpath", "readlink", "symlink", "truncate",
		"chmod", "utimes", "mkdtemp", "open", "close", "fstat", "fsync", "read", "write",
	} {
		_ = exports.Set(asyncName, r.fsAsync(vm, asyncName))
	}
	_ = exports.Set("exists", func(call goja.FunctionCall) goja.Value {
		callback, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		name := call.Argument(0).String()
		cwd := r.nodeCwd
		go func() {
			path, err := r.resolveNodePathAt(name, cwd, false, nodePathFollowFinal)
			if err == nil {
				_, err = os.Stat(path)
			}
			r.loop.RunOnLoop(func(vm *goja.Runtime) { _, _ = callback(goja.Undefined(), vm.ToValue(err == nil)) })
		}()
		return goja.Undefined()
	})
	_ = exports.Set("constants", map[string]int{"F_OK": 0, "R_OK": 4, "W_OK": 2, "X_OK": 1, "COPYFILE_EXCL": 1})
	r.attachFSPromises(vm, exports)
	_ = module.Set("exports", exports)
}

func (r *Runtime) fsAsync(vm *goja.Runtime, name string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("callback must be a function"))
		}
		callback, ok := goja.AssertFunction(call.Arguments[len(call.Arguments)-1])
		if !ok {
			panic(vm.NewTypeError("callback must be a function"))
		}
		arguments := append([]goja.Value(nil), call.Arguments[:len(call.Arguments)-1]...)
		operation := r.prepareFSAsync(vm, name, arguments)
		go func() {
			result, err := operation.run()
			r.loop.RunOnLoop(func(vm *goja.Runtime) {
				r.runAsyncJob(vm, "fs."+name, func() error {
					if err != nil {
						_, callbackErr := callback(goja.Undefined(), vm.NewGoError(err))
						return callbackErr
					}
					values := append([]goja.Value{goja.Null()}, operation.values(vm, result)...)
					_, callbackErr := callback(goja.Undefined(), values...)
					return callbackErr
				})
			})
		}()
		return goja.Undefined()
	}
}

func (r *Runtime) attachFSPromises(vm *goja.Runtime, exports *goja.Object) {
	factory, err := vm.RunString(`(function(fs) {
		const methods = ["readFile","writeFile","appendFile","access","stat","lstat","readdir","mkdir","rm","unlink","rmdir","rename","copyFile","realpath","readlink","symlink","truncate","chmod","utimes","mkdtemp","close","fstat","fsync"];
		const promises = {};
		for (const name of methods) promises[name] = (...args) => new Promise((resolve, reject) => fs[name](...args, (error, value) => error ? reject(error) : resolve(value)));
		promises.read = (...args) => new Promise((resolve, reject) => fs.read(...args, (error, bytesRead, buffer) => error ? reject(error) : resolve({ bytesRead, buffer })));
		promises.write = (...args) => new Promise((resolve, reject) => fs.write(...args, (error, bytesWritten, buffer) => error ? reject(error) : resolve({ bytesWritten, buffer })));
		const fileHandle = (fd) => ({
			fd,
			close: () => promises.close(fd),
			stat: (...args) => promises.fstat(fd, ...args),
			sync: () => promises.fsync(fd),
			read: (...args) => promises.read(fd, ...args),
			write: (...args) => promises.write(fd, ...args)
		});
		promises.open = (...args) => new Promise((resolve, reject) => fs.open(...args, (error, fd) => error ? reject(error) : resolve(fileHandle(fd))));
		return promises;
	})`)
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

func (r *Runtime) fsStat(vm *goja.Runtime, name string, follow bool) goja.Value {
	var path string
	var err error
	if follow {
		path, err = r.resolveNodePath(name, false)
	} else {
		path, err = r.resolveNodePathNoFollow(name, false)
	}
	if err != nil {
		panic(vm.NewGoError(err))
	}
	var info os.FileInfo
	if follow {
		info, err = os.Stat(path)
	} else {
		info, err = os.Lstat(path)
	}
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
		if encoding == nil || goja.IsUndefined(encoding) || goja.IsNull(encoding) {
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

func (r *Runtime) copyNodeFile(sourceName, targetName string) error {
	source, err := r.resolveNodePath(sourceName, false)
	if err != nil {
		return err
	}
	target, err := r.resolveNodePath(targetName, true)
	if err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func (r *Runtime) fsOpen(vm *goja.Runtime, call goja.FunctionCall) int {
	path, err := r.resolveNodePath(call.Argument(0).String(), true)
	if err != nil {
		panic(vm.NewGoError(err))
	}
	fd, err := r.fsOpenPath(path, call.Argument(1).String(), fsMode(call.Argument(2), 0o666))
	if err != nil {
		panic(vm.NewGoError(err))
	}
	return fd
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

func (r *Runtime) fsFile(fd int) (*os.File, error) {
	r.fileMu.Lock()
	defer r.fileMu.Unlock()
	handle, ok := r.files[fd]
	if !ok {
		return nil, fmt.Errorf("bad file descriptor: %d", fd)
	}
	return handle.file, nil
}

func (r *Runtime) fsClose(fd int) error {
	r.fileMu.Lock()
	handle, ok := r.files[fd]
	if ok {
		delete(r.files, fd)
	}
	r.fileMu.Unlock()
	if !ok {
		return fmt.Errorf("bad file descriptor: %d", fd)
	}
	r.removeNodeResource(handle.resourceID)
	return handle.file.Close()
}

func (r *Runtime) fsRead(vm *goja.Runtime, call goja.FunctionCall) int {
	file, err := r.fsFile(int(call.Argument(0).ToInteger()))
	if err != nil {
		panic(vm.NewGoError(err))
	}
	object := call.Argument(1).ToObject(vm)
	data := buffer.Bytes(vm, object)
	offset := int(call.Argument(2).ToInteger())
	length := int(call.Argument(3).ToInteger())
	position := call.Argument(4)
	if offset < 0 || length < 0 || offset+length > len(data) {
		panic(vm.NewGoError(fmt.Errorf("buffer range is out of bounds")))
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

func (r *Runtime) fsWrite(vm *goja.Runtime, call goja.FunctionCall) int {
	file, err := r.fsFile(int(call.Argument(0).ToInteger()))
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
