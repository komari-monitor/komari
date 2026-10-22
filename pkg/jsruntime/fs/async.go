package fs

import (
	"errors"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/buffer"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"io"
	"io/fs"
	"os"
)

type fsAsyncOperation struct {
	run    func() (any, error)
	values func(*moejs.Runtime, any) []moejs.Value
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
	target moejs.Value
	offset int
}
type fsWriteResult struct {
	count  int
	target moejs.Value
}

func fsArgument(arguments []moejs.Value, index int) moejs.Value {
	if index < 0 || index >= len(arguments) {
		return moejs.Undefined()
	}
	return arguments[index]
}
func fsNoValues(*moejs.Runtime, any) []moejs.Value {
	return nil
}
func fsSingleValue(vm *moejs.Runtime, result any) []moejs.Value {
	return []moejs.Value{native.From(vm, result)}
}
func (m *Module) prepareFSAsync(vm *moejs.Runtime, name string, arguments []moejs.Value) fsAsyncOperation {
	cwd := m.Cwd()
	follow := func(path string, allowMissing bool) (string, error) {
		return m.resolveNodePathAt(path, cwd, allowMissing, nodePathFollowFinal)
	}
	noFollow := func(path string, allowMissing bool) (string, error) {
		return m.resolveNodePathAt(path, cwd, allowMissing, nodePathNoFollowFinal)
	}
	switch name {
	case "readFile":
		pathName := native.String(vm, fsArgument(arguments, 0))
		encoding := fsEncoding(vm, fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err != nil {
				return nil, err
			}
			data, err := m.nodeReadFile(path)
			return fsReadFileResult{data: data, encoding: encoding}, err
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			value := result.(fsReadFileResult)
			return []moejs.Value{encodeFSData(vm, value.data, value.encoding)}
		}}
	case "writeFile", "appendFile":
		pathName := native.String(vm, fsArgument(arguments, 0))
		data := append([]byte(nil), buffer.Bytes(vm, fsArgument(arguments, 1))...)
		mode := fsMode(vm, fsArgument(arguments, 2), 0o666)
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
		pathName := native.String(vm, fsArgument(arguments, 0))
		mode := fsAccessMode(vm, fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeAccess(path, mode)
			}
			return nil, err
		}, values: fsNoValues}
	case "stat", "lstat":
		pathName := native.String(vm, fsArgument(arguments, 0))
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
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{native.From(vm, fsStatObject(vm, result.(os.FileInfo)))}
		}}
	case "readdir":
		pathName := native.String(vm, fsArgument(arguments, 0))
		withTypes := fsWithFileTypes(vm, fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err != nil {
				return nil, err
			}
			entries, err := m.nodeReadDir(path)
			return fsReadDirResult{entries: entries, withTypes: withTypes}, err
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			value := result.(fsReadDirResult)
			entries := make([]any, 0, len(value.entries))
			for _, entry := range value.entries {
				if value.withTypes {
					entries = append(entries, fsDirent(vm, entry))
				} else {
					entries = append(entries, entry.Name())
				}
			}
			return []moejs.Value{native.From(vm, entries)}
		}}
	case "mkdir":
		pathName := native.String(vm, fsArgument(arguments, 0))
		options := fsArgument(arguments, 1)
		recursive, mode := fsBooleanOption(vm, options, "recursive"), fsMode(vm, options, 0o777)
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
		pathName := native.String(vm, fsArgument(arguments, 0))
		options := fsArgument(arguments, 1)
		recursive, force := fsBooleanOption(vm, options, "recursive"), fsBooleanOption(vm, options, "force")
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
		pathName := native.String(vm, fsArgument(arguments, 0))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(pathName, false)
			if err == nil {
				err = m.nodeRemove(path, false)
			}
			return nil, err
		}, values: fsNoValues}
	case "rename":
		oldName, newName := native.String(vm, fsArgument(arguments, 0)), native.String(vm, fsArgument(arguments, 1))
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
		sourceName, targetName := native.String(vm, fsArgument(arguments, 0)), native.String(vm, fsArgument(arguments, 1))
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
		pathName := native.String(vm, fsArgument(arguments, 0))
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
		targetName, linkName := native.String(vm, fsArgument(arguments, 0)), native.String(vm, fsArgument(arguments, 1))
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
		pathName, size := native.String(vm, fsArgument(arguments, 0)), native.Int(vm, fsArgument(arguments, 1))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeTruncate(path, size)
			}
			return nil, err
		}, values: fsNoValues}
	case "chmod":
		pathName := native.String(vm, fsArgument(arguments, 0))
		mode := os.FileMode(native.Int(vm, fsArgument(arguments, 1)))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeChmod(path, mode)
			}
			return nil, err
		}, values: fsNoValues}
	case "utimes":
		pathName := native.String(vm, fsArgument(arguments, 0))
		accessTime, modifyTime := fsTime(native.Export(vm, fsArgument(arguments, 1))), fsTime(native.Export(vm, fsArgument(arguments, 2)))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, false)
			if err == nil {
				err = m.nodeChtimes(path, accessTime, modifyTime)
			}
			return nil, err
		}, values: fsNoValues}
	case "mkdtemp":
		prefix := native.String(vm, fsArgument(arguments, 0))
		return fsAsyncOperation{run: func() (any, error) {
			path, err := noFollow(prefix, true)
			if err != nil {
				return nil, err
			}
			return m.nodeMkdirTemp(path)
		}, values: fsSingleValue}
	case "open":
		pathName := native.String(vm, fsArgument(arguments, 0))
		flags := native.String(vm, fsArgument(arguments, 1))
		mode := fsMode(vm, fsArgument(arguments, 2), 0o666)
		return fsAsyncOperation{run: func() (any, error) {
			path, err := follow(pathName, true)
			if err != nil {
				return nil, err
			}
			return m.fsOpenPath(path, flags, mode)
		}, values: fsSingleValue}
	case "close":
		fd := int(native.Int(vm, fsArgument(arguments, 0)))
		return fsAsyncOperation{run: func() (any, error) {
			return nil, m.fsClose(fd)
		}, values: fsNoValues}
	case "fstat":
		fd := int(native.Int(vm, fsArgument(arguments, 0)))
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err != nil {
				return nil, err
			}
			return file.Stat()
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			return []moejs.Value{native.From(vm, fsStatObject(vm, result.(os.FileInfo)))}
		}}
	case "fsync":
		fd := int(native.Int(vm, fsArgument(arguments, 0)))
		return fsAsyncOperation{run: func() (any, error) {
			file, err := m.fsFile(fd)
			if err == nil {
				err = file.Sync()
			}
			return nil, err
		}, values: fsNoValues}
	case "read":
		fd := int(native.Int(vm, fsArgument(arguments, 0)))
		target := fsArgument(arguments, 1)
		offset := int(native.Int(vm, fsArgument(arguments, 2)))
		length := int(native.Int(vm, fsArgument(arguments, 3)))
		positionValue := fsArgument(arguments, 4)
		hasPosition := !positionValue.IsNull() && !positionValue.IsUndefined()
		position := native.Int(vm, positionValue)
		bufferLength := len(buffer.Bytes(vm, target))
		if offset < 0 || length < 0 || offset+length > bufferLength {
			panic(native.TypeError(vm, "buffer range is out of bounds"))
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
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			value := result.(fsReadResult)
			copy(buffer.Bytes(vm, value.target)[value.offset:], value.data)
			return []moejs.Value{native.From(vm, value.count), value.target}
		}}
	case "write":
		fd := int(native.Int(vm, fsArgument(arguments, 0)))
		target := fsArgument(arguments, 1)
		data := append([]byte(nil), buffer.Bytes(vm, target)...)
		positionValue := fsArgument(arguments, 4)
		if _, isString := native.Export(vm, target).(string); !isString {
			offset := int(native.Int(vm, fsArgument(arguments, 2)))
			length := int(native.Int(vm, fsArgument(arguments, 3)))
			if offset < 0 || length < 0 || offset+length > len(data) {
				panic(native.TypeError(vm, "buffer range is out of bounds"))
			}
			data = data[offset : offset+length]
		} else {
			positionValue = fsArgument(arguments, 2)
		}
		hasPosition := !positionValue.IsNull() && !positionValue.IsUndefined()
		position := native.Int(vm, positionValue)
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
		}, values: func(vm *moejs.Runtime, result any) []moejs.Value {
			value := result.(fsWriteResult)
			return []moejs.Value{native.From(vm, value.count), value.target}
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
