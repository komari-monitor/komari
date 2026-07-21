// Package backup contains the shared upload preparation for backup restores.
package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var restoreMutex sync.Mutex

// SaveUploadedBackup validates a Komari backup and stages it for restoration
// during the next process startup.
func SaveUploadedBackup(file io.Reader, filename string) error {
	if !restoreMutex.TryLock() {
		return fmt.Errorf("another restore operation is already in progress")
	}
	defer restoreMutex.Unlock()

	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return fmt.Errorf("uploaded file must be a ZIP archive")
	}
	if err := os.MkdirAll("./data", 0755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	tempFile, err := os.CreateTemp("", "backup-upload-*.zip")
	if err != nil {
		return fmt.Errorf("create temporary backup: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(tempFile, file); err != nil {
		tempFile.Close()
		return fmt.Errorf("save uploaded backup: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close uploaded backup: %w", err)
	}

	reader, err := zip.OpenReader(tempPath)
	if err != nil {
		return fmt.Errorf("open backup archive: %w", err)
	}
	hasMarkup := false
	for _, entry := range reader.File {
		if entry.Name == "komari-backup-markup" {
			hasMarkup = true
			break
		}
	}
	reader.Close()
	if !hasMarkup {
		return fmt.Errorf("invalid backup file: missing komari-backup-markup file")
	}

	finalPath := filepath.Join(".", "data", "backup.zip")
	if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove previous backup: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err == nil {
		return nil
	}
	in, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("prepare backup file: %w", err)
	}
	defer in.Close()
	out, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("write backup file: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close backup file: %w", err)
	}
	return nil
}
