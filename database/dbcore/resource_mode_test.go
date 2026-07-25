package dbcore

import (
	"slices"
	"testing"
)

func TestSQLiteResourcePragmasRestoreNormalMode(t *testing.T) {
	low := sqliteResourcePragmas(true)
	if !slices.Contains(low, "PRAGMA mmap_size = 0;") ||
		!slices.Contains(low, "PRAGMA cache_size = -8192;") ||
		!slices.Contains(low, "PRAGMA temp_store = FILE;") {
		t.Fatalf("low-resource pragmas are incomplete: %v", low)
	}

	normal := sqliteResourcePragmas(false)
	if !slices.Contains(normal, "PRAGMA mmap_size = 268435456;") ||
		!slices.Contains(normal, "PRAGMA cache_size = -65536;") ||
		!slices.Contains(normal, "PRAGMA temp_store = MEMORY;") {
		t.Fatalf("normal-mode pragmas do not restore defaults: %v", normal)
	}
}
