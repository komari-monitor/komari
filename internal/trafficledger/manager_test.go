package trafficledger

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
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
