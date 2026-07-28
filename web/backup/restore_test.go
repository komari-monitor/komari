package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeTestArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close archive writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive file: %v", err)
	}
	return path
}

func TestValidateArchiveAcceptsLegacyRootLayout(t *testing.T) {
	archive := writeTestArchive(t, map[string]string{
		"komari.db":            "database",
		"theme/config.json":    "{}",
		"komari-backup-markup": "backup marker",
	})
	if err := ValidateArchive(archive); err != nil {
		t.Fatalf("ValidateArchive rejected legacy root layout: %v", err)
	}
}

func TestValidateArchiveRequiresMarkup(t *testing.T) {
	archive := writeTestArchive(t, map[string]string{"komari.db": "database"})
	if err := ValidateArchive(archive); err == nil {
		t.Fatal("ValidateArchive accepted archive without markup")
	}
}
