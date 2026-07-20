package metric

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

const sqliteStorageVersion = 3

// migrateSQLiteStorageV3 normalizes repeated series identity into one compact
// dictionary. Compatibility views retain the original points/rollups columns,
// so every existing query and API response remains unchanged.
func (s *Store) migrateSQLiteStorageV3(ctx context.Context) error {
	pointType, err := sqliteObjectType(ctx, s.db, s.tables.points)
	if err != nil {
		return err
	}
	rollupType, err := sqliteObjectType(ctx, s.db, s.tables.rollups)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return fmt.Errorf("metric: enable SQLite incremental auto-vacuum: %w", err)
	}

	switch {
	case pointType == "" && rollupType == "":
		if err := s.createSQLiteStorageV3(ctx); err != nil {
			return err
		}
	case pointType == "table" && rollupType == "table":
		if err := s.migrateLegacySQLiteStorage(ctx); err != nil {
			return err
		}
	case pointType == "view" && rollupType == "view":
		if err := s.ensureSQLiteStorageV3(ctx); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"metric: inconsistent SQLite storage objects: %s=%q %s=%q",
			s.tables.points, pointType, s.tables.rollups, rollupType,
		)
	}

	s.sqliteStorageV3 = true
	var autoVacuum int
	if err := s.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		return fmt.Errorf("metric: inspect SQLite auto-vacuum mode: %w", err)
	}
	if autoVacuum != 2 {
		if err := s.fullSQLiteVacuum(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) createSQLiteStorageV3(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin SQLite V3 creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.createSQLiteCoreTables(ctx, tx); err != nil {
		return err
	}
	if err := s.createSQLiteV3PhysicalTables(ctx, tx); err != nil {
		return err
	}
	if err := s.createSQLiteV3CompatibilityObjects(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit SQLite V3 creation: %w", err)
	}
	return nil
}

func (s *Store) ensureSQLiteStorageV3(ctx context.Context) error {
	for _, table := range []string{s.tables.series, s.tables.pointValues, s.tables.rollupValues} {
		kind, err := sqliteObjectType(ctx, s.db, table)
		if err != nil {
			return err
		}
		if kind != "table" {
			return fmt.Errorf("metric: SQLite V3 table %s is missing", table)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin SQLite V3 verification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.createSQLiteCoreTables(ctx, tx); err != nil {
		return err
	}
	if err := s.createSQLiteV3CompatibilityObjects(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit SQLite V3 verification: %w", err)
	}
	return nil
}

func (s *Store) migrateLegacySQLiteStorage(ctx context.Context) error {
	log.Printf("metric: migrating SQLite metric storage to V%d", sqliteStorageVersion)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin SQLite V3 migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.createSQLiteCoreTables(ctx, tx); err != nil {
		return err
	}
	for _, table := range []string{s.tables.pointValues, s.tables.rollupValues, s.tables.series} {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("metric: reset SQLite V3 table %s: %w", table, err)
		}
	}
	if err := s.createSQLiteV3PhysicalTables(ctx, tx); err != nil {
		return err
	}

	for _, source := range []string{s.tables.points, s.tables.rollups} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT OR IGNORE INTO %s (metric_name, entity_id, tags_hash, tags)
			 SELECT metric_name, entity_id, tags_hash, tags FROM %s`,
			s.tables.series, source,
		)); err != nil {
			return fmt.Errorf("metric: build SQLite series dictionary from %s: %w", source, err)
		}
	}

	pointSourceRows, err := sqliteTableRowCountTx(ctx, tx, s.tables.points)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (series_id, ts_nano, value, labels, created_at)
		 SELECT s.id, p.ts_nano, p.value, p.labels, p.created_at
		 FROM %s p JOIN %s s
		   ON s.metric_name = p.metric_name AND s.entity_id = p.entity_id AND s.tags_hash = p.tags_hash`,
		s.tables.pointValues, s.tables.points, s.tables.series,
	)); err != nil {
		return fmt.Errorf("metric: copy SQLite raw points to V3: %w", err)
	}
	pointTargetRows, err := sqliteTableRowCountTx(ctx, tx, s.tables.pointValues)
	if err != nil {
		return err
	}
	if pointSourceRows != pointTargetRows {
		return fmt.Errorf("metric: SQLite V3 point count mismatch: source=%d target=%d", pointSourceRows, pointTargetRows)
	}

	rollupSourceRows, err := sqliteTableRowCountTx(ctx, tx, s.tables.rollups)
	if err != nil {
		return err
	}
	if err := s.copySQLiteRollupsV3(ctx, tx); err != nil {
		return err
	}
	rollupTargetRows, err := sqliteTableRowCountTx(ctx, tx, s.tables.rollupValues)
	if err != nil {
		return err
	}
	if rollupSourceRows != rollupTargetRows {
		return fmt.Errorf("metric: SQLite V3 rollup count mismatch: source=%d target=%d", rollupSourceRows, rollupTargetRows)
	}

	if _, err := tx.ExecContext(ctx, `DROP TABLE `+s.tables.points); err != nil {
		return fmt.Errorf("metric: replace legacy SQLite points table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE `+s.tables.rollups); err != nil {
		return fmt.Errorf("metric: replace legacy SQLite rollups table: %w", err)
	}
	if err := s.createSQLiteV3CompatibilityObjects(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit SQLite V3 migration: %w", err)
	}

	if err := s.fullSQLiteVacuum(ctx); err != nil {
		return err
	}
	log.Printf(
		"metric: migrated SQLite metric storage to V%d (%d points, %d rollups preserved)",
		sqliteStorageVersion, pointTargetRows, rollupTargetRows,
	)
	return nil
}

func (s *Store) copySQLiteRollupsV3(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT s.id, r.resolution_nano, r.bucket_nano, r.count, r.sum, r.sum_sq,
		        r.min_val, r.max_val, r.first_val, r.first_ts, r.last_val, r.last_ts,
		        r.digest, r.created_at
		 FROM %s r JOIN %s s
		   ON s.metric_name = r.metric_name AND s.entity_id = r.entity_id AND s.tags_hash = r.tags_hash`,
		s.tables.rollups, s.tables.series,
	))
	if err != nil {
		return fmt.Errorf("metric: read legacy SQLite rollups: %w", err)
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(
		`INSERT INTO %s
		 (series_id, resolution_nano, bucket_nano, count, sum, sum_sq, min_val, max_val,
		  first_val, first_ts, last_val, last_ts, digest, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.tables.rollupValues,
	))
	if err != nil {
		return fmt.Errorf("metric: prepare SQLite V3 rollup copy: %w", err)
	}
	defer stmt.Close()
	for rows.Next() {
		var seriesID, resolution, bucket, count, firstTS, lastTS, createdAt int64
		var sum, sumSq, minVal, maxVal, firstVal, lastVal float64
		var digest []byte
		if err := rows.Scan(
			&seriesID, &resolution, &bucket, &count, &sum, &sumSq, &minVal, &maxVal,
			&firstVal, &firstTS, &lastVal, &lastTS, &digest, &createdAt,
		); err != nil {
			return fmt.Errorf("metric: scan legacy SQLite rollup: %w", err)
		}
		if _, err := stmt.ExecContext(ctx,
			seriesID, resolution, bucket, count, sum, sumSq, minVal, maxVal,
			firstVal, firstTS, lastVal, lastTS, compressTDigestBlob(digest), createdAt,
		); err != nil {
			return fmt.Errorf("metric: copy legacy SQLite rollup: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("metric: read legacy SQLite rollups: %w", err)
	}
	return nil
}

func (s *Store) createSQLiteCoreTables(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name VARCHAR(191) PRIMARY KEY, type VARCHAR(32) NOT NULL,
			unit VARCHAR(64) NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
			retention_days INTEGER NOT NULL DEFAULT 0, metadata TEXT NOT NULL,
			created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL
		)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			metric_name VARCHAR(191) PRIMARY KEY,
			watermark_nano BIGINT NOT NULL, updated_at BIGINT NOT NULL
		)`, s.tables.watermarks),
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("metric: create SQLite core table: %w", err)
		}
	}
	return nil
}

func (s *Store) createSQLiteV3PhysicalTables(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id INTEGER PRIMARY KEY,
			metric_name VARCHAR(191) NOT NULL,
			entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL,
			tags TEXT NOT NULL,
			UNIQUE(metric_name, entity_id, tags_hash)
		)`, s.tables.series),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_series_entity_idx ON %s (entity_id)`, s.cfg.TablePrefix, s.tables.series),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			series_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			ts_nano BIGINT NOT NULL,
			value DOUBLE PRECISION NOT NULL,
			labels TEXT NOT NULL,
			created_at BIGINT NOT NULL,
			PRIMARY KEY(series_id, ts_nano)
		) WITHOUT ROWID`, s.tables.pointValues, s.tables.series),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			series_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			resolution_nano BIGINT NOT NULL,
			bucket_nano BIGINT NOT NULL,
			count BIGINT NOT NULL,
			sum DOUBLE PRECISION NOT NULL,
			sum_sq DOUBLE PRECISION NOT NULL,
			min_val DOUBLE PRECISION NOT NULL,
			max_val DOUBLE PRECISION NOT NULL,
			first_val DOUBLE PRECISION NOT NULL,
			first_ts BIGINT NOT NULL,
			last_val DOUBLE PRECISION NOT NULL,
			last_ts BIGINT NOT NULL,
			digest BLOB,
			created_at BIGINT NOT NULL,
			PRIMARY KEY(series_id, resolution_nano, bucket_nano)
		) WITHOUT ROWID`, s.tables.rollupValues, s.tables.series),
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("metric: create SQLite V3 physical table: %w", err)
		}
	}
	return nil
}

func (s *Store) createSQLiteV3CompatibilityObjects(ctx context.Context, tx *sql.Tx) error {
	pointInsertTrigger := s.cfg.TablePrefix + "points_insert_v3"
	pointDeleteTrigger := s.cfg.TablePrefix + "points_delete_v3"
	rollupInsertTrigger := s.cfg.TablePrefix + "rollups_insert_v3"
	rollupDeleteTrigger := s.cfg.TablePrefix + "rollups_delete_v3"
	statements := []string{
		fmt.Sprintf(`CREATE VIEW IF NOT EXISTS %s AS
		 SELECT p.series_id AS id, s.metric_name, s.entity_id, s.tags_hash,
		        p.ts_nano, p.value, s.tags, p.labels, p.created_at
		 FROM %s p JOIN %s s ON s.id = p.series_id`, s.tables.points, s.tables.pointValues, s.tables.series),
		fmt.Sprintf(`CREATE VIEW IF NOT EXISTS %s AS
		 SELECT r.series_id AS id, s.metric_name, s.entity_id, s.tags_hash, s.tags,
		        r.resolution_nano, r.bucket_nano, r.count, r.sum, r.sum_sq,
		        r.min_val, r.max_val, r.first_val, r.first_ts, r.last_val, r.last_ts,
		        r.digest, r.created_at
		 FROM %s r JOIN %s s ON s.id = r.series_id`, s.tables.rollups, s.tables.rollupValues, s.tables.series),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s INSTEAD OF INSERT ON %s BEGIN
		 INSERT INTO %s (metric_name, entity_id, tags_hash, tags)
		 VALUES (NEW.metric_name, NEW.entity_id, NEW.tags_hash, NEW.tags)
		 ON CONFLICT(metric_name, entity_id, tags_hash) DO UPDATE SET tags = excluded.tags;
		 INSERT INTO %s (series_id, ts_nano, value, labels, created_at)
		 SELECT id, NEW.ts_nano, NEW.value, NEW.labels, NEW.created_at FROM %s
		 WHERE metric_name = NEW.metric_name AND entity_id = NEW.entity_id AND tags_hash = NEW.tags_hash
		 ON CONFLICT(series_id, ts_nano) DO UPDATE SET
		   value = excluded.value, labels = excluded.labels, created_at = excluded.created_at;
		 END`, pointInsertTrigger, s.tables.points, s.tables.series, s.tables.pointValues, s.tables.series),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s INSTEAD OF DELETE ON %s BEGIN
		 DELETE FROM %s WHERE series_id = OLD.id AND ts_nano = OLD.ts_nano;
		 END`, pointDeleteTrigger, s.tables.points, s.tables.pointValues),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s INSTEAD OF INSERT ON %s BEGIN
		 INSERT INTO %s (metric_name, entity_id, tags_hash, tags)
		 VALUES (NEW.metric_name, NEW.entity_id, NEW.tags_hash, NEW.tags)
		 ON CONFLICT(metric_name, entity_id, tags_hash) DO UPDATE SET tags = excluded.tags;
		 INSERT INTO %s
		  (series_id, resolution_nano, bucket_nano, count, sum, sum_sq, min_val, max_val,
		   first_val, first_ts, last_val, last_ts, digest, created_at)
		 SELECT id, NEW.resolution_nano, NEW.bucket_nano, NEW.count, NEW.sum, NEW.sum_sq,
		        NEW.min_val, NEW.max_val, NEW.first_val, NEW.first_ts, NEW.last_val, NEW.last_ts,
		        NEW.digest, NEW.created_at FROM %s
		 WHERE metric_name = NEW.metric_name AND entity_id = NEW.entity_id AND tags_hash = NEW.tags_hash
		 ON CONFLICT(series_id, resolution_nano, bucket_nano) DO UPDATE SET
		   count = excluded.count, sum = excluded.sum, sum_sq = excluded.sum_sq,
		   min_val = excluded.min_val, max_val = excluded.max_val,
		   first_val = excluded.first_val, first_ts = excluded.first_ts,
		   last_val = excluded.last_val, last_ts = excluded.last_ts,
		   digest = excluded.digest, created_at = excluded.created_at;
		 END`, rollupInsertTrigger, s.tables.rollups, s.tables.series, s.tables.rollupValues, s.tables.series),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS %s INSTEAD OF DELETE ON %s BEGIN
		 DELETE FROM %s WHERE series_id = OLD.id AND resolution_nano = OLD.resolution_nano AND bucket_nano = OLD.bucket_nano;
		 END`, rollupDeleteTrigger, s.tables.rollups, s.tables.rollupValues),
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("metric: create SQLite V3 compatibility object: %w", err)
		}
	}
	return nil
}

func sqliteObjectType(ctx context.Context, db *sql.DB, name string) (string, error) {
	var kind string
	err := db.QueryRowContext(ctx,
		`SELECT type FROM sqlite_master WHERE name = ? AND type IN ('table', 'view')`, name,
	).Scan(&kind)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("metric: inspect SQLite object %s: %w", name, err)
	}
	return kind, nil
}

func sqliteTableRowCountTx(ctx context.Context, tx *sql.Tx, table string) (int64, error) {
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
		return 0, fmt.Errorf("metric: count SQLite rows in %s: %w", table, err)
	}
	return count, nil
}

func (s *Store) fullSQLiteVacuum(ctx context.Context) error {
	if err := sqliteCheckpoint(ctx, s.db); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, sqliteVacuumSQL); err != nil {
		return fmt.Errorf("metric: vacuum SQLite storage after V3 migration: %w", err)
	}
	return sqliteCheckpoint(ctx, s.db)
}

func (s *Store) incrementalSQLiteVacuum(ctx context.Context, pages int) error {
	if s.cfg.Driver != DriverSQLite || !s.sqliteStorageV3 || pages <= 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, pages)); err != nil {
		return fmt.Errorf("metric: incremental SQLite vacuum: %w", err)
	}
	return nil
}
