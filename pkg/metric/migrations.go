package metric

import (
	"context"
	"fmt"
)

// Migrate creates the tables and indexes used by the metric store.
//
// Migrate 创建 metric 包需要的表和索引。
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	d := s.dialect
	jsonType := d.jsonType()
	pk := d.autoIncrementPrimaryKey()
	blob := d.blobType()

	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name VARCHAR(191) PRIMARY KEY,
			type VARCHAR(32) NOT NULL,
			unit VARCHAR(64) NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			retention_days INTEGER NOT NULL DEFAULT 0,
			metadata %s NOT NULL,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL
		)`, s.tables.definitions, jsonType),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s,
			metric_name VARCHAR(191) NOT NULL,
			entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL,
			ts_nano BIGINT NOT NULL,
			value DOUBLE PRECISION NOT NULL,
			tags %s NOT NULL,
			labels %s NOT NULL,
			created_at BIGINT NOT NULL,
			UNIQUE(metric_name, entity_id, tags_hash, ts_nano)
		)`, s.tables.points, pk, jsonType, jsonType),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_metric_entity_time_idx ON %s (metric_name, entity_id, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_metric_time_idx ON %s (metric_name, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_entity_time_idx ON %s (entity_id, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id %s,
			metric_name VARCHAR(191) NOT NULL,
			entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL,
			tags %s NOT NULL,
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
			digest %s,
			created_at BIGINT NOT NULL,
			UNIQUE(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)
		)`, s.tables.rollups, pk, jsonType, blob),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rollups_lookup_idx ON %s (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)`, s.cfg.TablePrefix, s.tables.rollups),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rollups_res_time_idx ON %s (metric_name, resolution_nano, bucket_nano)`, s.cfg.TablePrefix, s.tables.rollups),
	}

	if s.cfg.Driver == DriverMySQL {
		statements = []string{
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				name VARCHAR(191) PRIMARY KEY,
				type VARCHAR(32) NOT NULL,
				unit VARCHAR(64) NOT NULL DEFAULT '',
				description TEXT NOT NULL,
				retention_days INT NOT NULL DEFAULT 0,
				metadata %s NOT NULL,
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`, s.tables.definitions, jsonType),
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id %s,
				metric_name VARCHAR(191) NOT NULL,
				entity_id VARCHAR(191) NOT NULL,
				tags_hash VARCHAR(64) NOT NULL,
				ts_nano BIGINT NOT NULL,
				value DOUBLE NOT NULL,
				tags %s NOT NULL,
				labels %s NOT NULL,
				created_at BIGINT NOT NULL,
				UNIQUE KEY uq_metric_entity_time (metric_name, entity_id, tags_hash, ts_nano),
				INDEX idx_metric_entity_time (metric_name, entity_id, ts_nano),
				INDEX idx_metric_time (metric_name, ts_nano),
				INDEX idx_entity_time (entity_id, ts_nano)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`, s.tables.points, pk, jsonType, jsonType),
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id %s,
				metric_name VARCHAR(191) NOT NULL,
				entity_id VARCHAR(191) NOT NULL,
				tags_hash VARCHAR(64) NOT NULL,
				tags %s NOT NULL,
				resolution_nano BIGINT NOT NULL,
				bucket_nano BIGINT NOT NULL,
				count BIGINT NOT NULL,
				sum DOUBLE NOT NULL,
				sum_sq DOUBLE NOT NULL,
				min_val DOUBLE NOT NULL,
				max_val DOUBLE NOT NULL,
				first_val DOUBLE NOT NULL,
				first_ts BIGINT NOT NULL,
				last_val DOUBLE NOT NULL,
				last_ts BIGINT NOT NULL,
				digest %s,
				created_at BIGINT NOT NULL,
				UNIQUE KEY uq_rollup (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano),
				INDEX idx_rollup_res_time (metric_name, resolution_nano, bucket_nano)
			) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`, s.tables.rollups, pk, jsonType, blob),
		}
	}

	if s.cfg.Driver == DriverPostgreSQL {
		statements = []string{
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				name VARCHAR(191) PRIMARY KEY,
				type VARCHAR(32) NOT NULL,
				unit VARCHAR(64) NOT NULL DEFAULT '',
				description TEXT NOT NULL DEFAULT '',
				retention_days INTEGER NOT NULL DEFAULT 0,
				metadata %s NOT NULL,
				created_at BIGINT NOT NULL,
				updated_at BIGINT NOT NULL
			)`, s.tables.definitions, jsonType),
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id %s,
				metric_name VARCHAR(191) NOT NULL,
				entity_id VARCHAR(191) NOT NULL,
				tags_hash VARCHAR(64) NOT NULL,
				ts_nano BIGINT NOT NULL,
				value DOUBLE PRECISION NOT NULL,
				tags %s NOT NULL,
				labels %s NOT NULL,
				created_at BIGINT NOT NULL,
				UNIQUE(metric_name, entity_id, tags_hash, ts_nano)
			)`, s.tables.points, pk, jsonType, jsonType),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_metric_entity_time_idx ON %s (metric_name, entity_id, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_metric_time_idx ON %s (metric_name, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_points_entity_time_idx ON %s (entity_id, ts_nano)`, s.cfg.TablePrefix, s.tables.points),
			fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				id %s,
				metric_name VARCHAR(191) NOT NULL,
				entity_id VARCHAR(191) NOT NULL,
				tags_hash VARCHAR(64) NOT NULL,
				tags %s NOT NULL,
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
				digest %s,
				created_at BIGINT NOT NULL,
				UNIQUE(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)
			)`, s.tables.rollups, pk, jsonType, blob),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rollups_lookup_idx ON %s (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)`, s.cfg.TablePrefix, s.tables.rollups),
			fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rollups_res_time_idx ON %s (metric_name, resolution_nano, bucket_nano)`, s.cfg.TablePrefix, s.tables.rollups),
		}
	}

	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
