package metric

import (
	"context"
	"database/sql"
	"fmt"
)

// Migrate creates the normalized metric schema. Series, labels, and
// resolutions are interned once and every stored timestamp is Unix
// milliseconds. Exact samples live only in the Store's ten-minute memory
// window and are never persisted.
// Existing installations are rebuilt explicitly by the administrator guide,
// rather than being changed during startup.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	for _, statement := range s.normalizedSchemaStatements() {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.createNormalizedIndexes(ctx); err != nil {
		return err
	}
	if s.cfg.Driver == DriverSQLite {
		_, err := s.db.ExecContext(ctx, "PRAGMA optimize")
		return err
	}
	return nil
}

func (s *Store) normalizedSchemaStatements() []string {
	d := s.dialect
	jsonType := d.jsonType()
	pk := d.autoIncrementPrimaryKey()
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name VARCHAR(191) PRIMARY KEY, type VARCHAR(32) NOT NULL,
			unit VARCHAR(64) NOT NULL DEFAULT '', description TEXT NOT NULL,
			retention_days INTEGER NOT NULL DEFAULT 0, metadata %s NOT NULL,
			created_at_milli BIGINT NOT NULL, updated_at_milli BIGINT NOT NULL
		)`, s.tables.definitions, jsonType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s, labels_hash VARCHAR(64) NOT NULL, labels %s NOT NULL,
			UNIQUE(labels_hash)
		)`, s.tables.labels, pk, jsonType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL, tags %s NOT NULL,
			UNIQUE(metric_name, entity_id, tags_hash),
			FOREIGN KEY (metric_name) REFERENCES %s(name) ON DELETE CASCADE
		)`, s.tables.series, pk, jsonType, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s, resolution_milli BIGINT NOT NULL, UNIQUE(resolution_milli)
		)`, s.tables.resolutions, pk),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			series_id BIGINT NOT NULL, resolution_id BIGINT NOT NULL, label_id BIGINT NOT NULL, bucket_milli BIGINT NOT NULL,
			count BIGINT NOT NULL, sum DOUBLE PRECISION NOT NULL, sum_sq DOUBLE PRECISION NOT NULL,
			min_val DOUBLE PRECISION NOT NULL, max_val DOUBLE PRECISION NOT NULL,
			first_val DOUBLE PRECISION NOT NULL, first_ts_milli BIGINT NOT NULL,
			last_val DOUBLE PRECISION NOT NULL, last_ts_milli BIGINT NOT NULL,
			digest %s, created_at_milli BIGINT NOT NULL,
			UNIQUE(series_id, resolution_id, label_id, bucket_milli),
			FOREIGN KEY (series_id) REFERENCES %s(id) ON DELETE CASCADE,
			FOREIGN KEY (resolution_id) REFERENCES %s(id) ON DELETE CASCADE,
			FOREIGN KEY (label_id) REFERENCES %s(id) ON DELETE CASCADE
		)`, s.tables.rollups, d.blobType(), s.tables.series, s.tables.resolutions, s.tables.labels),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			state_key VARCHAR(64) PRIMARY KEY, phase VARCHAR(32) NOT NULL,
			upper_rowid BIGINT NOT NULL, cursor_rowid BIGINT NOT NULL,
			updated_at_milli BIGINT NOT NULL
		)`, s.tables.state),
	}
}

type normalizedIndex struct {
	name    string
	table   string
	columns string
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) normalizedIndexes() []normalizedIndex {
	return normalizedIndexesFor(s.cfg.TablePrefix, s.tables)
}

func normalizedIndexesFor(prefix string, tables tables) []normalizedIndex {
	return []normalizedIndex{
		{name: prefix + "series_metric_entity_idx", table: tables.series, columns: "metric_name, entity_id"},
		{name: prefix + "rollups_resolution_bucket_idx", table: tables.rollups, columns: "resolution_id, bucket_milli"},
	}
}

func (s *Store) createNormalizedIndexes(ctx context.Context) error {
	if s.cfg.Driver == DriverMySQL {
		for _, index := range s.normalizedIndexes() {
			exists, err := s.mysqlIndexExists(ctx, index.table, index.name)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX %s ON %s (%s)", index.name, index.table, index.columns)); err != nil {
				return err
			}
		}
		return nil
	}
	return s.createPortableNormalizedIndexes(ctx, s.db)
}

func (s *Store) createPortableNormalizedIndexes(ctx context.Context, exec sqlExecer) error {
	for _, index := range s.normalizedIndexes() {
		if _, err := exec.ExecContext(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", index.name, index.table, index.columns)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) dropNormalizedIndexes(ctx context.Context) error {
	for _, index := range s.normalizedIndexes() {
		if s.cfg.Driver == DriverMySQL {
			exists, err := s.mysqlIndexExists(ctx, index.table, index.name)
			if err != nil {
				return err
			}
			if exists {
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", index.table, index.name)); err != nil {
					return err
				}
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DROP INDEX IF EXISTS "+index.name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) mysqlIndexExists(ctx context.Context, table, index string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ? LIMIT 1`, table, index).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
