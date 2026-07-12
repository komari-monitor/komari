package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// MaintenanceAction identifies the backend-specific operation used to reclaim
// physical database space.
//
// MaintenanceAction 表示回收数据库物理空间时使用的后端专用操作。
type MaintenanceAction string

const (
	// MaintenanceVacuum is SQLite's VACUUM operation.
	MaintenanceVacuum MaintenanceAction = "vacuum"
	// MaintenanceOptimize is MySQL's OPTIMIZE TABLE operation.
	MaintenanceOptimize MaintenanceAction = "optimize"
	// MaintenanceVacuumFull is PostgreSQL's blocking VACUUM FULL operation.
	MaintenanceVacuumFull MaintenanceAction = "vacuum_full"
)

const (
	sqliteCheckpointSQL = "PRAGMA wal_checkpoint(TRUNCATE)"
	sqliteVacuumSQL     = "VACUUM"
)

// Driver returns the Store's configured database backend.
//
// Driver 返回 Store 配置的数据库后端。
func (s *Store) Driver() Driver {
	return s.cfg.Driver
}

// MaintenanceAction returns the physical-space reclamation operation used by
// the Store's backend.
//
// MaintenanceAction 返回当前后端用于回收物理空间的操作。
func (s *Store) MaintenanceAction() MaintenanceAction {
	return maintenanceActionFor(s.cfg.Driver)
}

// StorageSize returns the physical bytes occupied by this Store. SQLite
// includes the main database, WAL, and shared-memory files. Server backends
// include only the definitions, points, and rollups tables managed by Store.
//
// StorageSize 返回当前 Store 占用的物理字节数。SQLite 会统计主数据库、WAL
// 和共享内存文件；服务端数据库只统计 Store 管理的 definitions、points 和
// rollups 三张表。
func (s *Store) StorageSize(ctx context.Context) (int64, error) {
	s.maintenanceMu.RLock()
	defer s.maintenanceMu.RUnlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return 0, ErrClosed
	}

	if s.cfg.Driver == DriverSQLite {
		return s.sqliteStorageSize(ctx)
	}

	query, args, err := managedStorageSizeQuery(s.cfg.Driver, s.tables)
	if err != nil {
		return 0, err
	}
	var size int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&size); err != nil {
		return 0, fmt.Errorf("metric: query %s storage size: %w", s.cfg.Driver, err)
	}
	return size, nil
}

// ReclaimSpace performs the backend-specific blocking operation that returns
// unused database pages to the filesystem. It serializes against other
// maintenance calls and keeps Close from closing the pool mid-operation.
//
// ReclaimSpace 执行后端专用的阻塞式空间回收操作。该方法会与其他维护调用
// 串行执行，并阻止 Close 在操作过程中关闭连接池。
func (s *Store) ReclaimSpace(ctx context.Context) error {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.db == nil {
		return ErrClosed
	}

	switch s.cfg.Driver {
	case DriverSQLite:
		if err := sqliteCheckpoint(ctx, s.db); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, sqliteVacuumSQL); err != nil {
			return fmt.Errorf("metric: vacuum sqlite database: %w", err)
		}
		// VACUUM itself can populate the WAL; truncate it again so the reported
		// physical size reflects the completed reclamation.
		return sqliteCheckpoint(ctx, s.db)
	case DriverMySQL:
		query, err := managedReclaimQuery(s.cfg.Driver, s.tables)
		if err != nil {
			return err
		}
		return s.optimizeMySQLTables(ctx, query)
	case DriverPostgreSQL:
		query, err := managedReclaimQuery(s.cfg.Driver, s.tables)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("metric: vacuum PostgreSQL metric tables: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported driver %q", ErrInvalidArgument, s.cfg.Driver)
	}
}

func (s *Store) sqliteStorageSize(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return 0, fmt.Errorf("metric: list sqlite databases: %w", err)
	}

	var path string
	for rows.Next() {
		var sequence int
		var name, file string
		if err := rows.Scan(&sequence, &name, &file); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("metric: scan sqlite database path: %w", err)
		}
		if name == "main" {
			path = file
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("metric: list sqlite databases: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("metric: close sqlite database list: %w", err)
	}
	if path == "" {
		// SQLite reports an empty path for in-memory databases.
		return 0, nil
	}

	var size int64
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		switch {
		case err == nil:
			size += info.Size()
		case errors.Is(err, os.ErrNotExist):
		default:
			return 0, fmt.Errorf("metric: stat sqlite storage file %q: %w", name, err)
		}
	}
	return size, nil
}

func sqliteCheckpoint(ctx context.Context, db *sql.DB) error {
	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRowContext(ctx, sqliteCheckpointSQL).Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("metric: checkpoint sqlite WAL: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("metric: checkpoint sqlite WAL: database is busy (%d log frames, %d checkpointed)", logFrames, checkpointedFrames)
	}
	return nil
}

func (s *Store) optimizeMySQLTables(ctx context.Context, query string) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("metric: optimize MySQL metric tables: %w", err)
	}

	var resultErrors []error
	for rows.Next() {
		var table, operation, messageType, message string
		if err := rows.Scan(&table, &operation, &messageType, &message); err != nil {
			_ = rows.Close()
			return fmt.Errorf("metric: scan MySQL optimize result: %w", err)
		}
		if err := mysqlOptimizeResultError(table, messageType, message); err != nil {
			resultErrors = append(resultErrors, err)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("metric: read MySQL optimize results: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("metric: close MySQL optimize results: %w", err)
	}
	return errors.Join(resultErrors...)
}

func mysqlOptimizeResultError(table, messageType, message string) error {
	if !strings.EqualFold(strings.TrimSpace(messageType), "error") {
		return nil
	}
	table = strings.TrimSpace(table)
	if table == "" {
		table = "unknown table"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "unknown error"
	}
	return fmt.Errorf("metric: optimize MySQL table %s: %s", table, message)
}

func maintenanceActionFor(driver Driver) MaintenanceAction {
	switch driver {
	case DriverSQLite:
		return MaintenanceVacuum
	case DriverMySQL:
		return MaintenanceOptimize
	case DriverPostgreSQL:
		return MaintenanceVacuumFull
	default:
		return ""
	}
}

func managedStorageSizeQuery(driver Driver, t tables) (string, []any, error) {
	names := managedTableNames(t)
	switch driver {
	case DriverMySQL:
		return `SELECT COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME IN (?, ?, ?)`, stringsToAny(names), nil
	case DriverPostgreSQL:
		for i := range names {
			names[i] = strings.ToLower(names[i])
		}
		return `SELECT COALESCE(SUM(pg_total_relation_size(c.oid)), 0)
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND c.relname IN ($1, $2, $3)`, stringsToAny(names), nil
	default:
		return "", nil, fmt.Errorf("%w: storage-size query is unavailable for driver %q", ErrInvalidArgument, driver)
	}
}

func managedReclaimQuery(driver Driver, t tables) (string, error) {
	names := managedTableNames(t)
	switch driver {
	case DriverSQLite:
		return sqliteVacuumSQL, nil
	case DriverMySQL:
		for i := range names {
			names[i] = quoteMaintenanceIdentifier(driver, names[i])
		}
		return "OPTIMIZE TABLE " + strings.Join(names, ", "), nil
	case DriverPostgreSQL:
		for i := range names {
			names[i] = quoteMaintenanceIdentifier(driver, strings.ToLower(names[i]))
		}
		return "VACUUM (FULL, ANALYZE) " + strings.Join(names, ", "), nil
	default:
		return "", fmt.Errorf("%w: reclaim query is unavailable for driver %q", ErrInvalidArgument, driver)
	}
}

func managedTableNames(t tables) []string {
	return []string{t.definitions, t.points, t.rollups}
}

func quoteMaintenanceIdentifier(driver Driver, identifier string) string {
	if driver == DriverMySQL {
		return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for i := range values {
		args[i] = values[i]
	}
	return args
}
