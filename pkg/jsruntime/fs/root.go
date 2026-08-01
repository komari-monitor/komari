package fs

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/komari-monitor/komari/pkg/jsruntime/internal/filepathutil"
)

const rootPathEscapeText = "path escapes from parent"

func isRootPathEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), rootPathEscapeText)
}

func rootPathEscapeError(name string) error {
	return fmt.Errorf("path symlink escapes BaseDir: %s: %w", name, fs.ErrPermission)
}

var errFilesystemClosed = errors.New("JavaScript filesystem is closed")

const nodeAccessMask = 1 | 2 | 4

// filesystemFor returns the confined root that owns name together with the
// path relative to that root, or a nil root when access is unrestricted. The
// lifecycle read lock is held for the duration of the caller's operation; the
// returned unlock function must be called once when the operation completes.
// Callers must not hold any other Module lock.
func (m *Module) filesystemFor(name string) (*rootedDir, string, func(), error) {
	m.lifecycleMu.RLock()
	if m.closed {
		m.lifecycleMu.RUnlock()
		return nil, "", nil, errFilesystemClosed
	}
	if m.nodeFSRoot == nil {
		return nil, name, m.lifecycleMu.RUnlock, nil
	}
	if filepathutil.WithinBase(m.nodeRoot, name) {
		relative, err := filepathutil.RelativeToBase(m.nodeRoot, name)
		if err != nil {
			m.lifecycleMu.RUnlock()
			return nil, "", nil, err
		}
		return &rootedDir{path: m.nodeRoot, handle: m.nodeFSRoot}, relative, m.lifecycleMu.RUnlock, nil
	}
	for _, extra := range m.extraRoots {
		if extra.handle == nil {
			continue
		}
		if filepathutil.WithinBase(extra.path, name) {
			relative, err := filepathutil.RelativeToBase(extra.path, name)
			if err != nil {
				m.lifecycleMu.RUnlock()
				return nil, "", nil, err
			}
			entry := extra
			return &entry, relative, m.lifecycleMu.RUnlock, nil
		}
	}
	m.lifecycleMu.RUnlock()
	return nil, "", nil, fmt.Errorf("path escapes BaseDir: %s", name)
}

func nodePathError(operation, name string, err error) error {
	if err == nil {
		return nil
	}
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	if isRootPathEscape(err) {
		err = rootPathEscapeError(name)
	}
	return &os.PathError{Op: operation, Path: name, Err: err}
}

func (m *Module) nodeOpenFile(name string, flags int, mode os.FileMode) (*os.File, error) {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if root == nil {
		return os.OpenFile(name, flags, mode)
	}
	file, err := root.handle.OpenFile(relative, flags, mode.Perm())
	return file, nodePathError("open", name, err)
}

func (m *Module) nodeReadFile(name string) ([]byte, error) {
	file, err := m.nodeOpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	return data, nodePathError("read", name, err)
}

// ReadSource loads a CommonJS source file through the same rooted filesystem
// handle used by fs, preventing path validation and file opening from racing.
func (m *Module) ReadSource(name string) ([]byte, error) {
	file, err := m.nodeOpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, os.ErrNotExist
	}
	data, err := io.ReadAll(file)
	return data, nodePathError("read", name, err)
}

func (m *Module) nodeWriteFile(name string, data []byte, mode os.FileMode) error {
	file, err := m.nodeOpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Close())
}

func (m *Module) nodeStat(name string, follow bool) (os.FileInfo, error) {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if root == nil {
		if follow {
			return os.Stat(name)
		}
		return os.Lstat(name)
	}
	if follow {
		info, statErr := root.handle.Stat(relative)
		return info, nodePathError("stat", name, statErr)
	}
	info, statErr := root.handle.Lstat(relative)
	return info, nodePathError("lstat", name, statErr)
}

func (m *Module) nodeAccess(name string, mode int) error {
	if mode < 0 || mode&^nodeAccessMask != 0 {
		return nodePathError("access", name, syscall.EINVAL)
	}
	info, err := m.nodeStat(name, true)
	if err != nil {
		return nodePathError("access", name, err)
	}
	if err := checkNodeAccess(info, mode); err != nil {
		return nodePathError("access", name, err)
	}
	return nil
}

func (m *Module) nodeReadDir(name string) ([]fs.DirEntry, error) {
	file, err := m.nodeOpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	entries, err := file.ReadDir(-1)
	return entries, nodePathError("readdir", name, err)
}

func (m *Module) nodeMkdir(name string, mode os.FileMode, recursive bool) error {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return err
	}
	defer unlock()
	if root == nil {
		if recursive {
			return os.MkdirAll(name, mode)
		}
		return os.Mkdir(name, mode)
	}
	if recursive {
		err = root.handle.MkdirAll(relative, mode.Perm())
	} else {
		err = root.handle.Mkdir(relative, mode.Perm())
	}
	return nodePathError("mkdir", name, err)
}

func (m *Module) nodeRemove(name string, recursive bool) error {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return err
	}
	defer unlock()
	if root == nil {
		if recursive {
			return os.RemoveAll(name)
		}
		return os.Remove(name)
	}
	if recursive {
		err = root.handle.RemoveAll(relative)
	} else {
		err = root.handle.Remove(relative)
	}
	return nodePathError("remove", name, err)
}

// nodeRename requires both paths to live under the same confined root; a
// rename across the BaseDir/storage boundary is rejected.
func (m *Module) nodeRename(oldName, newName string) error {
	root, oldRelative, unlock, err := m.filesystemFor(oldName)
	if err != nil {
		return err
	}
	defer unlock()
	if root == nil {
		return os.Rename(oldName, newName)
	}
	newRelative, err := filepathutil.RelativeToBase(root.path, newName)
	if err != nil {
		return nodePathError("rename", newName, err)
	}
	return nodePathError("rename", oldName, root.handle.Rename(oldRelative, newRelative))
}

func (m *Module) nodeReadlink(name string) (string, error) {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return "", err
	}
	defer unlock()
	if root == nil {
		return os.Readlink(name)
	}
	target, err := root.handle.Readlink(relative)
	return target, nodePathError("readlink", name, err)
}

func (m *Module) nodeSymlink(targetName, linkName string) error {
	target := filepath.FromSlash(targetName)
	root, relativeLink, unlock, err := m.filesystemFor(linkName)
	if err != nil {
		return err
	}
	defer unlock()
	if root == nil {
		return os.Symlink(target, linkName)
	}
	effectiveTarget := target
	if !filepath.IsAbs(effectiveTarget) {
		effectiveTarget = filepath.Join(filepath.Dir(linkName), effectiveTarget)
	}
	effectiveTarget = filepath.Clean(effectiveTarget)
	if !m.withinAnyRoot(effectiveTarget) {
		return fmt.Errorf("path symlink escapes BaseDir: %s", targetName)
	}
	if filepath.IsAbs(target) {
		target, _ = filepath.Rel(filepath.Dir(linkName), effectiveTarget)
	}
	return nodePathError("symlink", linkName, root.handle.Symlink(target, relativeLink))
}

func (m *Module) nodeTruncate(name string, size int64) error {
	file, err := m.nodeOpenFile(name, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	err = file.Truncate(size)
	return errors.Join(nodePathError("truncate", name, err), file.Close())
}

func (m *Module) nodeChmod(name string, mode os.FileMode) error {
	file, err := m.nodeOpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	err = file.Chmod(mode)
	return errors.Join(nodePathError("chmod", name, err), file.Close())
}

func (m *Module) nodeChtimes(name string, accessTime, modifyTime time.Time) error {
	root, relative, unlock, err := m.filesystemFor(name)
	if err != nil {
		return err
	}
	defer unlock()
	if root == nil {
		return os.Chtimes(name, accessTime, modifyTime)
	}
	file, err := root.handle.OpenFile(relative, os.O_RDONLY, 0)
	if err != nil {
		return nodePathError("open", name, err)
	}
	err = setNodeFileTimes(file, accessTime, modifyTime)
	return errors.Join(nodePathError("utimes", name, err), file.Close())
}

func (m *Module) nodeMkdirTemp(prefix string) (string, error) {
	root, _, unlock, err := m.filesystemFor(prefix)
	if err != nil {
		return "", err
	}
	if root == nil {
		defer unlock()
		return os.MkdirTemp(filepath.Dir(prefix), filepath.Base(prefix)+"*")
	}
	unlock()
	parent, base := filepath.Dir(prefix), filepath.Base(prefix)
	for range 100 {
		var random [6]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := filepath.Join(parent, fmt.Sprintf("%s%x", base, random[:]))
		if err := m.nodeMkdir(name, 0o700, false); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("create temporary directory: too many collisions")
}
