package migrations

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLegacyTimestampMigrationDefaultsToUTC(t *testing.T) {
	t.Setenv("TZ", "")
	db := openTimestampMigrationDB(t)
	insertLegacyLogTime(t, db, "2026-07-17 09:30:00.1234567")

	if err := migrateLegacyTimestampColumns(db); err != nil {
		t.Fatalf("migrate timestamps: %v", err)
	}
	want := time.Date(2026, 7, 17, 9, 30, 0, 123456700, time.UTC)
	assertMigratedLogTime(t, db, want)
}

func TestLegacyTimestampMigrationUsesLegacyTZ(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	db := openTimestampMigrationDB(t)
	insertLegacyLogTime(t, db, "2026-07-17 09:30:00.123456789")

	if err := migrateLegacyTimestampColumns(db); err != nil {
		t.Fatalf("migrate timestamps: %v", err)
	}
	want := time.Date(2026, 7, 17, 1, 30, 0, 123456789, time.UTC)
	assertMigratedLogTime(t, db, want)
}

func TestParseLegacyTimestampAcceptsHistoricalEpochUnits(t *testing.T) {
	stamp := time.Date(2026, 7, 17, 1, 30, 0, 123456789, time.UTC)
	tests := map[string]struct {
		value string
		want  time.Time
	}{
		"seconds":      {value: fmt.Sprint(stamp.Unix()), want: stamp.Truncate(time.Second)},
		"milliseconds": {value: fmt.Sprint(stamp.UnixMilli()), want: stamp.Truncate(time.Millisecond)},
		"microseconds": {value: fmt.Sprint(stamp.UnixMicro()), want: stamp.Truncate(time.Microsecond)},
		"nanoseconds":  {value: fmt.Sprint(stamp.UnixNano()), want: stamp},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseLegacyTimestamp(test.value, time.UTC)
			if err != nil {
				t.Fatalf("parse legacy timestamp: %v", err)
			}
			if !got.Equal(test.want) {
				t.Fatalf("parsed timestamp = %s, want %s", got, test.want)
			}
		})
	}
}

func openTimestampMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Exec(`CREATE TABLE logs (id INTEGER PRIMARY KEY, time TIMESTAMP)`).Error; err != nil {
		t.Fatalf("create logs table: %v", err)
	}
	return db
}

func insertLegacyLogTime(t *testing.T, db *gorm.DB, value string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO logs (time) VALUES (?)`, value).Error; err != nil {
		t.Fatalf("insert legacy timestamp: %v", err)
	}
}

func assertMigratedLogTime(t *testing.T, db *gorm.DB, want time.Time) {
	t.Helper()
	var got time.Time
	if err := db.Raw(`SELECT time FROM logs LIMIT 1`).Scan(&got).Error; err != nil {
		t.Fatalf("read migrated timestamp: %v", err)
	}
	if !got.Equal(want) || got.Nanosecond() != want.Nanosecond() {
		t.Fatalf("migrated time = %s, want %s", got, want)
	}

	var raw string
	if err := db.Raw(`SELECT CAST(time AS TEXT) FROM logs LIMIT 1`).Scan(&raw).Error; err != nil {
		t.Fatalf("read raw migrated timestamp: %v", err)
	}
	if !strings.HasSuffix(raw, "+00:00") {
		t.Fatalf("stored timestamp %q has no explicit UTC offset", raw)
	}
}
