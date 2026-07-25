package metric

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestInspectSQLiteMigrationDetectsLegacyAndCurrentLayouts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	cfg := SQLite(path, WithMaxOpenConns(1))

	empty, err := InspectSQLiteMigration(ctx, cfg)
	if err != nil {
		t.Fatalf("inspect missing database: %v", err)
	}
	if empty.Required || empty.Layout != "empty" {
		t.Fatalf("unexpected missing database summary: %#v", empty)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	createLegacySQLiteMetricSchema(t, ctx, db)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO metric_definitions
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES ('cpu.usage', 'gauge', '', '', 1, '{}', ?, ?)`, now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO metric_points
		(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES ('cpu.usage', 'node-a', 'hash', ?, 42.5, '{}', '{}', ?)`, now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	legacy, err := InspectSQLiteMigration(ctx, cfg)
	if err != nil {
		t.Fatalf("inspect legacy database: %v", err)
	}
	if !legacy.Required || legacy.Layout != "legacy" || legacy.SourceRows != 1 {
		t.Fatalf("unexpected legacy summary: %#v", legacy)
	}

	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	current, err := InspectSQLiteMigration(ctx, cfg)
	if err != nil {
		t.Fatalf("inspect current database: %v", err)
	}
	if current.Required || current.Layout != "current" {
		t.Fatalf("unexpected current summary: %#v", current)
	}
}

func TestInspectSQLiteMigrationSkipsExternalDatabases(t *testing.T) {
	for _, driver := range []Driver{DriverMySQL, DriverPostgreSQL} {
		t.Run(string(driver), func(t *testing.T) {
			summary, err := InspectSQLiteMigration(context.Background(), Config{Driver: driver})
			if err != nil {
				t.Fatalf("inspect external database: %v", err)
			}
			if summary != (SQLiteMigrationSummary{}) {
				t.Fatalf("external database requested SQLite migration: %#v", summary)
			}
		})
	}
}

func TestSQLiteMigrationProgressReportsValidatedCompletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "progress.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	createLegacySQLiteMetricSchema(t, ctx, db)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO metric_definitions
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES ('cpu.usage', 'gauge', '', '', 1, '{}', ?, ?)`, now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO metric_points
		(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES ('cpu.usage', 'node-a', 'hash', ?, 42.5, '{}', '{}', ?)`, now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatalf("seed point: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	var snapshots []MigrationProgress
	store, err := Open(ctx, SQLite(path,
		WithMaxOpenConns(1),
		WithMigrationProgress(func(progress MigrationProgress) {
			snapshots = append(snapshots, progress)
		}),
	))
	if err != nil {
		t.Fatalf("migrate with progress: %v", err)
	}
	defer store.Close()

	phases := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		phases = append(phases, snapshot.Phase)
	}
	for _, phase := range []string{
		MigrationPhaseNormalizingPoints,
		MigrationPhaseEncodingPoints,
		MigrationPhaseValidating,
		MigrationPhaseCompleted,
	} {
		if !slices.Contains(phases, phase) {
			t.Fatalf("missing phase %q in %v", phase, phases)
		}
	}
	last := snapshots[len(snapshots)-1]
	if last.Phase != MigrationPhaseCompleted || last.Current != last.Total || last.Preserved != 1 {
		t.Fatalf("unexpected final progress: %#v", last)
	}
	points, err := store.Query(ctx, Query{
		MetricName: "cpu.usage",
		EntityID:   "node-a",
		Start:      now.Add(-time.Second),
		End:        now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("query migrated point: %v", err)
	}
	if len(points) != 1 || points[0].Value != 42.5 || !points[0].Timestamp.Equal(now) {
		t.Fatalf("migration changed point: %#v", points)
	}
}
