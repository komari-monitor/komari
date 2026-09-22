package public

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var defaultDistCacheDir = "./cache/dist"

func PrepareDefaultDist() error {
	targetDir, err := filepath.Abs(defaultDistCacheDir)
	if err != nil {
		return fmt.Errorf("resolve embedded dist cache: %w", err)
	}
	if err := extractDistArchive(embeddedDistArchive, targetDir); err != nil {
		return err
	}
	defaultDistCacheDir = targetDir
	return nil
}

func extractDistArchive(archive []byte, targetDir string) error {
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("clear embedded dist cache: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create embedded dist cache: %w", err)
	}

	decoder, err := zstd.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open zstd archive: %w", err)
	}
	defer decoder.Close()

	reader := tar.NewReader(decoder)
	copyBuffer := make([]byte, 32*1024)
	files := make(map[string]struct{})
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		name := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if header.Typeflag == tar.TypeDir && name == "." {
			continue
		}
		if name == "." || name == ".." ||
			strings.HasPrefix(name, "../") ||
			strings.HasPrefix(name, "/") ||
			strings.ContainsRune(name, '\x00') {
			return fmt.Errorf("invalid embedded tar path %q", name)
		}

		target := filepath.Join(targetDir, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("create embedded tar directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if _, exists := files[name]; exists {
				return fmt.Errorf("duplicate tar entry %q", name)
			}
			files[name] = struct{}{}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("create embedded tar parent for %q: %w", name, err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("create embedded tar entry %q: %w", name, err)
			}
			_, copyErr := io.CopyBuffer(file, reader, copyBuffer)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract embedded tar entry %q: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close embedded tar entry %q: %w", name, closeErr)
			}
		default:
			return fmt.Errorf("unsupported tar entry %q type %d", name, header.Typeflag)
		}
	}

	if info, err := os.Stat(filepath.Join(targetDir, IndexFile)); err != nil || info.IsDir() {
		return fmt.Errorf("tar archive does not contain %q", IndexFile)
	}
	return nil
}
