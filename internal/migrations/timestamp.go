package migrations

import (
	"database/sql"
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/komari-monitor/komari/internal/config"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const timestampUTCMigrationKey = "internal_timestamp_utc_migrated"

type timestampColumn struct {
	table  string
	column string
}

var legacyTimestampColumns = [...]timestampColumn{
	{table: "clients", column: "expired_at"},
	{table: "clients", column: "created_at"},
	{table: "clients", column: "updated_at"},
	{table: "client_infos", column: "expired_at"},
	{table: "client_infos", column: "created_at"},
	{table: "client_infos", column: "updated_at"},
	{table: "users", column: "created_at"},
	{table: "users", column: "updated_at"},
	{table: "sessions", column: "latest_online"},
	{table: "sessions", column: "expires"},
	{table: "sessions", column: "created_at"},
	{table: "logs", column: "time"},
	{table: "clipboards", column: "created_at"},
	{table: "clipboards", column: "updated_at"},
	{table: "offline_notifications", column: "last_notified"},
	{table: "load_notifications", column: "last_notified"},
	{table: "task_results", column: "finished_at"},
	{table: "task_results", column: "created_at"},
	{table: "records", column: "time"},
	{table: "records_long_term", column: "time"},
	{table: "gpu_records", column: "time"},
	{table: "ping_records", column: "time"},
	{table: "configs", column: "created_at"},
	{table: "configs", column: "updated_at"},
}

var legacyTimestampLayouts = [...]string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

type timestampRow struct {
	rowID int64
	value sql.NullString
}

// migrateLegacyTimestampColumns makes old offset-free SQLite values
// unambiguous before any current model scans them as time.Time.
func migrateLegacyTimestampColumns(db *gorm.DB) error {
	if timestampMigrationDone(db) {
		return nil
	}

	location := legacyTimestampLocation()
	var converted int64
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, target := range legacyTimestampColumns {
			if !tx.Migrator().HasTable(target.table) || !tx.Migrator().HasColumn(target.table, target.column) {
				continue
			}
			count, err := migrateTimestampColumn(tx, target, location)
			if err != nil {
				return err
			}
			converted += count
		}
		return nil
	})
	if err != nil {
		return err
	}
	if converted > 0 {
		logger.Infof("migration", "Converted %d legacy timestamp values to explicit UTC", converted)
	}
	return nil
}

func migrateTimestampColumn(db *gorm.DB, target timestampColumn, location *time.Location) (int64, error) {
	table := quoteSQLiteIdentifier(target.table)
	column := quoteSQLiteIdentifier(target.column)
	var converted int64
	var lastRowID int64

	for {
		query := fmt.Sprintf(
			"SELECT rowid, CAST(%s AS TEXT) FROM %s WHERE rowid > ? AND %s IS NOT NULL ORDER BY rowid LIMIT 1000",
			column, table, column,
		)
		rows, err := db.Raw(query, lastRowID).Rows()
		if err != nil {
			return converted, fmt.Errorf("read legacy timestamp %s.%s: %w", target.table, target.column, err)
		}

		batch := make([]timestampRow, 0, 1000)
		for rows.Next() {
			var row timestampRow
			if err := rows.Scan(&row.rowID, &row.value); err != nil {
				rows.Close()
				return converted, fmt.Errorf("scan legacy timestamp %s.%s: %w", target.table, target.column, err)
			}
			batch = append(batch, row)
			lastRowID = row.rowID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return converted, fmt.Errorf("iterate legacy timestamp %s.%s: %w", target.table, target.column, err)
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}

		for _, row := range batch {
			raw := strings.TrimSpace(row.value.String)
			if !row.value.Valid || raw == "" {
				continue
			}
			stamp, err := parseLegacyTimestamp(raw, location)
			if err != nil {
				return converted, fmt.Errorf("convert legacy timestamp %s.%s rowid=%d: %w", target.table, target.column, row.rowID, err)
			}
			update := fmt.Sprintf("UPDATE %s SET %s = ? WHERE rowid = ?", table, column)
			if err := db.Exec(update, stamp.UTC(), row.rowID).Error; err != nil {
				return converted, fmt.Errorf("write UTC timestamp %s.%s rowid=%d: %w", target.table, target.column, row.rowID, err)
			}
			converted++
		}
		if len(batch) < 1000 {
			break
		}
	}
	return converted, nil
}

func parseLegacyTimestamp(value string, location *time.Location) (time.Time, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return time.Time{}, fmt.Errorf("timestamp is empty")
	}
	if location == nil {
		location = time.UTC
	}
	if stamp, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return stamp.UTC(), nil
	}
	for _, layout := range legacyTimestampLayouts {
		if stamp, err := time.ParseInLocation(layout, raw, location); err == nil {
			return stamp.UTC(), nil
		}
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("unsupported legacy timestamp %q", value)
	}
	return legacyEpochTime(epoch), nil
}

func legacyEpochTime(value int64) time.Time {
	abs := value
	if abs < 0 {
		if abs == math.MinInt64 {
			return time.Unix(0, value).UTC()
		}
		abs = -abs
	}
	switch {
	case abs >= 1e17:
		return time.Unix(0, value).UTC()
	case abs >= 1e14:
		return time.Unix(0, value*int64(time.Microsecond)).UTC()
	case abs >= 1e11:
		return time.UnixMilli(value).UTC()
	default:
		return time.Unix(value, 0).UTC()
	}
}

// legacyTimestampLocation is intentionally migration-only. The old custom
// type used TZ and defaulted to UTC; current runtime code uses system time.Local.
func legacyTimestampLocation() *time.Location {
	name := strings.TrimSpace(os.Getenv("TZ"))
	if name == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		logger.Infof("migration", "Legacy timezone %q cannot be loaded; interpreting old timestamps as UTC: %v", name, err)
		return time.UTC
	}
	return location
}

func timestampMigrationDone(db *gorm.DB) bool {
	if !db.Migrator().HasTable(&appconfig.ConfigItem{}) || hasLegacyConfigTable(db) {
		return false
	}
	var item appconfig.ConfigItem
	if err := db.Where("key = ?", timestampUTCMigrationKey).First(&item).Error; err != nil {
		return false
	}
	return item.Value == "true"
}

func markTimestampMigrationDone(db *gorm.DB) error {
	if hasLegacyConfigTable(db) {
		return fmt.Errorf("new config item table is unavailable")
	}
	if !db.Migrator().HasTable(&appconfig.ConfigItem{}) {
		if err := db.AutoMigrate(&appconfig.ConfigItem{}); err != nil {
			return fmt.Errorf("create config item table: %w", err)
		}
	}
	item := appconfig.ConfigItem{Key: timestampUTCMigrationKey, Value: "true"}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&item).Error
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
