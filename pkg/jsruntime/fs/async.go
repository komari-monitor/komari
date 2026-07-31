package fs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
)

type fsAsyncOperation struct {
	run    func() (any, error)
	values func(*goja.Runtime, any) []goja.Value
}

type fsReadFileResult struct {
	data     []byte
	encoding string
}

type fsReadDirResult struct {
	entries   []fs.DirEntry
	withTypes bool
}

type fsReadResult struct {
	count  int
	data   []byte
	target goja.Value
	offset int
}

type fsWriteResult struct {
	count  int
	target goja.Value
}

func fsArgument(arguments []goja.Value, index int) goja.Value {
	if index < 0 || index >= len(arguments) {
		return goja.Undefined()
	}
	return arguments[index]
}

func fsNoValues(*goja.Runtime, any) []goja.Value { return nil }

func fsSingleValue(vm *goja.Runtime, result any) []goja.Value {
	return []goja.Value{vm.ToValue(result)}
}

func (m *Module) prepareFSAsync(vm *goja.Runtime, name string, arguments []goja.Value) fsAsyncOperation {
	cwd := m.Cwd()
	follow := func(path string, allowMissing bool) (string, error) {
		return m.resolveNodePathAt(path, cwd, allowMissing, nodePathFollowFinal)
	}
	noFollow := func(path string, allowMissing bool) (string, error) {
		return m.resolveNodePathAt(path, cwd, allowMissing, nodePathNoFollowFinal)
	}

	switch name {
	case "readFile":
		pathName := fsArgument(arguments, 0).String()
		encoding := fsEncoding(fsArgument(arguments, 1))
		return fsAsyncOperation{
			run: func() (any, error) {
				path, err := follow(pathName, false)
				if err != nil {
					return nil, err
				}
				data, err := m.nodeReadFile(path)
				return fsReadFileResult{data: data, encoding: encoding}, err
			},
			values: func(vm *goja.Runtime, result any) []goja.Value {
				value := result.(fsReadFileResult)
				return []goja.Value{encodeFSData(vm, value.data, value.encoding)}
			},
		}
	case "writeFile", "appendFile":
		pathName := fsArgument(arguments, 0).String()
		data := append([]byte(nil), buffer.Bytes(vm, fsArgument(arguments, 1))...)
		mode := fsMode(fsArgument(arguments, 2), 0o666)
		appendMode := name == "appendFile"
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, true)
			if err != nil {
				return nil, err
			}
			flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
			if appendMode {
				flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
			}
			file, err := m.nodeOpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			_, writeErr := file.Write(data)
			return nil, errors.Join(writeErr, file.Close())
		}, values: fsNoValues}
	case "access":
		pathName := fsArgument(arguments, 0).String()
		mode := fsAccessMode(fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeAccess(path, mode)
			}
			return nil, err
		}, values: fsNoValues}
	case "stat", "lstat":
		pathName := fsArgument(arguments, 0).String()
		isLstat := name == "lstat"
		return fsAsyncOperation{run: func() (any, error) {
			var path string
			var err error
			if isLstat {
				path, err = noFollow(pathName, false)
			} else {
				path, err = follow(pathName, false)
			}
			if err != nil {
				return nil, err
			}
			if isLstat {
				return m.nodeStat(path, false)
			}
			return m.nodeStat(path, true)
		}, values: func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{fsStatObject(vm, result.(os.FileInfo))}
		}}
	case "readdir":
		pathName := fsArgument(arguments, 0).String()
		withTypes := fsWithFileTypes(fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err != nil {
				return nil, err
			}
			entries, err := m.nodeReadDir(path)
			return fsReadDirResult{entries: entries, withTypes: withTypes}, err
		}, values: func(vm *goja.Runtime, result any) []goja.Value {
			value := result.(fsReadDirResult)
			entries := make([]any, 0, len(value.entries))
			for _, entry := range value.entries {
				if value.withTypes {
					entries = append(entries, fsDirent(vm, entry))
				} else {
					entries = append(entries, entry.Name())
				}
			}
			return []goja.Value{vm.ToValue(entries)}
		}}
	case "mkdir":
		pathName := fsArgument(arguments, 0).String()
		options := fsArgument(arguments, 1)
		recursive, mode := fsBooleanOption(options, "recursive"), fsMode(options, 0o777)
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(pathName, true)
			if err != nil {
				return nil, err
			}
			if recursive {
				err = m.nodeMkdir(path, mode, true)
			} else {
				err = m.nodeMkdir(path, mode, false)
			}
			return nil, err
		}, values: fsNoValues}
	case "rm":
		pathName := fsArgument(arguments, 0).String()
		options := fsArgument(arguments, 1)
		recursive, force := fsBooleanOption(options, "recursive"), fsBooleanOption(options, "force")
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(pathName, true)
			if err != nil {
				return nil, err
			}
			if recursive {
				err = m.nodeRemove(path, true)
			} else {
				err = m.nodeRemove(path, false)
			}
			if force && os.IsNotExist(err) {
				err = nil
			}
			return nil, err
		}, values: fsNoValues}
	case "unlink", "rmdir":
		pathName := fsArgument(arguments, 0).String()
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(pathName, false)
			if err == nil {
				err = m.nodeRemove(path, false)
			}
			return nil, err
		}, values: fsNoValues}
	case "rename":
		oldName, newName := fsArgument(arguments, 0).String(), fsArgument(arguments, 1).String()
		return fsAsyncOperation{run: func() (any, error) {
			oldPath, err := noFollow(oldName, false)
			if err != nil {
				return nil, err
			}
			newPath, err := noFollow(newName, true)
			if err == nil {
				err = m.nodeRename(oldPath, newPath)
			}
			return nil, err
		}, values: fsNoValues}
	case "copyFile":
		sourceName, targetName := fsArgument(arguments, 0).String(), fsArgument(arguments, 1).String()
		return fsAsyncOperation{run: func() (any, error) {
			source, err := follow(sourceName, false)
			if err != nil {
				return nil, err
			}
			target, err := follow(targetName, true)
			if err != nil {
				return nil, err
			}
			input, err := m.nodeOpenFile(source, os.O_RDONLY, 0)
			if err != nil {
				return nil, err
			}
			defer input.Close()
			output, err := m.nodeOpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
			if err != nil {
				return nil, err
			}
			_, copyErr := io.Copy(output, input)
			return nil, errors.Join(copyErr, output.Close())
		}, values: fsNoValues}
	case "realpath", "readlink":
		pathName := fsArgument(arguments, 0).String()
		readLink := name == "readlink"
		return fsAsyncOperation{run: func() (any, error) {
			if readLink {
				path, err := noFollow(pathName, false)
				if err != nil {
					return nil, err
				}
				return m.nodeReadlink(path)
			}
			return follow(pathName, false)
		}, values: fsSingleValue}
	case "symlink":
		targetName, linkName := fsArgument(arguments, 0).String(), fsArgument(arguments, 1).String()
		return fsAsyncOperation{run: func() (any, error) {
			target, err := follow(targetName, true)
			if err != nil {
				return nil, err
			}
			link, err := noFollow(linkName, true)
			if err == nil {
				err = m.nodeSymlink(target, link)
			}
			return nil, err
		}, values: fsNoValues}
	case "truncate":
		pathName, size := fsArgument(arguments, 0).String(), fsArgument(arguments, 1).ToInteger()
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeTruncate(path, size)
			}
			return nil, err
		}, values: fsNoValues}
	case "chmod":
		pathName := fsArgument(arguments, 0).String()
		mode := os.FileMode(fsArgument(arguments, 1).ToInteger())
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeChmod(path, mode)
			}
			return nil, err
		}, values: fsNoValues}
	case "utimes":
		pathName := fsArgument(arguments, 0).String()
		accessTime, modifyTime := fsTime(fsArgument(arguments, 1).Export()), fsTime(fsArgument(arguments, 2).Export())
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeChtimes(path, accessTime, modifyTime)
			}
			return nil, err
		}, values: fsNoValues}
	case "mkdtemp":
		prefix := fsArgument(arguments, 0).String()
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(prefix, true)
			if err != nil {
				return nil, err
			}
			return m.nodeMkdirTemp(path)
		}, values: fsSingleValue}
	case "open":
		pathName := fsArgument(arguments, 0).String()
		flags := fsArgument(arguments, 1).String()
		mode := fsMode(fsArgument(arguments, 2), 0o666)
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, true)
			if err != nil {
				return nil, err
			}
			return m.fsOpenPath(path, flags, mode)
		}, values: fsSingleValue}
	case "close":
		fd := int(fsArgument(arguments, 0).ToInteger())
		return fsAsyncOperation{run: func() (any, error) { return nil, m.fsClose(fd) }, values: fsNoValues}
	case "fstat":
		fd := int(fsArgument(arguments, 0).ToInteger())
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err != nil {
				return nil, err
			}
			return file.Stat()
		}, values: func(vm *goja.Runtime, result any) []goja.Value {
			return []goja.Value{fsStatObject(vm, result.(os.FileInfo))}
		}}
	case "fsync":
		fd := int(fsArgument(arguments, 0).ToInteger())
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err == nil {
				err = file.Sync()
			}
			return nil, err
		}, values: fsNoValues}
	case "read":
		fd := int(fsArgument(arguments, 0).ToInteger())
		target := fsArgument(arguments, 1)
		offset := int(fsArgument(arguments, 2).ToInteger())
		length := int(fsArgument(arguments, 3).ToInteger())
		positionValue := fsArgument(arguments, 4)
		hasPosition := !goja.IsNull(positionValue) && !goja.IsUndefined(positionValue)
		position := positionValue.ToInteger()
		bufferLength := len(buffer.Bytes(vm, target))
		if offset < 0 || length < 0 || offset+length > bufferLength {
			panic(vm.NewTypeError("buffer range is out of bounds"))
		}
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err != nil {
				return nil, err
			}
			data := make([]byte, length)
			var count int
			if hasPosition {
				count, err = file.ReadAt(data, position)
			} else {
				count, err = file.Read(data)
			}
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return fsReadResult{count: count, data: data[:count], target: target, offset: offset}, err
		}, values: func(vm *goja.Runtime, result any) []goja.Value {
			value := result.(fsReadResult)
			copy(buffer.Bytes(vm, value.target)[value.offset:], value.data)
			return []goja.Value{vm.ToValue(value.count), value.target}
		}}
	case "write":
		fd := int(fsArgument(arguments, 0).ToInteger())
		target := fsArgument(arguments, 1)
		data := append([]byte(nil), buffer.Bytes(vm, target)...)
		positionValue := fsArgument(arguments, 4)
		if _, isString := target.Export().(string); !isString {
			offset := int(fsArgument(arguments, 2).ToInteger())
			length := int(fsArgument(arguments, 3).ToInteger())
			if offset < 0 || length < 0 || offset+length > len(data) {
				panic(vm.NewTypeError("buffer range is out of bounds"))
			}
			data = data[offset : offset+length]
		} else {
			positionValue = fsArgument(arguments, 2)
		}
		hasPosition := !goja.IsNull(positionValue) && !goja.IsUndefined(positionValue)
		position := positionValue.ToInteger()
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err != nil {
				return nil, err
			}
			var count int
			if hasPosition {
				count, err = file.WriteAt(data, position)
			} else {
				count, err = file.Write(data)
			}
			return fsWriteResult{count: count, target: target}, err
		}, values: func(vm *goja.Runtime, result any) []goja.Value {
			value := result.(fsWriteResult)
			return []goja.Value{vm.ToValue(value.count), value.target}
		}}
	default:
		return fsAsyncOperation{run: func() (any, error) {
			return nil, fmt.Errorf("unsupported fs operation: %s", name)
		}, values: fsNoValues}
	}
}

func (m *Module) fsOpenPath(path, flags string, mode os.FileMode) (int, error) {
	file, err := m.nodeOpenFile(path, fsOpenFlags(flags), mode)
	if err != nil {
		return 0, err
	}
	return m.registerFile(file)
}
