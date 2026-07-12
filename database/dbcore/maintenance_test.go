package dbcore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteFileSetSizeIncludesWALAndSHM(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "komari.db")
	files := map[string]int{
		databasePath:          11,
		databasePath + "-wal": 7,
		databasePath + "-shm": 5,
	}
	for path, size := range files {
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}

	got, err := sqliteFileSetSize("file:" + filepath.ToSlash(databasePath) + "?mode=rwc")
	if err != nil {
		t.Fatalf("sqliteFileSetSize: %v", err)
	}
	if got != 23 {
		t.Fatalf("size = %d, want 23", got)
	}
}

func TestSQLiteFileSetSizeRejectsNonFileDatabase(t *testing.T) {
	for _, dsn := range []string{":memory:", "file:metrics?MODE=MEMORY&cache=shared"} {
		if _, err := sqliteFileSetSize(dsn); err == nil {
			t.Fatalf("sqliteFileSetSize(%q) should fail", dsn)
		}
	}
}
