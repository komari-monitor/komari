package public

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var defaultDistFiles map[string][]byte

func loadEmbeddedDist() (map[string][]byte, error) {
	return decodeEmbeddedDist(embeddedDistArchive)
}

func decodeEmbeddedDist(archive []byte) (map[string][]byte, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("open zstd archive: %w", err)
	}
	defer decoder.Close()

	tarBytes, err := decoder.DecodeAll(archive, nil)
	if err != nil {
		return nil, fmt.Errorf("decode zstd archive: %w", err)
	}

	files := make(map[string][]byte)
	reader := tar.NewReader(bytes.NewReader(tarBytes))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		name := path.Clean(strings.ReplaceAll(header.Name, "\\", "/"))
		if header.Typeflag == tar.TypeDir && name == "." {
			continue
		}
		if name == "." || name == ".." ||
			strings.HasPrefix(name, "../") ||
			strings.HasPrefix(name, "/") ||
			strings.ContainsRune(name, '\x00') {
			return nil, fmt.Errorf("invalid embedded tar path %q", name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
			content, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("read tar entry %q: %w", name, err)
			}
			if _, exists := files[name]; exists {
				return nil, fmt.Errorf("duplicate tar entry %q", name)
			}
			files[name] = content
		default:
			return nil, fmt.Errorf("unsupported tar entry %q type %d", name, header.Typeflag)
		}
	}

	if _, ok := files[IndexFile]; !ok {
		return nil, fmt.Errorf("tar archive does not contain %q", IndexFile)
	}
	return files, nil
}
