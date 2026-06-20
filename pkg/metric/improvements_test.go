package metric

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
)

// newMemStore opens an isolated in-memory store for tests.
//
// newMemStore 打开一个用于测试的隔离内存 Store。
func newMemStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), SQLite("file:imp-test?mode=memory&cache=shared"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestCreateMetricRejectsDuplicate verifies create-only metric semantics.
//
// TestCreateMetricRejectsDuplicate 验证 CreateMetric 遇到重复指标时会拒绝。
func TestCreateMetricRejectsDuplicate(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	def := Definition{Name: "dup.metric", Type: TypeGauge}
	if err := s.CreateMetric(ctx, def); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := s.CreateMetric(ctx, def)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists on duplicate, got %v", err)
	}
}

// TestUpsertMetricOverwrites verifies upsert updates mutable definition fields.
//
// TestUpsertMetricOverwrites 验证 UpsertMetric 会更新指标定义的可变字段。
func TestUpsertMetricOverwrites(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "m", Type: TypeGauge, Unit: "ms"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.UpsertMetric(ctx, Definition{Name: "m", Type: TypeCounter, Unit: "count"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.GetMetric(ctx, "m")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Type != TypeCounter || got.Unit != "count" {
		t.Fatalf("upsert did not overwrite: %#v", got)
	}
}

// TestTagFilterPushdownWithPaging verifies tag filtering happens before paging.
//
// TestTagFilterPushdownWithPaging 验证标签过滤会先于分页在 SQL 中执行。
func TestTagFilterPushdownWithPaging(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "req", Type: TypeCounter}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 6; i++ {
		env := "prod"
		if i%2 == 1 {
			env = "stage"
		}
		batch = append(batch, Point{
			MetricName: "req", EntityID: "n1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Value:     float64(i),
			Tags:      map[string]string{"env": env},
		})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 3 prod points (i=0,2,4); with Limit=2 the SQL LIMIT must apply to the
	// tag-filtered set, returning the first two prod points in time order.
	got, err := s.Query(ctx, Query{
		MetricName: "req", EntityID: "n1",
		Start: base, End: base.Add(time.Hour),
		Tags:  map[string]string{"env": "prod"},
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 paged prod points, got %d", len(got))
	}
	if got[0].Value != 0 || got[1].Value != 2 {
		t.Fatalf("unexpected paged values: %#v", got)
	}
}

// TestSQLAggregateMatchesInMemory compares SQL and in-memory aggregation.
//
// TestSQLAggregateMatchesInMemory 对比 SQL 下推聚合和内存聚合结果。
func TestSQLAggregateMatchesInMemory(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "g", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	vals := []float64{5, 15, 25, 35, 100, 200}
	for i, v := range vals {
		batch = append(batch, Point{
			MetricName: "g", EntityID: "n1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Value:     v,
		})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	q := AggregateQuery{
		Query:       Query{MetricName: "g", EntityID: "n1", Start: base, End: base.Add(time.Hour)},
		Aggregation: AggAvg,
		Interval:    3 * time.Minute,
	}
	// SQL pushdown path.
	sqlRes, err := s.Aggregate(ctx, q)
	if err != nil {
		t.Fatalf("sql aggregate: %v", err)
	}
	// In-memory reference over the same raw points.
	pts, err := s.Query(ctx, q.Query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	memRes, err := AggregatePoints(pts, q)
	if err != nil {
		t.Fatalf("mem aggregate: %v", err)
	}
	if len(sqlRes) != len(memRes) {
		t.Fatalf("bucket count mismatch: sql=%d mem=%d", len(sqlRes), len(memRes))
	}
	for i := range sqlRes {
		if !sqlRes[i].Bucket.Equal(memRes[i].Bucket) || sqlRes[i].Value != memRes[i].Value || sqlRes[i].Count != memRes[i].Count {
			t.Fatalf("bucket %d mismatch: sql=%#v mem=%#v", i, sqlRes[i], memRes[i])
		}
	}
}

// TestCounterRateHandlesReset verifies reset-aware counter rate calculation.
//
// TestCounterRateHandlesReset 验证计数器重置时速率计算仍然稳定。
func TestCounterRateHandlesReset(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// Counter goes 0 -> 10 -> reset -> 5; naive (last-first)/sec would give a
	// positive-but-wrong 5/30s. Correct counter rate sums positive deltas:
	// (10-0) + (5-0 after reset) = 15 over 30s = 0.5/s.
	pts := []Point{
		{Timestamp: base, Value: 0},
		{Timestamp: base.Add(10 * time.Second), Value: 10},
		{Timestamp: base.Add(20 * time.Second), Value: 0},
		{Timestamp: base.Add(30 * time.Second), Value: 5},
	}
	rate := counterRate(pts)
	if rate != 0.5 {
		t.Fatalf("expected reset-aware rate 0.5/s, got %v", rate)
	}
}

// TestAlignTimeNegativeTimestamp verifies pre-epoch bucket alignment.
//
// TestAlignTimeNegativeTimestamp 验证 Unix epoch 之前的时间也能正确对齐桶。
func TestAlignTimeNegativeTimestamp(t *testing.T) {
	interval := time.Minute
	// 30s before the epoch should align down to -60s, not up to 0.
	tm := time.Unix(-30, 0).UTC()
	got := alignTime(tm, interval)
	want := time.Unix(-60, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("alignTime negative: got %v want %v", got, want)
	}
}

// TestLatestReturnsMostRecent verifies latest-point ordering.
//
// TestLatestReturnsMostRecent 验证 Latest 返回最新采样点。
func TestLatestReturnsMostRecent(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "l", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := s.Write(ctx, Point{MetricName: "l", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Minute), Value: float64(i)}); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	got, err := s.Latest(ctx, "l", "n1", 2)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(got) != 2 || got[0].Value != 4 || got[1].Value != 3 {
		t.Fatalf("unexpected latest: %#v", got)
	}
}

// TestStatsDistinguishesNoDataFromUnknownMetric verifies stats error semantics.
//
// TestStatsDistinguishesNoDataFromUnknownMetric 验证 Stats 能区分无数据和未知指标。
func TestStatsDistinguishesNoDataFromUnknownMetric(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)

	// Unknown metric -> ErrNotFound.
	_, err := s.Stats(ctx, Query{MetricName: "nope", Start: base, End: base.Add(time.Hour)})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown metric: expected ErrNotFound, got %v", err)
	}

	// Known metric but empty window -> ErrNoData.
	if err := s.CreateMetric(ctx, Definition{Name: "known", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = s.Stats(ctx, Query{MetricName: "known", Start: base, End: base.Add(time.Hour)})
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("empty window: expected ErrNoData, got %v", err)
	}
}

// TestStdDevPopMatchesCalculateStats verifies population standard deviation.
//
// TestStdDevPopMatchesCalculateStats 验证总体标准差与统计摘要一致。
func TestStdDevPopMatchesCalculateStats(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var pts []Point
	for i, v := range []float64{10, 20, 30, 40, 50} {
		pts = append(pts, Point{Timestamp: base.Add(time.Duration(i) * time.Minute), Value: v})
	}
	st, err := CalculateStats(pts)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	got, _ := aggregateValue(pts, AggStdDev)
	if math.Abs(got-st.StdDev) > 1e-9 {
		t.Fatalf("AggStdDev %v != Stats.StdDev %v", got, st.StdDev)
	}
	// Known value: population stddev of 10..50 step 10 is sqrt(200) ~= 14.142135.
	if math.Abs(got-14.142135623730951) > 1e-9 {
		t.Fatalf("unexpected population stddev: %v", got)
	}
}

// TestAggregateStdDevSQLiteUsesMemoryPath verifies SQLite stddev fallback.
//
// TestAggregateStdDevSQLiteUsesMemoryPath 验证 SQLite 标准差聚合会回退到内存路径。
func TestAggregateStdDevSQLiteUsesMemoryPath(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "sd", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i, v := range []float64{10, 20, 30} {
		batch = append(batch, Point{MetricName: "sd", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Minute), Value: v})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, err := s.Aggregate(ctx, AggregateQuery{
		Query:       Query{MetricName: "sd", EntityID: "n1", Start: base, End: base.Add(time.Hour)},
		Aggregation: AggStdDev,
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("aggregate stddev: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(res))
	}
	// Population stddev of {10,20,30} = sqrt(200/3) ~= 8.16496.
	if math.Abs(res[0].Value-8.16496580927726) > 1e-9 {
		t.Fatalf("unexpected stddev bucket value: %v", res[0].Value)
	}
}

// TestSQLAggValueExprPushdownMatrix verifies aggregation pushdown support.
//
// TestSQLAggValueExprPushdownMatrix 验证各后端支持的聚合下推矩阵。
func TestSQLAggValueExprPushdownMatrix(t *testing.T) {
	cases := []struct {
		driver Driver
		agg    Aggregation
		ok     bool
	}{
		{DriverSQLite, AggAvg, true},
		{DriverSQLite, AggStdDev, false}, // no STDDEV_POP in sqlite -> memory
		{DriverSQLite, AggP95, false},    // no percentile in sqlite -> memory
		{DriverMySQL, AggStdDev, true},   // STDDEV_POP
		{DriverMySQL, AggP95, false},     // no portable continuous percentile
		{DriverPostgreSQL, AggStdDev, true},
		{DriverPostgreSQL, AggP50, true}, // percentile_cont
		{DriverPostgreSQL, AggP99, true},
		{DriverPostgreSQL, AggFirst, false}, // needs ordered raw series
	}
	for _, c := range cases {
		expr, ok := sqlAggValueExpr(c.driver, c.agg)
		if ok != c.ok {
			t.Fatalf("%s/%s: pushdown=%v want %v (expr=%q)", c.driver, c.agg, ok, c.ok, expr)
		}
		if ok && expr == "" {
			t.Fatalf("%s/%s: pushdown ok but empty expr", c.driver, c.agg)
		}
	}
}

// TestSQLiteReadPoolOpens verifies SQLite read-pool creation.
//
// TestSQLiteReadPoolOpens 验证 SQLite 只读连接池会按配置打开。
func TestSQLiteReadPoolOpens(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "rp")
	store, err := Open(ctx, SQLiteInDir(dir, WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("open with read pool: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if store.readDB == nil {
		t.Fatalf("expected a dedicated read pool to be opened")
	}
	if store.reader() != store.readDB {
		t.Fatalf("reader() should return the dedicated read pool")
	}
	// Round-trip a write (primary) and a read (read pool).
	if err := store.CreateMetric(ctx, Definition{Name: "rp", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	if err := store.Write(ctx, Point{MetricName: "rp", EntityID: "n1", Timestamp: base, Value: 7}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := store.Latest(ctx, "rp", "n1", 1)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(got) != 1 || got[0].Value != 7 {
		t.Fatalf("read pool round-trip failed: %#v", got)
	}
}

// TestMemoryDSNSkipsReadPool verifies memory SQLite skips read pools.
//
// TestMemoryDSNSkipsReadPool 验证内存 SQLite 不会打开独立读池。
func TestMemoryDSNSkipsReadPool(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:rp-mem?mode=memory&cache=shared", WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if store.readDB != nil {
		t.Fatalf("in-memory database must not open a second read pool")
	}
}

// TestWriteBatchAtomicAcrossChunks verifies chunked writes are atomic.
//
// TestWriteBatchAtomicAcrossChunks 验证分块批量写入仍保持整体原子性。
func TestWriteBatchAtomicAcrossChunks(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "atom", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// 1500 valid points, then one invalid (missing entity) to force a mid-batch
	// failure on the second chunk. The whole batch must roll back.
	var batch []Point
	for i := 0; i < 1500; i++ {
		batch = append(batch, Point{MetricName: "atom", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Millisecond), Value: float64(i)})
	}
	batch = append(batch, Point{MetricName: "atom", EntityID: "", Timestamp: base.Add(2 * time.Second), Value: 1}) // invalid
	if err := s.WriteBatch(ctx, batch); err == nil {
		t.Fatalf("expected WriteBatch to fail on invalid point")
	}
	// Nothing should have been committed.
	pts, err := s.Query(ctx, Query{MetricName: "atom", EntityID: "n1", Start: base, End: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(pts) != 0 {
		t.Fatalf("expected 0 committed points after rolled-back batch, got %d", len(pts))
	}
}

// TestAggregateBucketPagingSQLPath verifies bucket paging in SQL aggregation.
//
// TestAggregateBucketPagingSQLPath 验证 SQL 聚合路径按桶分页。
func TestAggregateBucketPagingSQLPath(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "bp", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// 6 points across 6 one-minute buckets, values 0..5.
	var batch []Point
	for i := 0; i < 6; i++ {
		batch = append(batch, Point{MetricName: "bp", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Minute), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	// AggAvg -> SQL pushdown path. One bucket per minute. Page: offset 1, limit 2.
	res, err := s.Aggregate(ctx, AggregateQuery{
		Query:        Query{MetricName: "bp", EntityID: "n1", Start: base, End: base.Add(time.Hour)},
		Aggregation:  AggAvg,
		Interval:     time.Minute,
		BucketLimit:  2,
		BucketOffset: 1,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 paged buckets, got %d", len(res))
	}
	// Buckets are values 0..5; offset 1 skips the value-0 bucket, so we expect 1,2.
	if res[0].Value != 1 || res[1].Value != 2 {
		t.Fatalf("unexpected paged bucket values: %#v", res)
	}
}

// TestAggregateBucketPagingMemoryPathMatchesSQL compares bucket paging paths.
//
// TestAggregateBucketPagingMemoryPathMatchesSQL 对比内存聚合和 SQL 聚合的桶分页语义。
func TestAggregateBucketPagingMemoryPathMatchesSQL(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "bp2", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 6; i++ {
		batch = append(batch, Point{MetricName: "bp2", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Minute), Value: float64(i * 10)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	q := Query{MetricName: "bp2", EntityID: "n1", Start: base, End: base.Add(time.Hour)}
	// AggMin (SQL pushdown) and AggP50 (memory fallback on sqlite) with identical
	// bucket paging must select the same bucket window (the same timestamps).
	sqlPaged, err := s.Aggregate(ctx, AggregateQuery{Query: q, Aggregation: AggMin, Interval: time.Minute, BucketLimit: 3, BucketOffset: 2})
	if err != nil {
		t.Fatalf("sql aggregate: %v", err)
	}
	memPaged, err := s.Aggregate(ctx, AggregateQuery{Query: q, Aggregation: AggP50, Interval: time.Minute, BucketLimit: 3, BucketOffset: 2})
	if err != nil {
		t.Fatalf("mem aggregate: %v", err)
	}
	if len(sqlPaged) != 3 || len(memPaged) != 3 {
		t.Fatalf("expected 3 buckets each, got sql=%d mem=%d", len(sqlPaged), len(memPaged))
	}
	for i := range sqlPaged {
		if !sqlPaged[i].Bucket.Equal(memPaged[i].Bucket) {
			t.Fatalf("bucket window mismatch at %d: sql=%v mem=%v", i, sqlPaged[i].Bucket, memPaged[i].Bucket)
		}
	}
	// Per-bucket single point, so min == p50 == the value.
	for i := range sqlPaged {
		if sqlPaged[i].Value != memPaged[i].Value {
			t.Fatalf("value mismatch at %d: sql=%v mem=%v", i, sqlPaged[i].Value, memPaged[i].Value)
		}
	}
}

// TestAggregateIgnoresRawPointLimit verifies raw limits do not affect aggregation.
//
// TestAggregateIgnoresRawPointLimit 验证原始点分页参数不会影响聚合输入。
func TestAggregateIgnoresRawPointLimit(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "ig", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 6; i++ {
		batch = append(batch, Point{MetricName: "ig", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Minute), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The embedded Query.Limit must be ignored for aggregation: a single 1-hour
	// bucket should aggregate ALL 6 points (count 6), not just 2.
	res, err := s.Aggregate(ctx, AggregateQuery{
		Query:       Query{MetricName: "ig", EntityID: "n1", Start: base, End: base.Add(time.Hour), Limit: 2},
		Aggregation: AggP95, // memory path, where the bug would have surfaced
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(res) != 1 || res[0].Count != 6 {
		t.Fatalf("embedded Limit leaked into aggregation: %#v", res)
	}
}

// TestJSONTagKeyWithSpecialChars verifies JSON tag keys with special characters.
//
// TestJSONTagKeyWithSpecialChars 验证包含特殊字符的标签键可正确查询。
func TestJSONTagKeyWithSpecialChars(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "tk", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// Keys containing a dot, a hyphen, a space, and a quote.
	tags := map[string]string{
		"region.zone": "ap-1",
		"a-b":         "yes",
		"with space":  "ok",
	}
	if err := s.Write(ctx, Point{MetricName: "tk", EntityID: "n1", Timestamp: base, Value: 1, Tags: tags}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A second point that should NOT match the filter below.
	if err := s.Write(ctx, Point{MetricName: "tk", EntityID: "n1", Timestamp: base.Add(time.Minute), Value: 2, Tags: map[string]string{"region.zone": "eu-1"}}); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	for _, tc := range []struct {
		key, val string
		want     int
	}{
		{"region.zone", "ap-1", 1}, // dotted key must match the flat key, not nested
		{"a-b", "yes", 1},          // hyphen
		{"with space", "ok", 1},    // space
		{"region.zone", "eu-1", 1}, // the other point
	} {
		got, err := s.Query(ctx, Query{
			MetricName: "tk", EntityID: "n1",
			Start: base.Add(-time.Second), End: base.Add(time.Hour),
			Tags: map[string]string{tc.key: tc.val},
		})
		if err != nil {
			t.Fatalf("query %s=%s: %v", tc.key, tc.val, err)
		}
		if len(got) != tc.want {
			t.Fatalf("tag %q=%q: expected %d points, got %d", tc.key, tc.val, tc.want, len(got))
		}
	}
}
