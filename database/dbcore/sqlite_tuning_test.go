package dbcore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/internal/sqlitetune"
)

func TestMainSQLiteConnectorAppliesBoundedSettings(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "komari.db")
	db, err := sqlitetune.Open(buildSQLiteDSN(databasePath), mainSQLiteOptions())
	if err != nil {
		t.Fatalf("open SQLite connector: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if got := sqlitePragmaInt(t, db, "busy_timeout"); got != mainSQLiteBusyTimeout.Milliseconds() {
		t.Fatalf("busy_timeout = %d, want %d", got, mainSQLiteBusyTimeout.Milliseconds())
	}
	if got := sqlitePragmaInt(t, db, "synchronous"); got != 1 {
		t.Fatalf("synchronous = %d, want NORMAL (1)", got)
	}
	if got := sqlitePragmaInt(t, db, "cache_size"); got != -mainSQLiteCacheSizeKB {
		t.Fatalf("cache_size = %d, want %d", got, -mainSQLiteCacheSizeKB)
	}
	if got := sqlitePragmaInt(t, db, "mmap_size"); got != 0 {
		t.Fatalf("mmap_size = %d, want 0", got)
	}
	if got := sqlitePragmaInt(t, db, "temp_store"); got != 1 {
		t.Fatalf("temp_store = %d, want FILE (1)", got)
	}
	if got := sqlitePragmaInt(t, db, "cache_spill"); got == 0 {
		t.Fatal("cache_spill is disabled")
	}
	if got := sqlitePragmaInt(t, db, "wal_autocheckpoint"); got != mainSQLiteWALAutoCheckpoint {
		t.Fatalf("wal_autocheckpoint = %d, want %d", got, mainSQLiteWALAutoCheckpoint)
	}
	if got := sqlitePragmaInt(t, db, "journal_size_limit"); got != mainSQLiteJournalSizeLimit {
		t.Fatalf("journal_size_limit = %d, want %d", got, mainSQLiteJournalSizeLimit)
	}

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want WAL", journalMode)
	}
}

func TestMainSQLiteAutoCheckpointKeepsWALBounded(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "komari.db")
	db, err := sqlitetune.Open(buildSQLiteDSN(databasePath), mainSQLiteOptions())
	if err != nil {
		t.Fatalf("open SQLite connector: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec("CREATE TABLE checkpoint_test (payload BLOB NOT NULL)"); err != nil {
		t.Fatalf("create checkpoint test table: %v", err)
	}

	// This produces well beyond one auto-checkpoint interval. The checkpoint
	// below reports the WAL frame count observed before its own work begins.
	for range 1024 {
		if _, err := db.Exec("INSERT INTO checkpoint_test (payload) VALUES (zeroblob(4096))"); err != nil {
			t.Fatalf("insert checkpoint test row: %v", err)
		}
	}

	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		t.Fatalf("inspect automatic checkpoint: %v", err)
	}
	if busy != 0 {
		t.Fatalf("automatic checkpoint left a busy WAL: %d", busy)
	}
	if logFrames >= 2*mainSQLiteWALAutoCheckpoint {
		t.Fatalf("WAL retained %d frames after %d-frame auto-checkpoint interval", logFrames, mainSQLiteWALAutoCheckpoint)
	}
}

func sqlitePragmaInt(t *testing.T, db *sql.DB, pragma string) int64 {
	t.Helper()

	var value int64
	if err := db.QueryRow("PRAGMA " + pragma).Scan(&value); err != nil {
		t.Fatalf("read %s: %v", pragma, err)
	}
	return value
}
