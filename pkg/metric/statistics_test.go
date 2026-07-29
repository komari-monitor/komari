package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	plannerSeedEntities = 24
	plannerSeedMinutes  = 240
)

// seedRollupsForPlanner writes enough series and buckets that the planner has a
// real choice between the resolution index and the series-first plan. It
// returns the bucket window covering the seeded data.
func seedRollupsForPlanner(ctx context.Context, t *testing.T, store *Store) (time.Time, time.Time) {
	t.Helper()
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	if err := store.CreateMetric(ctx, Definition{
		Name:          "cpu.usage",
		Type:          TypeGauge,
		Unit:          "%",
		RetentionDays: 30,
	}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	points := make([]Point, 0, plannerSeedEntities*plannerSeedMinutes)
	for entity := 0; entity < plannerSeedEntities; entity++ {
		entityID := fmt.Sprintf("host-%02d", entity)
		for minute := 0; minute < plannerSeedMinutes; minute++ {
			points = append(points, Point{
				MetricName: "cpu.usage",
				EntityID:   entityID,
				Timestamp:  base.Add(time.Duration(minute) * time.Minute),
				Value:      float64(minute % 97),
			})
		}
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write batch: %v", err)
	}
	end := base.Add(time.Duration(plannerSeedMinutes) * time.Minute)
	if _, err := store.Compact(ctx, end.Add(2*time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return base, end
}

// capturingQuerier records the statement a Store method actually issues, so the
// planner assertion below runs against the live query text instead of a copy
// that silently rots when the query is rewritten.
type capturingQuerier struct {
	inner querier
	sql   string
	args  []any
}

func (c *capturingQuerier) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.sql = query
	c.args = args
	return c.inner.QueryContext(ctx, query, args...)
}

// captureRollupScan returns the statement scanRollupRowsBetween issues for a
// single-entity read, which is the query AggregateRollup runs on every
// dashboard refresh.
func captureRollupScan(ctx context.Context, t *testing.T, store *Store, start, end time.Time) (string, []any) {
	t.Helper()
	capture := &capturingQuerier{inner: store.reader()}
	if _, err := store.scanRollupRowsBetweenWith(ctx, capture, "cpu.usage", "host-01", nil,
		time.Minute, start.UnixMilli(), end.UnixMilli(), false); err != nil {
		t.Fatalf("scan rollup rows: %v", err)
	}
	if capture.sql == "" {
		t.Fatal("rollup scan issued no statement")
	}
	return capture.sql, capture.args
}

func explainPlan(ctx context.Context, t *testing.T, store *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := store.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}
	return plan
}

func rollupStatCount(ctx context.Context, t *testing.T, store *Store) int {
	t.Helper()
	var present int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sqlite_stat1'`).Scan(&present); err != nil {
		t.Fatalf("probe sqlite_stat1: %v", err)
	}
	if present == 0 {
		return 0
	}
	var rows int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_stat1 WHERE tbl = ?`, store.tables.rollups).Scan(&rows); err != nil {
		t.Fatalf("count rollup statistics: %v", err)
	}
	return rows
}

// TestUpdateStatisticsRecordsRollupPlan is the regression guard for the read
// path behind komari-monitor/komari#626. Reaching a rollup row costs two
// dictionary joins, so the planner only picks the series-first plan when it
// knows metric_series is tiny while metric_rollups is not. Without sqlite_stat1
// it falls back to the resolution index and scans a whole tier per call, which
// AggregateRollup multiplies by entity count times metric count on every
// dashboard refresh.
//
// The plan is asserted against the statement scanRollupRowsBetween actually
// issues, so rewriting that query cannot leave the guard passing against a
// shape the store no longer runs.
func TestUpdateStatisticsRecordsRollupPlan(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	start, end := seedRollupsForPlanner(ctx, t, store)
	query, args := captureRollupScan(ctx, t, store, start, end)

	if got := rollupStatCount(ctx, t, store); got != 0 {
		t.Fatalf("rollup statistics before update = %d, want 0", got)
	}

	// Establish that the unanalyzed database really does reach the rollup table
	// through the resolution index; otherwise the assertion after the refresh
	// would pass without proving anything.
	before := strings.Join(explainPlan(ctx, t, store, query, args...), "\n")
	if !strings.Contains(before, "resolution_bucket_idx") {
		t.Fatalf("unanalyzed plan does not scan the resolution tier, seed no longer reproduces the regression:\n%s", before)
	}

	if err := store.UpdateStatistics(ctx); err != nil {
		t.Fatalf("update statistics: %v", err)
	}

	if got := rollupStatCount(ctx, t, store); got == 0 {
		t.Fatal("update statistics recorded no sqlite_stat1 rows for the rollup table")
	}

	// The planner must now resolve the series before touching the rollup table.
	plan := explainPlan(ctx, t, store, query, args...)
	joined := strings.Join(plan, "\n")
	for _, step := range plan {
		if strings.Contains(step, store.tables.rollups) && strings.Contains(step, "resolution_bucket_idx") {
			t.Fatalf("planner still scans the rollup tier through the resolution index:\n%s", joined)
		}
	}
	if !strings.Contains(joined, "series_id=?") {
		t.Fatalf("planner does not seek the rollup table by series:\n%s", joined)
	}
}

// TestUpdateStatisticsIsBoundedAndRepeatable keeps the maintenance hook safe to
// call from the five-minute compaction schedule.
func TestUpdateStatisticsIsBoundedAndRepeatable(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seedRollupsForPlanner(ctx, t, store)

	for i := 0; i < 3; i++ {
		if err := store.UpdateStatistics(ctx); err != nil {
			t.Fatalf("update statistics run %d: %v", i, err)
		}
	}

	var limit int
	if err := store.db.QueryRowContext(ctx, "PRAGMA analysis_limit").Scan(&limit); err != nil {
		t.Fatalf("read analysis limit: %v", err)
	}
	if limit != 400 {
		t.Fatalf("analysis_limit = %d, want 400", limit)
	}
}

// TestUpdateStatisticsOnEmptyStore covers the fresh-install path, where Migrate
// has created the schema but no rollup row exists yet.
func TestUpdateStatisticsOnEmptyStore(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.UpdateStatistics(ctx); err != nil {
		t.Fatalf("update statistics on empty store: %v", err)
	}
}

func TestUpdateStatisticsClosedStore(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := store.UpdateStatistics(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("UpdateStatistics on closed store = %v, want %v", err, ErrClosed)
	}
}
