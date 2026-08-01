package plugin

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/komari-monitor/komari/database/models"
)

// InstallZip validates a plugin ZIP and extracts it into DataDir/<short>.
// The archive must contain komari-plugin.json at its root. Archive limits
// mirror the theme package format; path-traversal entries reject the whole
// package instead of being skipped. Reinstalling over a running plugin
// unloads it first and restores it to its persisted enabled state when the
// extraction succeeds.
func InstallZip(zipPath string) (models.Plugin, error) {
	var info models.Plugin
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return info, fmt.Errorf("failed to open ZIP file: %v", err)
	}
	defer r.Close()

	if err := validatePluginArchive(r.File); err != nil {
		return info, err
	}

	var manifest *zip.File
	for _, f := range r.File {
		if f.Name == manifestFile {
			manifest = f
			break
		}
	}
	if manifest == nil {
		return info, fmt.Errorf("plugin manifest %s not found, not a valid plugin package", manifestFile)
	}

	rc, err := manifest.Open()
	if err != nil {
		return info, fmt.Errorf("failed to read plugin manifest: %v", err)
	}
	configData, readErr := io.ReadAll(io.LimitReader(rc, maxPluginManifestSize+1))
	_ = rc.Close()
	if readErr != nil {
		return info, fmt.Errorf("failed to read plugin manifest: %v", readErr)
	}
	if len(configData) > maxPluginManifestSize {
		return info, fmt.Errorf("plugin manifest exceeds the %d byte limit", maxPluginManifestSize)
	}
	if err := json.Unmarshal(configData, &info); err != nil {
		return info, fmt.Errorf("invalid plugin manifest: %v", err)
	}
	if err := validateManifest(&info); err != nil {
		return info, err
	}
	if err := CheckKomariVersion(info.Komari); err != nil {
		return info, err
	}

	if err := global.unload(info.Short); err != nil && !errors.Is(err, errNotLoaded) {
		return info, fmt.Errorf("failed to unload running plugin %q before reinstall: %w", info.Short, err)
	}

	dir := filepath.Join(DataDir, info.Short)
	if err := os.RemoveAll(dir); err != nil {
		return info, fmt.Errorf("failed to remove existing plugin directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return info, fmt.Errorf("failed to create plugin directory: %v", err)
	}
	if err := extractPluginArchive(r.File, dir); err != nil {
		_ = os.RemoveAll(dir)
		return info, err
	}
	if _, err := os.Stat(filepath.Join(dir, info.Entry)); err != nil {
		_ = os.RemoveAll(dir)
		return info, fmt.Errorf("plugin entry %s does not exist", info.Entry)
	}
	for _, page := range info.Pages {
		if _, err := os.Stat(filepath.Join(dir, page.File)); err != nil {
			_ = os.RemoveAll(dir)
			return info, fmt.Errorf("plugin page %s does not exist", page.File)
		}
	}
	if global.stateStore().get(info.Short).Enabled {
		if err := global.restartPlugin(info.Short); err != nil {
			return info, fmt.Errorf("plugin reinstalled but reload failed: %w", err)
		}
	}
	return info, nil
}

func validatePluginArchive(files []*zip.File) error {
	if len(files) > maxPluginArchiveFiles {
		return fmt.Errorf("plugin archive has more than %d files", maxPluginArchiveFiles)
	}
	var total uint64
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		if file.UncompressedSize64 > maxPluginFileSize {
			return fmt.Errorf("plugin file %s exceeds the %d byte limit", file.Name, maxPluginFileSize)
		}
		total += file.UncompressedSize64
		if total > maxPluginExtractedSize {
			return fmt.Errorf("plugin archive exceeds the %d byte extraction limit", maxPluginExtractedSize)
		}
	}
	return nil
}

func extractPluginArchive(files []*zip.File, dir string) error {
	for _, f := range files {
		path := filepath.Join(dir, f.Name)
		if !withinDir(path, dir) {
			return fmt.Errorf("plugin archive contains an invalid path %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %v", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open archive file: %v", err)
		}
		outFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("failed to create file: %v", err)
		}
		_, copyErr := io.Copy(outFile, rc)
		_ = outFile.Close()
		_ = rc.Close()
		if copyErr != nil {
			return fmt.Errorf("failed to extract file: %v", copyErr)
		}
	}
	return nil
}

// withinDir reports whether path stays inside dir after cleaning.
func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}
