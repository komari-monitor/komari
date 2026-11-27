package trafficledger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestManager(t *testing.T) (*Manager, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:trafficledger_%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatalf("migrate clients: %v", err)
	}
	if err := db.Create(&models.Client{UUID: "node-1", Token: "token-node-1"}).Error; err != nil {
		t.Fatalf("create test client: %v", err)
	}
	manager := NewManager(db, "")
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize manager: %v", err)
	}
	return manager, db
}

func monthlyConfig() CycleConfig {
	return CycleConfig{Mode: ModeMonthly, ResetDay: 1, IntervalDays: 30, Timezone: "UTC"}
}

func reportAt(uuid string, at time.Time, up, down, uptime int64) v2.Report {
	return v2.Report{
		UUID: uuid, UpdatedAt: at, Uptime: uptime,
		Network: v2.NetworkReport{TotalUp: up, TotalDown: down},
	}
}

func reportPtr(uuid string, at time.Time, up, down, uptime int64) *v2.Report {
	r := reportAt(uuid, at, up, down, uptime)
	return &r
}

func TestManagerObserveCalibrateAndCloseCycle(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	cfg := monthlyConfig()
	cycleBoundary, _, err := CycleBounds(cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	oldCycleStart := time.Date(cycleBoundary.Year(), cycleBoundary.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	initialAt := now.Add(-2 * time.Minute)
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: cfg}, reportPtr("node-1", initialAt, 100, 20, 1000)); err != nil {
		t.Fatalf("switch to server cycle: %v", err)
	}
	if err := manager.ObserveReport(reportAt("node-1", initialAt.Add(time.Minute), 150, 50, 1060)); err != nil {
		t.Fatalf("observe report: %v", err)
	}
	got, ok := manager.Get("node-1")
	if !ok || got.AccumulatedUp != 50 || got.AccumulatedDown != 30 {
		t.Fatalf("ledger after report = %+v, want accumulated 50/30", got)
	}

	total := int64(160)
	result, err := manager.Calibrate(ctx, "node-1", CalibrateRequest{TargetTotal: &total})
	if err != nil {
		t.Fatalf("calibrate total: %v", err)
	}
	if result.BeforeUp != 50 || result.BeforeDown != 30 || result.AfterUp != 100 || result.AfterDown != 60 {
		t.Fatalf("calibration result = %+v, want 50/30 -> 100/60", result)
	}
	var persisted models.ClientTrafficLedger
	if err := db.First(&persisted, "uuid = ?", "node-1").Error; err != nil {
		t.Fatalf("load synchronously persisted calibration: %v", err)
	}
	if persisted.OffsetUp != 50 || persisted.OffsetDown != 30 || persisted.CalibratedAt == nil {
		t.Fatalf("persisted calibration = %+v", persisted)
	}

	var closed []ClosedCycleSnapshot
	manager.SetCycleCloseHook(func(tx *gorm.DB, snapshots []ClosedCycleSnapshot) error {
		var row models.ClientTrafficLedger
		if err := tx.First(&row, "uuid = ?", "node-1").Error; err != nil {
			return fmt.Errorf("read transitioned row inside close transaction: %w", err)
		}
		if row.AccumulatedUp != 50 || row.RawLastUp != 200 {
			return fmt.Errorf("hook did not observe the new row in its transaction: %+v", row)
		}
		closed = append(closed, snapshots...)
		return nil
	})
	entry := manager.entries["node-1"]
	entry.row.CycleStartAt, entry.row.CycleEndAt = &oldCycleStart, cycleBoundary
	boundaryReport := reportAt("node-1", now.Add(time.Minute), 200, 90, 1120)
	if err := manager.ObserveReport(boundaryReport); err != nil {
		t.Fatalf("observe across cycle boundary: %v", err)
	}
	got, _ = manager.Get("node-1")
	if got.AccumulatedUp != 50 || got.AccumulatedDown != 40 || got.OffsetUp != 0 || got.OffsetDown != 0 {
		t.Fatalf("new cycle state = %+v, want current-interval deltas and cleared offsets", got)
	}
	if err := manager.FlushDirty(ctx); err != nil {
		t.Fatalf("flush cycle transition: %v", err)
	}
	if len(closed) != 1 || closed[0].CloseReason != CloseReasonAutoRollover || closed[0].EffectiveUp != 100 || closed[0].EffectiveDown != 60 {
		t.Fatalf("closed cycle = %+v", closed)
	}
}

func TestOfflineEnableDoesNotSeedZeroBaseline(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	const oneTiB = int64(1 << 40)

	// Node is offline when server-side cycle is enabled (latestRaw == nil).
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, nil); err != nil {
		t.Fatalf("enable cycle while offline: %v", err)
	}
	snap, ok := manager.Get("node-1")
	if !ok || snap.HasBaseline || snap.RawLastUp != 0 || snap.RawLastDown != 0 {
		t.Fatalf("offline enable unexpectedly seeded baseline: %+v", snap)
	}

	// First real report carries 2 TiB OS uptime counter: must establish baseline with 0 delta.
	if err := manager.ObserveReport(reportAt("node-1", now, 2*oneTiB, 3*oneTiB, 500_000)); err != nil {
		t.Fatalf("first report after offline enable: %v", err)
	}
	snap, _ = manager.Get("node-1")
	if !snap.HasBaseline || snap.AccumulatedUp != 0 || snap.AccumulatedDown != 0 {
		t.Fatalf("first report dumped historical counter: %+v", snap)
	}

	// Subsequent report adds only the real delta.
	if err := manager.ObserveReport(reportAt("node-1", now.Add(time.Minute), 2*oneTiB+500, 3*oneTiB+800, 500_060)); err != nil {
		t.Fatalf("second report: %v", err)
	}
	snap, _ = manager.Get("node-1")
	if snap.AccumulatedUp != 500 || snap.AccumulatedDown != 800 {
		t.Fatalf("delta after baseline = %d/%d, want 500/800", snap.AccumulatedUp, snap.AccumulatedDown)
	}
}

func TestInPlaceConfigChangePreservesAccumulatedUsage(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", now, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", now.Add(time.Minute), 350, 500, 1060)); err != nil {
		t.Fatal(err)
	}
	delta := int64(100)
	if _, err := manager.Calibrate(ctx, "node-1", CalibrateRequest{DeltaUp: &delta}); err != nil {
		t.Fatal(err)
	}

	// Adjust reset day and timezone while staying in server mode.
	updated := CycleConfig{Mode: ModeMonthly, ResetDay: 15, IntervalDays: 30, Timezone: "Asia/Shanghai"}
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: updated}, reportPtr("node-1", now.Add(2*time.Minute), 350, 500, 1120)); err != nil {
		t.Fatalf("update cycle config in place: %v", err)
	}
	snap, _ := manager.Get("node-1")
	if snap.ResetDay != 15 || snap.Timezone != "Asia/Shanghai" {
		t.Fatalf("config not updated: %+v", snap)
	}
	if snap.AccumulatedUp != 250 || snap.AccumulatedDown != 300 || snap.OffsetUp != 100 {
		t.Fatalf("in-place config update wiped usage: %+v", snap)
	}
}

func TestClearOffsetsClearsCalibratedAtAndFlag(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", now, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", now.Add(time.Minute), 160, 280, 1060)); err != nil {
		t.Fatal(err)
	}
	targetUp, targetDown := int64(200), int64(300)
	if _, err := manager.Calibrate(ctx, "node-1", CalibrateRequest{TargetUp: &targetUp, TargetDown: &targetDown}); err != nil {
		t.Fatal(err)
	}
	if display := manager.ResolveDisplay("node-1", 0, 0); !display.Calibrated {
		t.Fatal("expected display.Calibrated=true after nonzero offset")
	}

	// Clearing offsets back to accumulated values must clear CalibratedAt and Calibrated.
	accUp, accDown := int64(60), int64(80)
	if _, err := manager.Calibrate(ctx, "node-1", CalibrateRequest{TargetUp: &accUp, TargetDown: &accDown}); err != nil {
		t.Fatal(err)
	}
	snap, _ := manager.Get("node-1")
	if snap.OffsetUp != 0 || snap.OffsetDown != 0 || snap.CalibratedAt != nil {
		t.Fatalf("clearing offsets left stale calibration state: %+v", snap)
	}
	if display := manager.ResolveDisplay("node-1", 0, 0); display.Calibrated {
		t.Fatal("expected display.Calibrated=false after clearing offsets")
	}
}

func TestManagerCycleFlipAfterLongGapRebaselines(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	initialAt := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", initialAt, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	janStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	febStart := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	entry.row.CycleStartAt, entry.row.CycleEndAt = &janStart, &febStart

	if err := manager.ObserveReport(reportAt("node-1", initialAt.Add(time.Minute), 150, 250, 1060)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", time.Date(2024, 2, 1, 0, 10, 0, 0, time.UTC), 900, 1200, 1600)); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.Get("node-1")
	if got.AccumulatedUp != 0 || got.AccumulatedDown != 0 {
		t.Fatalf("long-gap cycle flip accumulated old traffic: up=%d down=%d", got.AccumulatedUp, got.AccumulatedDown)
	}
	if got.RawLastUp != 900 || got.RawLastDown != 1200 || !got.HasBaseline {
		t.Fatalf("new raw baseline not captured: %+v", got)
	}
}

func TestManagerObserveUsesUptimeForRebootAndJitter(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	const oneGiB = int64(1 << 30)
	at := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", at, 300*oneGiB, 200*oneGiB, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", at.Add(time.Minute), 100*oneGiB, 100*oneGiB, 10)); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.Get("node-1")
	if got.AccumulatedUp != 100*oneGiB || got.AccumulatedDown != 100*oneGiB {
		t.Fatalf("uptime reboot delta = %d/%d, want 100GiB/100GiB", got.AccumulatedUp, got.AccumulatedDown)
	}
	if err := manager.ObserveReport(reportAt("node-1", at.Add(2*time.Minute), 100*oneGiB-1, 100*oneGiB-1, 70)); err != nil {
		t.Fatal(err)
	}
	got, _ = manager.Get("node-1")
	if got.AccumulatedUp != 100*oneGiB || got.AccumulatedDown != 100*oneGiB {
		t.Fatalf("counter jitter changed usage: %d/%d", got.AccumulatedUp, got.AccumulatedDown)
	}
}

func TestManagerObserveMonotonicCounterAcrossAgentRestart(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	const oneGiB = int64(1 << 30)
	at := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", at, 200*oneGiB, 150*oneGiB, 864000)); err != nil {
		t.Fatal(err)
	}
	// Agent restarts (uptime collapses) but persists its cumulative counter and
	// resumes adding on top of it: only the true delta may be counted, never the
	// full historical counter.
	if err := manager.ObserveReport(reportAt("node-1", at.Add(time.Minute), 200*oneGiB+5<<20, 150*oneGiB+3<<20, 15)); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.Get("node-1")
	if got.AccumulatedUp != 5<<20 || got.AccumulatedDown != 3<<20 {
		t.Fatalf("cross-restart monotonic delta = %d/%d, want 5MiB/3MiB", got.AccumulatedUp, got.AccumulatedDown)
	}
}

func TestFlushDirtyRollsOfflineCyclesAndClearsBaseline(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", time.Now().UTC(), 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	oldStart := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	oldEnd := time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC)
	entry.row.CycleStartAt, entry.row.CycleEndAt = &oldStart, &oldEnd
	entry.row.AccumulatedUp, entry.row.AccumulatedDown = 700, 900
	entry.row.HasBaseline = true
	oldReportAt := oldEnd.Add(-time.Minute)
	entry.row.RawLastAt = &oldReportAt

	var reason string
	manager.SetCycleCloseHook(func(_ *gorm.DB, snapshots []ClosedCycleSnapshot) error {
		if len(snapshots) != 1 {
			t.Fatalf("hook received %d snapshots, want 1", len(snapshots))
		}
		reason = snapshots[0].CloseReason
		return nil
	})
	if err := manager.FlushDirty(ctx); err != nil {
		t.Fatalf("flush offline cycle: %v", err)
	}
	got, _ := manager.Get("node-1")
	if got.AccumulatedUp != 0 || got.AccumulatedDown != 0 || got.HasBaseline {
		t.Fatalf("offline rollover state = %+v, want empty cycle and cleared baseline", got)
	}
	if reason != CloseReasonAutoRollover {
		t.Fatalf("close reason = %q, want auto_rollover", reason)
	}
	var persisted models.ClientTrafficLedger
	if err := db.First(&persisted, "uuid = ?", "node-1").Error; err != nil {
		t.Fatal(err)
	}
	if persisted.HasBaseline || persisted.AccumulatedUp != 0 {
		t.Fatalf("offline rollover was not persisted: %+v", persisted)
	}
}

func TestResolveDisplayLazilyRollsExpiredCycle(t *testing.T) {
	manager, _ := newTestManager(t)
	now := time.Now().UTC()
	if err := manager.UpdateConfig(context.Background(), "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", now.Add(-2*time.Second), 10, 20, 100)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	oldStart := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	oldEnd := time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC)
	entry.row.CycleStartAt, entry.row.CycleEndAt = &oldStart, &oldEnd
	entry.row.AccumulatedUp, entry.row.AccumulatedDown = 123, 456
	entry.row.HasBaseline = true

	// Online node (reported 2s ago) rolling via ResolveDisplay must keep baseline!
	display := manager.ResolveDisplay("node-1", 0, 0)
	if display.Up != 0 || display.Down != 0 || display.CycleStartAt == nil || !display.CycleStartAt.After(oldStart) {
		t.Fatalf("display after lazy rollover = %+v", display)
	}
	if !entry.row.HasBaseline || !entry.dirty || len(manager.closures) != 1 {
		t.Fatalf("online lazy rollover should keep baseline and queue closure: entry=%+v closures=%d", entry, len(manager.closures))
	}
}

func TestSetDefaultTimezoneRefreshesInheritedCycleWindow(t *testing.T) {
	manager, _ := newTestManager(t)
	if err := manager.SetDefaultTimezone("UTC"); err != nil {
		t.Fatal(err)
	}
	cfg := CycleConfig{Mode: ModeMonthly, ResetDay: 1, IntervalDays: 30}
	if err := manager.UpdateConfig(context.Background(), "node-1", UpdateConfigRequest{CycleConfig: cfg}, reportPtr("node-1", time.Now().UTC(), 100, 200, 500)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	entry.row.AccumulatedUp, entry.row.OffsetUp = 44, 9
	oldEnd := cloneTime(entry.row.CycleEndAt)
	if err := manager.SetDefaultTimezone("Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.Get("node-1")
	if oldEnd == nil || got.CycleEndAt == nil || got.CycleEndAt.Equal(*oldEnd) {
		t.Fatalf("inherited cycle end was not refreshed: old=%v new=%v", oldEnd, got.CycleEndAt)
	}
	if got.Timezone != "" || got.AccumulatedUp != 44 || got.OffsetUp != 9 {
		t.Fatalf("timezone refresh failed to preserve inherited config/accounting: %+v", got)
	}
}

func TestCalibrationAndResetCommitBeforeChangingMemory(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", at, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", at.Add(time.Minute), 150, 260, 1060)); err != nil {
		t.Fatal(err)
	}
	before, _ := manager.Get("node-1")
	if err := db.Exec("CREATE TRIGGER fail_traffic_ledger_update BEFORE UPDATE ON client_traffic_ledgers BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
		t.Fatal(err)
	}
	target := int64(500)
	if _, err := manager.Calibrate(ctx, "node-1", CalibrateRequest{TargetUp: &target}); err == nil {
		t.Fatal("calibrate unexpectedly succeeded despite database trigger")
	}
	after, _ := manager.Get("node-1")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed calibration changed memory:\nbefore=%+v\nafter=%+v", before, after)
	}
	if err := db.Exec("DROP TRIGGER fail_traffic_ledger_update").Error; err != nil {
		t.Fatal(err)
	}

	failure := errors.New("hook rejected close")
	manager.SetCycleCloseHook(func(_ *gorm.DB, _ []ClosedCycleSnapshot) error { return failure })
	if err := manager.ResetCycle(ctx, "node-1", at.Add(2*time.Hour)); !errors.Is(err, failure) {
		t.Fatalf("ResetCycle error = %v, want hook failure", err)
	}
	after, _ = manager.Get("node-1")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("failed reset changed memory:\nbefore=%+v\nafter=%+v", before, after)
	}

	var closeReason string
	manager.SetCycleCloseHook(func(_ *gorm.DB, snapshots []ClosedCycleSnapshot) error {
		closeReason = snapshots[0].CloseReason
		return nil
	})
	if err := manager.ResetCycle(ctx, "node-1", at.Add(3*time.Hour)); err != nil {
		t.Fatalf("successful ResetCycle: %v", err)
	}
	after, _ = manager.Get("node-1")
	if closeReason != CloseReasonManualReset || after.AccumulatedUp != 0 || after.AccumulatedDown != 0 || after.OffsetUp != 0 || after.OffsetDown != 0 || after.CalibratedAt != nil {
		t.Fatalf("manual reset result: reason=%q ledger=%+v", closeReason, after)
	}
}

func TestAgentModePassThroughAndRejectsServerOnlyOps(t *testing.T) {
	manager, db := newTestManager(t)
	at := time.Now().UTC()

	// Unconfigured client has no DB row and ObserveReport stays a fast no-op.
	if err := manager.ObserveReport(reportAt("node-1", at, 1234, 5678, 100)); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Get("node-1"); ok {
		t.Fatal("agent-mode client should not create a ledger entry automatically")
	}
	display := manager.ResolveDisplay("node-1", 1234, 5678)
	if display.Mode != ModeAgent || display.Up != 1234 || display.Down != 5678 {
		t.Fatalf("agent display = %+v, want pass-through 1234/5678", display)
	}
	var count int64
	if err := db.Model(&models.ClientTrafficLedger{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("agent-mode client unexpectedly created %d DB rows (err=%v)", count, err)
	}
	target := int64(500)
	if _, err := manager.Calibrate(context.Background(), "node-1", CalibrateRequest{TargetUp: &target}); err == nil {
		t.Fatal("agent-mode calibration unexpectedly succeeded")
	}
	if err := manager.ResetCycle(context.Background(), "node-1", at); err == nil {
		t.Fatal("agent-mode reset unexpectedly succeeded")
	}
}

func TestFailedFlushRetainsDirtyStateAndCycleClose(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", at, 100, 100, 100)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	oldStart := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	oldEnd := time.Date(2020, 2, 1, 0, 0, 0, 0, time.UTC)
	entry.row.CycleStartAt, entry.row.CycleEndAt = &oldStart, &oldEnd
	entry.row.AccumulatedUp = 9
	entry.dirty = true
	failure := errors.New("hook failure")
	manager.SetCycleCloseHook(func(_ *gorm.DB, _ []ClosedCycleSnapshot) error { return failure })
	if err := manager.FlushDirty(ctx); !errors.Is(err, failure) {
		t.Fatalf("FlushDirty error = %v, want hook failure", err)
	}
	if !entry.dirty || len(manager.closures) != 1 {
		t.Fatal("failed flush discarded dirty state or cycle close")
	}
	manager.SetCycleCloseHook(func(_ *gorm.DB, _ []ClosedCycleSnapshot) error { return nil })
	if err := manager.FlushDirty(ctx); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	if entry.dirty || len(manager.closures) != 0 {
		t.Fatal("successful retry did not clear dirty state")
	}
}

// shutdownMutationProbe captures the outcome of the mutations attempted while
// the final-flush transaction of Shutdown is still pending. The full display
// is stored so the test can verify the read-only projection semantics of a
// cross-cycle ResolveDisplay on a stopped (or stopping) manager.
type shutdownMutationProbe struct {
	display         DisplayTraffic
	observeAccepted bool
	tzAccepted      bool
}

// runShutdownMutationsDuringHold fires one cross-cycle ResolveDisplay, one
// ObserveReport and one SetDefaultTimezone concurrently while the shutdown
// transaction is held open. It uses only the public manager surface plus the
// same-package entry access every trafficledger test already relies on.
func runShutdownMutationsDuringHold(manager *Manager, node string, probe *shutdownMutationProbe) {
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		probe.display = manager.ResolveDisplay(node, 0, 0)
	}()
	go func() {
		defer wg.Done()
		probe.observeAccepted = manager.ObserveReport(reportAt(node, time.Now().UTC(), 999, 999, 9999)) == nil
	}()
	go func() {
		defer wg.Done()
		probe.tzAccepted = manager.SetDefaultTimezone("Asia/Tokyo") == nil
	}()
	wg.Wait()
}

// readLedgerState captures one entry's dirty flag, the queued closure count
// and a deep-copy snapshot under the manager lock.
func readLedgerState(manager *Manager, node string) (dirty bool, closures int, snap LedgerSnapshot) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	entry := manager.entries[node]
	return entry.dirty, len(manager.closures), snapshotFromModel(entry.row)
}

// rewindEntryCycleEnd moves one entry's in-memory cycle end back into the past
// without touching its version, dirty flag, counters or the closure queue, so
// a later cross-cycle ResolveDisplay is forced down the mutation (lazy
// rollover) path deterministically instead of hitting the read fast path.
func rewindEntryCycleEnd(manager *Manager, node string, end *time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if entry := manager.entries[node]; entry != nil {
		entry.row.CycleEndAt = end
	}
}

// runShutdownFirstWave performs the first Shutdown, whose injected hook
// failure must roll the final-flush transaction back while keeping every dirty
// row and queued closure for the retry.
func runShutdownFirstWave(t *testing.T, manager *Manager, probe *shutdownMutationProbe, expiredEnd, febStart time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := manager.Shutdown(ctx); err == nil || !strings.Contains(err.Error(), "injected shutdown flush failure") {
		t.Fatalf("first shutdown error = %v, want injected failure", err)
	}
	if probe.observeAccepted {
		t.Error("ObserveReport was accepted while the final flush was pending; nothing can ever persist that increment")
	}
	if probe.tzAccepted {
		t.Error("SetDefaultTimezone was accepted while the final flush was pending; timezone refresh dirtied rows past the snapshot")
	}
	dirtyAfterFail, closuresAfterFail, mem := readLedgerState(manager, "node-1")
	if !dirtyAfterFail {
		t.Error("failed shutdown dropped dirty state instead of keeping it for retry")
	}
	// The failed flush must keep exactly the one closure queued by the
	// pre-shutdown boundary crossing. A lazy ResolveDisplay rollover leaking
	// past the stop fence would append a second closure here.
	if closuresAfterFail != 1 {
		t.Errorf("failed shutdown kept %d closures, want exactly 1: a cross-cycle ResolveDisplay rolled the entry past the stop fence", closuresAfterFail)
	}
	// The read-only projection must leave the unrolled memory byte-identical:
	// counters still carry the real deltas and the rewound end stays expired.
	if mem.AccumulatedUp != 50 || mem.AccumulatedDown != 60 {
		t.Errorf("after failed shutdown memory accumulated %d/%d, want the unrolled 50/60: ResolveDisplay mutated live state", mem.AccumulatedUp, mem.AccumulatedDown)
	}
	if mem.CycleEndAt == nil || !mem.CycleEndAt.Equal(expiredEnd) {
		t.Errorf("after failed shutdown memory cycle end = %v, want the rewound expired end %v", mem.CycleEndAt, expiredEnd)
	}
	// The display may project the rolled cycle, but only as a projection: the
	// projected start must land strictly after the expired 2024-01 window.
	if probe.display.Up != 0 || probe.display.Down != 0 {
		t.Errorf("held-transaction display = %d/%d, want the projected fresh cycle 0/0", probe.display.Up, probe.display.Down)
	}
	if probe.display.CycleStartAt == nil || probe.display.CycleStartAt.Before(febStart) {
		t.Errorf("held-transaction display cycle start = %v, want a new cycle at or after %v", probe.display.CycleStartAt, febStart)
	}
}

// assertWaveTwoCommittedLedger verifies the retry flush committed the
// untampered rollover of node-1: zero dirty rows, zero leftover closures,
// memory byte-consistent with the database and no wave mutation visible.
func assertWaveTwoCommittedLedger(t *testing.T, manager *Manager, db *gorm.DB) {
	t.Helper()
	finalDirty, finalClosures, mem := readLedgerState(manager, "node-1")
	if finalDirty {
		t.Error("successful retry left the entry dirty: accepted state is unpersisted")
	}
	if finalClosures != 0 {
		t.Errorf("successful retry left %d unflushed closures", finalClosures)
	}
	persisted := loadPersistedLedger(t, db, "node-1")
	if !ledgerRowMatches(persisted, mem) {
		t.Errorf("memory diverged from retry flush:\npersisted accumulated=%d/%d window=%v..%v\nmemory    accumulated=%d/%d window=%v..%v",
			persisted.AccumulatedUp, persisted.AccumulatedDown, persisted.CycleStartAt, persisted.CycleEndAt,
			mem.AccumulatedUp, mem.AccumulatedDown, mem.CycleStartAt, mem.CycleEndAt)
	}
	// The retry's own legitimate rollover closes the 50/60 cycle and starts a
	// fresh zeroed one. Any mutation admitted in either wave would leave
	// visible tampering: the accepted ObserveReport counters (999-150/999-260
	// = 849/739), or the Asia/Tokyo boundary shift (start at 09:00 instead of
	// midnight UTC). Wave-2 mutations land strictly after the retry snapshot,
	// so they can never be persisted again — memory and database would diverge.
	if persisted.AccumulatedUp != 0 || persisted.AccumulatedDown != 0 {
		t.Errorf("retry persisted accumulated %d/%d, want the untampered fresh cycle 0/0: a post-stop mutation leaked into the retry", persisted.AccumulatedUp, persisted.AccumulatedDown)
	}
	if persisted.CycleStartAt == nil {
		t.Fatal("retry persisted ledger has no cycle start")
	}
	// The legitimate cycle boundary is midnight in the inherited effective
	// location (empty node timezone + empty default => time.Local). A leaked
	// Asia/Tokyo mutation shifts the boundary one hour off local midnight.
	loc, err := ResolveLocation("", "")
	if err != nil {
		t.Fatal(err)
	}
	startLocal := persisted.CycleStartAt.In(loc)
	if startLocal.Hour() != 0 || startLocal.Minute() != 0 || startLocal.Second() != 0 {
		t.Errorf("retry persisted cycle start %v is not midnight-aligned in the inherited location: the timezone mutation leaked into the retry", startLocal)
	}
	if persisted.Timezone != "" {
		t.Errorf("persisted timezone = %q, want inherited empty", persisted.Timezone)
	}
}

// ledgerRowMatches reports whether the persisted row carries the same
// accounting cycle and counters as the in-memory snapshot.
func ledgerRowMatches(row models.ClientTrafficLedger, snap LedgerSnapshot) bool {
	if row.AccumulatedUp != snap.AccumulatedUp || row.AccumulatedDown != snap.AccumulatedDown {
		return false
	}
	if row.OffsetUp != snap.OffsetUp || row.OffsetDown != snap.OffsetDown {
		return false
	}
	if (row.CycleStartAt == nil) != (snap.CycleStartAt == nil) || (row.CycleStartAt != nil && !row.CycleStartAt.Equal(*snap.CycleStartAt)) {
		return false
	}
	if (row.CycleEndAt == nil) != (snap.CycleEndAt == nil) || (row.CycleEndAt != nil && !row.CycleEndAt.Equal(*snap.CycleEndAt)) {
		return false
	}
	return true
}

func loadPersistedLedger(t *testing.T, db *gorm.DB, uuid string) models.ClientTrafficLedger {
	t.Helper()
	var row models.ClientTrafficLedger
	if err := db.First(&row, "uuid = ?", uuid).Error; err != nil {
		t.Fatalf("load persisted ledger %s: %v", uuid, err)
	}
	return row
}

// TestShutdownRejectsPostStopMutationsAndRetriesFinalFlush is the concurrency
// regression guard for the stop-fence: the final flush transaction is held
// open via the cycle-close hook while concurrent cross-cycle ResolveDisplay,
// ObserveReport and SetDefaultTimezone are forced into the exact window in
// which a mutation accepted after the snapshot would never be persisted
// again. After a successful stop the ledger must contain zero dirty rows and
// zero unflushed closures, memory must equal the database, and mutations must
// be rejected; after an injected flush failure, retrying Shutdown must
// re-persist the same untampered state exactly once.
func TestShutdownRejectsPostStopMutationsAndRetriesFinalFlush(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	// Inheriting timezone ("") so a wrongly admitted SetDefaultTimezone would
	// refresh the cycle window in place, dirty the row, and shift the UTC
	// midnight-aligned boundary: all externally observable tampering.
	cfg := CycleConfig{Mode: ModeMonthly, ResetDay: 1, IntervalDays: 30}
	at := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: cfg}, reportPtr("node-1", at, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}

	janStart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	febEnd := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	crossAt := at.Add(35 * 24 * time.Hour)
	farEnd := time.Now().UTC().Add(time.Hour)

	// A second configured node whose end stays valid during the retry
	// snapshot, so it is persisted with its real 70/80 counters exactly once.
	// Only the retry wave rewinds its end, inside the committing transaction:
	// that is the precise window in which a cross-cycle ResolveDisplay lazy
	// rollover would increment the version past the snapshot, queue an orphan
	// closure, and leave memory diverged from the database after settle. It
	// must be seeded BEFORE node-1 crosses its boundary: UpdateConfig commits
	// any queued closures in its own transaction, so seeding afterwards would
	// silently persist closure #1 ahead of the wave-1 rollback window.
	if err := db.Create(&models.Client{UUID: "node-2", Token: "tsxga-rhle-5"}).Error; err != nil {
		t.Fatalf("create second test client: %v", err)
	}
	if err := manager.UpdateConfig(ctx, "node-2", UpdateConfigRequest{CycleConfig: cfg}, reportPtr("node-2", at, 1000, 2000, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-2", at.Add(time.Minute), 1070, 2080, 1060)); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.entries["node-2"].row.CycleStartAt = timePointer(janStart)
	manager.entries["node-2"].row.CycleEndAt = timePointer(farEnd)
	manager.mu.Unlock()

	// Seed the expired-cycle state directly (the test already lives in the
	// same package): the state predates any stop, so the final flush must
	// carry it. RawLastAt is nudged to just before the crossing report so the
	// rollover keeps its baseline and the real delta 150-100 / 260-200 lands.
	manager.mu.Lock()
	entry1 := manager.entries["node-1"]
	entry1.row.CycleStartAt, entry1.row.CycleEndAt = &janStart, &febEnd
	entry1.row.RawLastAt = timePointer(crossAt.Add(-time.Minute))
	manager.mu.Unlock()

	// Crossing the boundary queues closure #1 and dirties the row with the
	// real deltas 50/60 in the fresh cycle.
	if err := manager.ObserveReport(reportAt("node-1", crossAt, 150, 260, 1060)); err != nil {
		t.Fatal(err)
	}
	// Deterministic expiry control: a lazy ResolveDisplay rollover only fires
	// for an expired in-memory end. The hook barrier rewinds the end into the
	// past atomically inside the held transaction, so no wall-clock race
	// decides whether the original interleaving happens.
	manager.mu.Lock()
	entry1.row.CycleEndAt = &farEnd
	manager.mu.Unlock()

	var (
		mu         sync.Mutex
		probePhase int
		probes     = map[int]*shutdownMutationProbe{1: {}, 2: {}}
		hookCalls  atomic.Int32
		failFirst  atomic.Bool
	)
	failFirst.Store(true)
	expiredEnd := time.Now().UTC().Add(-time.Minute)
	expiredEndPtr := timePointer(expiredEnd)
	manager.SetCycleCloseHook(func(tx *gorm.DB, snapshots []ClosedCycleSnapshot) error {
		hookCalls.Add(1)
		mu.Lock()
		phase := probePhase
		probe := probes[phase]
		mu.Unlock()
		if probe != nil {
			// The transaction is open and rows were already upserted: this is
			// the exact window of the original race. Each wave rewinds the
			// in-memory cycle end of its target node into the past atomically
			// inside the held transaction so the cross-cycle display rollover is
			// deterministic rather than wall-clock racy; then fires the three
			// concurrent mutation entries before the transaction may commit or
			// roll back. The rewind only touches CycleEndAt — never version,
			// dirty or the closure queue — so a correctly fenced manager keeps
			// the retry byte-consistent, while an unfenced ResolveDisplay leaves
			// observable tampering.
			mutNode := "node-1"
			if phase == 2 {
				mutNode = "node-2"
			}
			rewindEntryCycleEnd(manager, mutNode, expiredEndPtr)
			runShutdownMutationsDuringHold(manager, mutNode, probe)
		}
		if failFirst.Load() {
			failFirst.Store(false)
			return errors.New("injected shutdown flush failure")
		}
		return nil
	})

	// --- wave 1: the first final flush fails inside the hook ---
	mu.Lock()
	probePhase = 1
	mu.Unlock()
	runShutdownFirstWave(t, manager, probes[1], expiredEnd, febEnd)

	// --- wave 2: retry Shutdown; mutations must still be fenced ---
	mu.Lock()
	probePhase = 2
	mu.Unlock()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("retry shutdown = %v, want nil", err)
	}
	p2 := probes[2]
	if p2.observeAccepted {
		t.Error("ObserveReport was accepted while the retry flush was pending")
	}
	if p2.tzAccepted {
		t.Error("SetDefaultTimezone was accepted while the retry flush was pending")
	}

	// Zero unpersisted accepted state: no dirty rows, no leftover closures,
	// and memory byte-consistent with what the retry committed.
	assertWaveTwoCommittedLedger(t, manager, db)
	assertWaveTwoSecondNode(t, manager, db, probes[2])
	if got := manager.DefaultTimezone(); got != "" {
		t.Errorf("default timezone = %q after shutdown, want unchanged empty", got)
	}
	// Retry semantics: the hook must have run in both waves (failure + retry),
	// proving the rolled-back closures were re-presented rather than dropped.
	if n := hookCalls.Load(); n != 2 {
		t.Errorf("close hook invoked %d times, want exactly 2 (failing flush + retry)", n)
	}
	// Fully flushed: a repeated Shutdown is a no-op success and must not
	// re-present closures.
	if err := manager.Shutdown(ctx); err != nil {
		t.Errorf("repeat shutdown after success = %v, want nil", err)
	}
	if n := hookCalls.Load(); n != 2 {
		t.Errorf("close hook invoked %d times after repeat shutdown, want exactly 2 (no redundant work)", n)
	}

	assertPostShutdownDisplayProjection(t, manager)
	if err := manager.ObserveReport(reportAt("node-1", time.Now().UTC(), 999, 999, 9999)); !errors.Is(err, ErrManagerStopped) {
		t.Errorf("post-shutdown observe error = %v, want ErrManagerStopped", err)
	}
	if err := manager.SetDefaultTimezone("Asia/Tokyo"); !errors.Is(err, ErrManagerStopped) {
		t.Errorf("post-shutdown SetDefaultTimezone error = %v, want ErrManagerStopped", err)
	}
}

// assertWaveTwoSecondNode verifies the retry wave's committing-transaction
// window: node-2 was snapshotted and persisted with its real unrolled 70/80
// counters, the concurrent cross-cycle ResolveDisplay must have been a pure
// projection, and after the commit the entry is clean, no orphan closure
// survives and memory still matches the persisted row on the accounting
// counters and cycle start (the in-memory end carries only the harness
// rewind, which a fenced manager never persists or rolls).
func assertWaveTwoSecondNode(t *testing.T, manager *Manager, db *gorm.DB, probe *shutdownMutationProbe) {
	t.Helper()
	dirty, closures, mem := readLedgerState(manager, "node-2")
	if dirty {
		t.Error("successful retry left node-2 dirty: a ResolveDisplay lazy rollover leaked past the retry transaction window")
	}
	if closures != 0 {
		t.Errorf("successful retry left %d unflushed closures after node-2 window: an orphan rollover closure leaked", closures)
	}
	persisted := loadPersistedLedger(t, db, "node-2")
	if persisted.AccumulatedUp != 70 || persisted.AccumulatedDown != 80 {
		t.Errorf("persisted node-2 accumulated %d/%d, want the untampered 70/80", persisted.AccumulatedUp, persisted.AccumulatedDown)
	}
	if mem.AccumulatedUp != 70 || mem.AccumulatedDown != 80 {
		t.Errorf("in-memory node-2 accumulated %d/%d, want the unrolled 70/80: ResolveDisplay mutated live state", mem.AccumulatedUp, mem.AccumulatedDown)
	}
	if persisted.CycleStartAt == nil || mem.CycleStartAt == nil || !mem.CycleStartAt.Equal(*persisted.CycleStartAt) {
		t.Errorf("node-2 cycle start diverged: persisted %v memory %v", persisted.CycleStartAt, mem.CycleStartAt)
	}
	if probe.display.Up != 0 || probe.display.Down != 0 {
		t.Errorf("retry-window node-2 display = %d/%d, want the projected fresh cycle 0/0", probe.display.Up, probe.display.Down)
	}
	if probe.display.CycleStartAt == nil || !probe.display.CycleStartAt.After(*mem.CycleStartAt) {
		t.Errorf("retry-window node-2 display cycle start = %v, want a new cycle after %v", probe.display.CycleStartAt, mem.CycleStartAt)
	}
}

// assertPostShutdownDisplayProjection runs one cross-cycle ResolveDisplay on
// node-2 after a successful stop. The node is genuinely expired (the harness
// rewind was never rolled), so the lazy-rollover mutation path is really
// exercised: the projection may report the rolled cycle, but memory, dirty
// flags and the closure queue must stay byte-identical.
func assertPostShutdownDisplayProjection(t *testing.T, manager *Manager) {
	t.Helper()
	dirtyBefore, closuresBefore, memBefore := readLedgerState(manager, "node-2")
	if dirtyBefore {
		t.Fatal("precondition: node-2 must be clean before the post-shutdown display probe")
	}
	if memBefore.CycleEndAt == nil || time.Now().UTC().Before(memBefore.CycleEndAt.UTC()) {
		t.Fatalf("precondition: node-2 cycle end %v must be genuinely expired, otherwise the lazy mutation path is never exercised", memBefore.CycleEndAt)
	}
	display := manager.ResolveDisplay("node-2", 0, 0)
	if display.Up != 0 || display.Down != 0 {
		t.Errorf("post-shutdown display = %d/%d, want the projected fresh cycle 0/0", display.Up, display.Down)
	}
	if display.Calibrated {
		t.Error("post-shutdown display unexpectedly calibrated")
	}
	if display.CycleStartAt == nil || !display.CycleStartAt.After(*memBefore.CycleStartAt) {
		t.Errorf("post-shutdown display cycle start = %v, want a new cycle after %v", display.CycleStartAt, memBefore.CycleStartAt)
	}
	dirtyAfter, closuresAfter, memAfter := readLedgerState(manager, "node-2")
	if closuresAfter != closuresBefore {
		t.Errorf("post-shutdown ResolveDisplay queued closures: %d -> %d", closuresBefore, closuresAfter)
	}
	if dirtyAfter {
		t.Error("post-shutdown ResolveDisplay dirtied the entry")
	}
	if !reflect.DeepEqual(memBefore, memAfter) {
		t.Errorf("post-shutdown ResolveDisplay mutated memory:\nbefore=%+v\nafter =%+v", memBefore, memAfter)
	}
}

// TestShutdownPersistsFinalObservations is the regression guard for the
// flush-then-stop race: samples accepted before Shutdown must be persisted by
// the final flush, and samples arriving after Shutdown must be rejected
// instead of silently dropped.
func TestShutdownPersistsFinalObservations(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", now, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	// Observe a sample but never flush: only Shutdown's final flush may persist it.
	if err := manager.ObserveReport(reportAt("node-1", now.Add(time.Minute), 160, 260, 1060)); err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	var persisted models.ClientTrafficLedger
	if err := db.First(&persisted, "uuid = ?", "node-1").Error; err != nil {
		t.Fatalf("load persisted ledger after shutdown: %v", err)
	}
	if persisted.AccumulatedUp != 60 || persisted.AccumulatedDown != 60 {
		t.Fatalf("final flush persisted %d/%d, want 60/60", persisted.AccumulatedUp, persisted.AccumulatedDown)
	}
	// Post-shutdown observations are rejected rather than silently lost.
	if err := manager.ObserveReport(reportAt("node-1", now.Add(2*time.Minute), 999, 999, 1120)); !errors.Is(err, ErrManagerStopped) {
		t.Fatalf("post-shutdown observe error = %v, want ErrManagerStopped", err)
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("second shutdown = %v, want nil", err)
	}
}

// TestShutdownFailureCanRetryFinalFlush verifies that a failed final flush
// does not latch the manager into a false successful stopped state: residual
// dirty rows and closures are retried and persisted by a repeated Shutdown.
func TestShutdownFailureCanRetryFinalFlush(t *testing.T) {
	manager, db := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", now, 100, 200, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ObserveReport(reportAt("node-1", now.Add(time.Minute), 160, 260, 1060)); err != nil {
		t.Fatal(err)
	}

	failures := 1
	manager.SetCycleCloseHook(func(tx *gorm.DB, closures []ClosedCycleSnapshot) error {
		if failures > 0 {
			failures--
			return errors.New("injected final flush failure")
		}
		return nil
	})
	manager.mu.Lock()
	manager.nextClosure = 1
	manager.closures = append(manager.closures, queuedClose{
		sequence: 1,
		snapshot: closeSnapshot(manager.entries["node-1"].row, now, CloseReasonManualReset),
	})
	manager.mu.Unlock()

	if err := manager.Shutdown(ctx); err == nil || !strings.Contains(err.Error(), "injected final flush failure") {
		t.Fatalf("first shutdown error = %v, want injected failure", err)
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("retry shutdown = %v, want nil", err)
	}
	var persisted models.ClientTrafficLedger
	if err := db.First(&persisted, "uuid = ?", "node-1").Error; err != nil {
		t.Fatalf("load persisted ledger after retry: %v", err)
	}
	if persisted.AccumulatedUp != 60 || persisted.AccumulatedDown != 60 {
		t.Fatalf("persisted accumulation after retry = up %d down %d, want 60/60", persisted.AccumulatedUp, persisted.AccumulatedDown)
	}
	manager.mu.RLock()
	stillDirty := manager.entries["node-1"].dirty
	manager.mu.RUnlock()
	if stillDirty {
		t.Fatal("successful shutdown retry left the entry dirty")
	}
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("third shutdown after successful retry = %v, want nil", err)
	}
}

// Atomic client+ledger removal is regression-tested end-to-end through the
// admin:removeClient RPC in web/rpc/jsonrpc/admin.audit_test.go
// (TestAdminRemoveClientRollsBackOnLedgerHookFailure): a same-package happy
// path here could never falsify a non-atomic two-step deletion, because a
// successful delete looks identical whether or not it ran inside one
// transaction.

// TestSetDefaultTimezoneBackwardShiftPreservesUsage guards the reverse
// direction of timezone inheritance refresh: moving the inherited timezone
// backwards (Asia/Tokyo -> UTC) shifts the cycle start later in absolute time
// while still describing the same accounting cycle, so accumulated usage must
// survive instead of being wiped by a false rollover.
func TestSetDefaultTimezoneBackwardShiftPreservesUsage(t *testing.T) {
	manager, _ := newTestManager(t)
	if err := manager.SetDefaultTimezone("Asia/Tokyo"); err != nil {
		t.Fatal(err)
	}
	cfg := CycleConfig{Mode: ModeMonthly, ResetDay: 1, IntervalDays: 30}
	if err := manager.UpdateConfig(context.Background(), "node-1", UpdateConfigRequest{CycleConfig: cfg}, reportPtr("node-1", time.Now().UTC(), 100, 200, 500)); err != nil {
		t.Fatal(err)
	}
	entry := manager.entries["node-1"]
	entry.row.AccumulatedUp, entry.row.AccumulatedDown, entry.row.OffsetUp = 440, 550, 9
	if err := manager.SetDefaultTimezone("UTC"); err != nil {
		t.Fatal(err)
	}
	got, _ := manager.Get("node-1")
	if got.AccumulatedUp != 440 || got.AccumulatedDown != 550 || got.OffsetUp != 9 {
		t.Fatalf("backward timezone shift wiped usage: %+v", got)
	}
	if len(manager.closures) != 0 {
		t.Fatalf("backward timezone shift produced %d spurious closures", len(manager.closures))
	}
}

func TestManagerConcurrentReportsAndDisplay(t *testing.T) {
	manager, _ := newTestManager(t)
	base := time.Now().UTC()
	if err := manager.UpdateConfig(context.Background(), "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", base.Add(-time.Second), 0, 0, 10)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			report := reportAt("node-1", base.Add(time.Duration(i)*time.Millisecond), int64(i*100), int64(i*50), int64(i+100))
			if err := manager.ObserveReport(report); err != nil {
				t.Errorf("observe: %v", err)
			}
			manager.ResolveDisplay("node-1", report.Network.TotalUp, report.Network.TotalDown)
			manager.Get("node-1")
		}(i)
	}
	wg.Wait()
}

func TestRestartReplaysTrafficSinceLastPersistedBaseline(t *testing.T) {
	first, db := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	at := time.Date(now.Year(), now.Month(), 15, 12, 0, 0, 0, time.UTC)
	if err := first.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: monthlyConfig()}, reportPtr("node-1", at, 100, 200, 1000)); err != nil {
		t.Fatalf("switch to server cycle: %v", err)
	}
	if err := first.ObserveReport(reportAt("node-1", at.Add(time.Minute), 200, 260, 1060)); err != nil {
		t.Fatalf("observe unflushed report: %v", err)
	}

	second := NewManager(db, "")
	if err := second.Initialize(ctx); err != nil {
		t.Fatalf("initialize replacement manager: %v", err)
	}
	if err := second.ObserveReport(reportAt("node-1", at.Add(2*time.Minute), 260, 320, 1120)); err != nil {
		t.Fatalf("observe after restart: %v", err)
	}
	got, ok := second.Get("node-1")
	if !ok || got.AccumulatedUp != 160 || got.AccumulatedDown != 120 {
		t.Fatalf("recovered cycle totals = %d/%d, want 160/120", got.AccumulatedUp, got.AccumulatedDown)
	}
	if err := second.FlushDirty(ctx); err != nil {
		t.Fatalf("flush recovered totals: %v", err)
	}
	var persisted models.ClientTrafficLedger
	if err := db.First(&persisted, "uuid = ?", "node-1").Error; err != nil {
		t.Fatalf("load recovered ledger: %v", err)
	}
	if persisted.AccumulatedUp != 160 || persisted.AccumulatedDown != 120 || persisted.RawLastUp != 260 || persisted.RawLastDown != 320 {
		t.Fatalf("persisted recovered state = %+v", persisted)
	}
}

func TestPreserveCurrentUsageOnSwitchFromAgentMode(t *testing.T) {
	manager, _ := newTestManager(t)
	ctx := context.Background()
	at := time.Now().UTC()

	// Offline switch with PreserveCurrentUsage must not seed baseline or fake offsets.
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{
		CycleConfig:          monthlyConfig(),
		PreserveCurrentUsage: true,
	}, nil); err != nil {
		t.Fatalf("offline switch with preserve: %v", err)
	}
	snap, _ := manager.Get("node-1")
	if snap.HasBaseline || snap.OffsetUp != 0 || snap.OffsetDown != 0 || snap.CalibratedAt != nil {
		t.Fatalf("offline preserve should leave baseline/offsets empty: %+v", snap)
	}

	// Switch back to agent, then switch to monthly while online with PreserveCurrentUsage.
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{CycleConfig: DefaultCycleConfig()}, nil); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{
		CycleConfig:          CycleConfig{Mode: ModeMonthly, ResetDay: 15, IntervalDays: 30, Timezone: "UTC"},
		PreserveCurrentUsage: true,
	}, reportPtr("node-1", at, 1200, 3400, 500)); err != nil {
		t.Fatalf("online switch with preserve: %v", err)
	}
	snap, _ = manager.Get("node-1")
	if !snap.HasBaseline || snap.AccumulatedUp != 0 || snap.AccumulatedDown != 0 ||
		snap.OffsetUp != 1200 || snap.OffsetDown != 3400 || snap.CalibratedAt == nil ||
		snap.EffectiveUp != 1200 || snap.EffectiveDown != 3400 {
		t.Fatalf("preserved initial usage = %+v", snap)
	}

	// Subsequent report adds only the real delta without double counting.
	if err := manager.ObserveReport(reportAt("node-1", at.Add(time.Minute), 1250, 3550, 560)); err != nil {
		t.Fatal(err)
	}
	snap, _ = manager.Get("node-1")
	if snap.AccumulatedUp != 50 || snap.AccumulatedDown != 150 || snap.EffectiveUp != 1250 || snap.EffectiveDown != 3550 {
		t.Fatalf("post-preserve delta = %+v, want effective 1250/3550", snap)
	}

	// Staying in server mode ignores PreserveCurrentUsage and keeps current accumulated + offsets.
	if err := manager.UpdateConfig(ctx, "node-1", UpdateConfigRequest{
		CycleConfig:          CycleConfig{Mode: ModeManual, ResetDay: 15, IntervalDays: 30, Timezone: "UTC"},
		PreserveCurrentUsage: true,
	}, reportPtr("node-1", at.Add(2*time.Minute), 9999, 9999, 620)); err != nil {
		t.Fatal(err)
	}
	snap, _ = manager.Get("node-1")
	if snap.OffsetUp != 1200 || snap.OffsetDown != 3400 || snap.AccumulatedUp != 50 || snap.AccumulatedDown != 150 {
		t.Fatalf("in-place update unexpectedly overwrote offsets: %+v", snap)
	}
}
