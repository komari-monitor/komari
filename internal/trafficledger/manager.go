package trafficledger

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/internal/config"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const crossCycleGapLimit = 5 * time.Minute

var (
	ErrManagerNotInitialized = errors.New("traffic ledger manager is not initialized")
	ErrManagerStopped        = errors.New("traffic ledger manager is stopped")
	ErrLedgerNotFound        = errors.New("traffic ledger not found")
)

type ledgerEntry struct {
	row     models.ClientTrafficLedger
	dirty   bool
	version uint64
}

type queuedClose struct {
	sequence uint64
	snapshot ClosedCycleSnapshot
}

// Manager owns the in-memory traffic ledger and persists dirty state in batches.
type Manager struct {
	db *gorm.DB

	mu               sync.RWMutex
	flushMu          sync.Mutex
	defaultTimezone  string
	entries          map[string]*ledgerEntry
	closures         []queuedClose
	nextClosure      uint64
	hook             CycleCloseHook
	initialized      bool
	stopped          bool
	flushedAfterStop bool
}

func NewManager(db *gorm.DB, defaultTimezone string) *Manager {
	return &Manager{
		db:              db,
		defaultTimezone: strings.TrimSpace(defaultTimezone),
		entries:         make(map[string]*ledgerEntry),
	}
}

// Initialize migrates the ledger table and loads configured node ledgers into
// memory. Clients without a row implicitly use ModeAgent (pass-through).
func (m *Manager) Initialize(ctx context.Context) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	m.mu.RLock()
	ready, stopped := m.initialized, m.stopped
	m.mu.RUnlock()
	if ready {
		return nil
	}
	if stopped {
		return ErrManagerStopped
	}
	if m.db == nil {
		return errors.New("traffic ledger database is nil")
	}

	db := m.db.WithContext(ctx)
	if err := db.AutoMigrate(&models.ClientTrafficLedger{}); err != nil {
		return fmt.Errorf("migrate traffic ledger: %w", err)
	}
	var rows []models.ClientTrafficLedger
	if err := db.Find(&rows).Error; err != nil {
		return fmt.Errorf("load traffic ledgers: %w", err)
	}

	loaded := make(map[string]*ledgerEntry, len(rows))
	for _, row := range rows {
		if row.Mode == "" {
			row.Mode = ModeAgent
		}
		loaded[row.UUID] = &ledgerEntry{row: row}
	}

	m.mu.Lock()
	m.entries = loaded
	m.initialized = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) SetCycleCloseHook(hook CycleCloseHook) {
	m.mu.Lock()
	m.hook = hook
	m.mu.Unlock()
}

func (m *Manager) DefaultTimezone() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultTimezone
}

// SetDefaultTimezone updates the fallback timezone used by nodes without an
// explicit timezone override, refreshing their cycle boundaries in place.
func (m *Manager) SetDefaultTimezone(timezone string) error {
	timezone = strings.TrimSpace(timezone)
	if timezone != "" {
		if _, err := loadLocationCached(timezone); err != nil {
			return fmt.Errorf("invalid traffic timezone %q: %w", timezone, err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		// A stopped manager must stay byte-identical with its final flush:
		// refreshing cycle windows here would dirty rows that nothing will
		// ever persist again.
		return ErrManagerStopped
	}
	m.defaultTimezone = timezone
	now := time.Now().UTC()
	for _, entry := range m.entries {
		if entry.row.Timezone == "" && isAutomaticMode(entry.row.Mode) {
			if err := m.refreshAutomaticCycleLocked(entry, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// ObserveReport records a raw cumulative counter sample in memory. It never
// performs database I/O; FlushDirty or Shutdown persists dirty entries.
func (m *Manager) ObserveReport(report v2.Report) error {
	if report.UUID == "" {
		return errors.New("report UUID is required")
	}

	// Fast path: untouched clients (ModeAgent) need no write lock or DB row.
	m.mu.RLock()
	if !m.initialized {
		m.mu.RUnlock()
		return ErrManagerNotInitialized
	}
	if m.stopped {
		m.mu.RUnlock()
		return ErrManagerStopped
	}
	entry := m.entries[report.UUID]
	if entry == nil || entry.row.Mode == ModeAgent {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.initialized {
		return ErrManagerNotInitialized
	}
	if m.stopped {
		return ErrManagerStopped
	}
	entry = m.entries[report.UUID]
	if entry == nil || entry.row.Mode == ModeAgent {
		return nil
	}

	at := report.UpdatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()

	row := &entry.row
	if row.HasBaseline && row.RawLastAt != nil && !at.After(row.RawLastAt.UTC()) {
		return nil
	}

	if isAutomaticMode(row.Mode) {
		if err := m.refreshAutomaticCycleLocked(entry, at); err != nil {
			return err
		}
	} else if row.Mode == ModeManual && row.CycleStartAt == nil {
		row.CycleStartAt = timePointer(at)
	}

	up := max(report.Network.TotalUp, 0)
	down := max(report.Network.TotalDown, 0)
	if row.HasBaseline {
		upDelta := ComputeCounterDelta(up, row.RawLastUp, report.Uptime, row.RawLastUptime)
		downDelta := ComputeCounterDelta(down, row.RawLastDown, report.Uptime, row.RawLastUptime)
		row.AccumulatedUp = saturatingAdd(row.AccumulatedUp, upDelta)
		row.AccumulatedDown = saturatingAdd(row.AccumulatedDown, downDelta)
	}

	row.RawLastUp, row.RawLastDown = up, down
	row.RawLastUptime = report.Uptime
	row.RawLastAt = timePointer(at)
	row.HasBaseline = true
	row.UpdatedAt = time.Now().UTC()
	markDirty(entry)
	return nil
}

// ResolveDisplay returns the traffic totals exposed to live status APIs and
// alerts. The common in-cycle path takes only RLock with zero tzdata lookups.
func (m *Manager) ResolveDisplay(uuid string, rawUp, rawDown int64) DisplayTraffic {
	now := time.Now().UTC()

	m.mu.RLock()
	entry := m.entries[uuid]
	if entry == nil || entry.row.Mode == ModeAgent {
		m.mu.RUnlock()
		return DisplayTraffic{Up: max(rawUp, 0), Down: max(rawDown, 0), Mode: ModeAgent}
	}
	if !needsRollover(entry.row, now) {
		display := displayFromRow(entry.row)
		m.mu.RUnlock()
		return display
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	entry = m.entries[uuid]
	if entry == nil || entry.row.Mode == ModeAgent {
		return DisplayTraffic{Up: max(rawUp, 0), Down: max(rawDown, 0), Mode: ModeAgent}
	}
	if m.stopped {
		// After Shutdown the ledger must stay byte-identical with the final
		// flush: a lazy rollover here would mark rows dirty and queue closures
		// that nothing ever persists again, silently dropping accepted state.
		// The status display degrades to a pure in-memory projection instead.
		projection := ledgerEntry{row: cloneLedger(entry.row)}
		_, _ = refreshEntryCycle(&projection, m.defaultTimezone, now)
		return displayFromRow(projection.row)
	}
	_ = m.refreshAutomaticCycleLocked(entry, now)
	return displayFromRow(entry.row)
}

func (m *Manager) Get(uuid string) (LedgerSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry := m.entries[uuid]
	if entry == nil {
		return LedgerSnapshot{}, false
	}
	return snapshotFromModel(entry.row), true
}

// flushSnapshots are the dirty rows versionsed under m.mu so a failed flush
// can merge concurrent increments back into the in-memory entries.
type flushSnapshot struct {
	rows     []models.ClientTrafficLedger
	versions map[string]uint64
	closures []ClosedCycleSnapshot
	hook     CycleCloseHook
}

// snapshotDirtyLocked rolls offline cycles and captures all dirty rows and
// pending closures. Call with m.mu held.
func (m *Manager) snapshotDirtyLocked(now time.Time) flushSnapshot {
	for _, entry := range m.entries {
		if isAutomaticMode(entry.row.Mode) && needsRollover(entry.row, now) {
			_ = m.refreshAutomaticCycleLocked(entry, now)
		}
	}
	snap := flushSnapshot{
		rows:     make([]models.ClientTrafficLedger, 0),
		versions: make(map[string]uint64),
		hook:     m.hook,
	}
	for uuid, entry := range m.entries {
		if entry.dirty {
			snap.rows = append(snap.rows, cloneLedger(entry.row))
			snap.versions[uuid] = entry.version
		}
	}
	closures, _ := m.snapshotClosuresLocked(nil)
	snap.closures = closures
	return snap
}

// commitFlushSnapshot persists rows and closures without holding m.mu; the
// database transaction must never run inside the global lock because SQLite
// single-connection waits would otherwise freeze every ObserveReport.
func (m *Manager) commitFlushSnapshot(ctx context.Context, snap flushSnapshot) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsertLedgers(tx, snap.rows); err != nil {
			return fmt.Errorf("persist traffic ledgers: %w", err)
		}
		if len(snap.closures) > 0 && snap.hook != nil {
			if err := snap.hook(tx, snap.closures); err != nil {
				return fmt.Errorf("close traffic cycles: %w", err)
			}
		}
		return nil
	})
}

// settleFlushLocked clears dirty flags for exactly the versions that were
// persisted. Rows observed or calibrated while the transaction ran carry a
// newer version and keep their dirty flag, so concurrent increments are never
// dropped; the closures list only drops entries covered by this flush.
func (m *Manager) settleFlushLocked(snap flushSnapshot, maxSeq uint64) {
	for uuid, version := range snap.versions {
		if entry := m.entries[uuid]; entry != nil && entry.version == version {
			entry.dirty = false
		}
	}
	m.trimClosuresLocked(maxSeq)
}

// FlushDirty checks for offline cycle rollovers and commits all dirty ledgers
// and queued cycle closures in a single database transaction.
func (m *Manager) FlushDirty(ctx context.Context) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	if !m.initialized {
		m.mu.Unlock()
		return ErrManagerNotInitialized
	}
	if m.stopped {
		m.mu.Unlock()
		return ErrManagerStopped
	}
	snap := m.snapshotDirtyLocked(time.Now().UTC())
	_, maxSeq := m.snapshotClosuresLocked(nil)
	m.mu.Unlock()

	if len(snap.rows) == 0 && len(snap.closures) == 0 {
		return nil
	}
	if err := m.commitFlushSnapshot(ctx, snap); err != nil {
		return err
	}

	m.mu.Lock()
	m.settleFlushLocked(snap, maxSeq)
	m.mu.Unlock()
	return nil
}

// Shutdown stops accepting new samples first, then performs the final flush.
// Marking stopped inside m.mu before the last transaction closes the race in
// which a report observed between the final flush and the old stopping point
// would be silently dropped without ever reaching the database.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	m.mu.Lock()
	if m.stopped && m.flushedAfterStop {
		m.mu.Unlock()
		return nil
	}
	if !m.initialized {
		m.mu.Unlock()
		return ErrManagerNotInitialized
	}
	// Stop accepting new observations before snapshotting so every accepted
	// sample is either already in the snapshot or rejected from here on. If the
	// final transaction fails, flushedAfterStop remains false and a repeated
	// Shutdown snapshots and retries the residual dirty rows/closures instead
	// of silently reporting success.
	m.stopped = true
	snap := m.snapshotDirtyLocked(time.Now().UTC())
	_, maxSeq := m.snapshotClosuresLocked(nil)
	m.mu.Unlock()

	if len(snap.rows) == 0 && len(snap.closures) == 0 {
		m.mu.Lock()
		m.flushedAfterStop = true
		m.mu.Unlock()
		return nil
	}
	if err := m.commitFlushSnapshot(ctx, snap); err != nil {
		return err
	}

	m.mu.Lock()
	m.settleFlushLocked(snap, maxSeq)
	m.flushedAfterStop = true
	m.mu.Unlock()
	return nil
}

// UpdateConfig atomically updates a client's cycle config and optional quota.
// latestRaw should be nil when the node has no live report; in that case
// HasBaseline stays false until the next real report arrives.
func (m *Manager) UpdateConfig(ctx context.Context, uuid string, req UpdateConfigRequest, latestRaw *v2.Report) error {
	if uuid == "" {
		return errors.New("client UUID is required")
	}
	req.Timezone = strings.TrimSpace(req.Timezone)
	if err := ValidateCycleConfig(req.CycleConfig); err != nil {
		return err
	}
	if req.TrafficLimit != nil && *req.TrafficLimit < 0 {
		return errors.New("traffic_limit must be non-negative")
	}
	var trafficLimitType *string
	if req.TrafficLimitType != nil {
		value := strings.ToLower(strings.TrimSpace(*req.TrafficLimitType))
		switch value {
		case "sum", "max", "min", "up", "down":
			trafficLimitType = &value
		default:
			return fmt.Errorf("invalid traffic_limit_type %q", *req.TrafficLimitType)
		}
	}

	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRunningLocked(); err != nil {
		return err
	}

	entry := m.entries[uuid]
	row := modelFromConfig(uuid, DefaultCycleConfig())
	if entry != nil {
		row = cloneLedger(entry.row)
	}

	now := time.Now().UTC()
	var extraCloses []ClosedCycleSnapshot
	oldMode := row.Mode

	if !sameCycleConfig(configFromModel(row), req.CycleConfig) {
		closes, err := m.applyModeTransitionLocked(&row, oldMode, req, latestRaw, now)
		if err != nil {
			return err
		}
		extraCloses = append(extraCloses, closes...)
	}
	row.UpdatedAt = now

	closures, maxSeq := m.snapshotClosuresLocked(extraCloses)
	if err := m.commitConfigTransaction(ctx, uuid, row, req.TrafficLimit, trafficLimitType, closures); err != nil {
		return fmt.Errorf("update traffic ledger config: %w", err)
	}

	if entry == nil {
		entry = &ledgerEntry{}
		m.entries[uuid] = entry
	}
	entry.row = row
	entry.dirty = false
	entry.version++
	m.trimClosuresLocked(maxSeq)
	return nil
}

// applyModeTransitionLocked rewrites row in place for a cycle config change,
// returning any cycle close snapshot caused by leaving server-side accounting.
func (m *Manager) applyModeTransitionLocked(row *models.ClientTrafficLedger, oldMode string, req UpdateConfigRequest, latestRaw *v2.Report, now time.Time) ([]ClosedCycleSnapshot, error) {
	row.Mode = req.Mode
	row.ResetDay = req.ResetDay
	row.IntervalDays = req.IntervalDays
	row.AnchorAt = cloneTime(req.AnchorAt)
	row.Timezone = req.Timezone

	switch {
	case req.Mode == ModeAgent:
		var closes []ClosedCycleSnapshot
		if oldMode != ModeAgent && row.CycleStartAt != nil {
			closes = append(closes, closeSnapshot(*row, now, CloseReasonModeChanged))
		}
		clearCycleCounters(row)
		clearBaseline(row)
		row.CycleStartAt, row.CycleEndAt = nil, nil
		return closes, nil

	case oldMode == ModeAgent:
		// Fresh opt-in to server-side accounting.
		clearCycleCounters(row)
		if err := m.assignCycleWindowLocked(row, now); err != nil {
			return nil, err
		}
		seedBaseline(row, latestRaw, now)
		if req.PreserveCurrentUsage && latestRaw != nil {
			up := max(latestRaw.Network.TotalUp, 0)
			down := max(latestRaw.Network.TotalDown, 0)
			if up > 0 || down > 0 {
				row.OffsetUp = up
				row.OffsetDown = down
				row.CalibratedAt = timePointer(now)
			}
		}
		return nil, nil
	}

	// Stay in server mode: preserve accumulated usage and offsets, update
	// cycle boundaries, and seed baseline if still missing.
	if err := m.assignCycleWindowLocked(row, now); err != nil {
		return nil, err
	}
	if !row.HasBaseline {
		seedBaseline(row, latestRaw, now)
	}
	return nil, nil
}

// commitConfigTransaction persists the config update in one transaction. It
// first re-verifies the client row exists so a concurrent adminRemoveClient
// cannot race a config update into resurrecting an orphaned ledger for the
// deleted node.
func (m *Manager) commitConfigTransaction(ctx context.Context, uuid string, row models.ClientTrafficLedger, trafficLimit *int64, trafficLimitType *string, closures []ClosedCycleSnapshot) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var clientCount int64
		if err := tx.Model(&models.Client{}).Where("uuid = ?", uuid).Count(&clientCount).Error; err != nil {
			return err
		}
		if clientCount == 0 {
			return fmt.Errorf("client %s no longer exists", uuid)
		}
		if trafficLimit != nil || trafficLimitType != nil {
			updates := make(map[string]any, 2)
			if trafficLimit != nil {
				updates["traffic_limit"] = *trafficLimit
			}
			if trafficLimitType != nil {
				updates["traffic_limit_type"] = *trafficLimitType
			}
			if err := tx.Model(&models.Client{}).Where("uuid = ?", uuid).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := upsertLedgers(tx, []models.ClientTrafficLedger{row}); err != nil {
			return err
		}
		if len(closures) > 0 && m.hook != nil {
			return m.hook(tx, closures)
		}
		return nil
	})
}

func (m *Manager) Calibrate(ctx context.Context, uuid string, req CalibrateRequest) (CalibrationResult, error) {
	if err := validateCalibration(req); err != nil {
		return CalibrationResult{}, err
	}

	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRunningLocked(); err != nil {
		return CalibrationResult{}, err
	}

	entry := m.entries[uuid]
	if entry == nil {
		return CalibrationResult{}, ErrLedgerNotFound
	}
	if entry.row.Mode == ModeAgent {
		return CalibrationResult{}, errors.New("traffic calibration is not supported in agent mode")
	}
	now := time.Now().UTC()
	if isAutomaticMode(entry.row.Mode) && needsRollover(entry.row, now) {
		if err := m.refreshAutomaticCycleLocked(entry, now); err != nil {
			return CalibrationResult{}, err
		}
	}

	row := cloneLedger(entry.row)
	beforeUp := effectiveValue(row.AccumulatedUp, row.OffsetUp)
	beforeDown := effectiveValue(row.AccumulatedDown, row.OffsetDown)
	result := CalibrationResult{BeforeUp: beforeUp, BeforeDown: beforeDown}

	switch {
	case req.TargetTotal != nil:
		up, down := splitTargetTotal(*req.TargetTotal, beforeUp, beforeDown)
		row.OffsetUp = up - row.AccumulatedUp
		row.OffsetDown = down - row.AccumulatedDown
	case req.TargetUp != nil:
		row.OffsetUp = *req.TargetUp - row.AccumulatedUp
	case req.DeltaUp != nil:
		row.OffsetUp = saturatingAdd(row.OffsetUp, *req.DeltaUp)
	}
	if req.TargetTotal == nil {
		if req.TargetDown != nil {
			row.OffsetDown = *req.TargetDown - row.AccumulatedDown
		} else if req.DeltaDown != nil {
			row.OffsetDown = saturatingAdd(row.OffsetDown, *req.DeltaDown)
		}
	}

	if row.OffsetUp == 0 && row.OffsetDown == 0 {
		row.CalibratedAt = nil
	} else {
		row.CalibratedAt = timePointer(now)
	}
	row.UpdatedAt = now
	result.AfterUp = effectiveValue(row.AccumulatedUp, row.OffsetUp)
	result.AfterDown = effectiveValue(row.AccumulatedDown, row.OffsetDown)
	result.OffsetUp, result.OffsetDown = row.OffsetUp, row.OffsetDown

	closures, maxSeq := m.snapshotClosuresLocked(nil)
	if err := m.commitLedgerLocked(ctx, []models.ClientTrafficLedger{row}, closures); err != nil {
		return CalibrationResult{}, fmt.Errorf("persist traffic calibration: %w", err)
	}
	entry.row = row
	entry.dirty = false
	entry.version++
	m.trimClosuresLocked(maxSeq)
	return result, nil
}

func (m *Manager) ResetCycle(ctx context.Context, uuid string, at time.Time) error {
	if uuid == "" {
		return errors.New("client UUID is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()

	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRunningLocked(); err != nil {
		return err
	}

	entry := m.entries[uuid]
	if entry == nil {
		return ErrLedgerNotFound
	}
	if entry.row.Mode == ModeAgent {
		return errors.New("cycle reset is not supported in agent mode")
	}
	if isAutomaticMode(entry.row.Mode) && needsRollover(entry.row, at) {
		if err := m.refreshAutomaticCycleLocked(entry, at); err != nil {
			return err
		}
	}

	row := cloneLedger(entry.row)
	var extraCloses []ClosedCycleSnapshot
	if row.CycleStartAt != nil {
		extraCloses = append(extraCloses, closeSnapshot(row, at, CloseReasonManualReset))
	}
	clearCycleCounters(&row)
	row.CycleStartAt = timePointer(at)
	row.CycleEndAt = nil
	if isAutomaticMode(row.Mode) {
		start, end, err := cycleBoundsWithDefault(configFromModel(row), m.defaultTimezone, at)
		if err != nil {
			return err
		}
		row.CycleStartAt, row.CycleEndAt = start, end
	}
	row.UpdatedAt = time.Now().UTC()

	closures, maxSeq := m.snapshotClosuresLocked(extraCloses)
	if err := m.commitLedgerLocked(ctx, []models.ClientTrafficLedger{row}, closures); err != nil {
		return fmt.Errorf("reset traffic cycle: %w", err)
	}
	entry.row = row
	entry.dirty = false
	entry.version++
	m.trimClosuresLocked(maxSeq)
	return nil
}

func (m *Manager) DeleteClient(uuid string) error {
	if uuid == "" {
		return errors.New("client UUID is required")
	}
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRunningLocked(); err != nil {
		return err
	}

	var extraCloses []ClosedCycleSnapshot
	if entry := m.entries[uuid]; entry != nil && entry.row.Mode != ModeAgent && entry.row.CycleStartAt != nil {
		extraCloses = append(extraCloses, closeSnapshot(entry.row, time.Now().UTC(), CloseReasonClientDeleted))
	}
	closures, maxSeq := m.snapshotClosuresLocked(extraCloses)
	if err := m.deleteClientRowsTransaction(context.Background(), uuid, false, closures); err != nil {
		return fmt.Errorf("delete client traffic ledger: %w", err)
	}
	delete(m.entries, uuid)
	m.trimClosuresLocked(maxSeq)
	return nil
}

// DeleteClientWithRecord atomically deletes the client row and its traffic
// ledger row in one database transaction, including any queued cycle-close
// hook writes. The in-memory entry is removed only after the transaction
// commits, so a failed deletion leaves both the database and memory unchanged.
func (m *Manager) DeleteClientWithRecord(ctx context.Context, uuid string) error {
	if uuid == "" {
		return errors.New("client UUID is required")
	}
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.requireRunningLocked(); err != nil {
		return err
	}

	var extraCloses []ClosedCycleSnapshot
	if entry := m.entries[uuid]; entry != nil && entry.row.Mode != ModeAgent && entry.row.CycleStartAt != nil {
		extraCloses = append(extraCloses, closeSnapshot(entry.row, time.Now().UTC(), CloseReasonClientDeleted))
	}
	closures, maxSeq := m.snapshotClosuresLocked(extraCloses)
	if err := m.deleteClientRowsTransaction(ctx, uuid, true, closures); err != nil {
		return fmt.Errorf("delete client and traffic ledger: %w", err)
	}
	delete(m.entries, uuid)
	m.trimClosuresLocked(maxSeq)
	return nil
}

func (m *Manager) deleteClientRowsTransaction(ctx context.Context, uuid string, deleteClient bool, closures []ClosedCycleSnapshot) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if deleteClient {
			if err := tx.Delete(&models.Client{}, "uuid = ?", uuid).Error; err != nil {
				return err
			}
		}
		if len(closures) > 0 && m.hook != nil {
			if err := m.hook(tx, closures); err != nil {
				return err
			}
		}
		return tx.Delete(&models.ClientTrafficLedger{}, "uuid = ?", uuid).Error
	})
}

// --- internal helpers ---

func (m *Manager) requireRunningLocked() error {
	if !m.initialized {
		return ErrManagerNotInitialized
	}
	if m.stopped {
		return ErrManagerStopped
	}
	return nil
}

func (m *Manager) assignCycleWindowLocked(row *models.ClientTrafficLedger, now time.Time) error {
	switch row.Mode {
	case ModeMonthly, ModeFixedDays:
		start, end, err := cycleBoundsWithDefault(configFromModel(*row), m.defaultTimezone, now)
		if err != nil {
			return err
		}
		row.CycleStartAt, row.CycleEndAt = start, end
	case ModeManual:
		if row.CycleStartAt == nil {
			row.CycleStartAt = timePointer(now)
		}
		row.CycleEndAt = nil
	default:
		row.CycleStartAt, row.CycleEndAt = nil, nil
	}
	return nil
}

// refreshAutomaticCycleLocked applies refreshEntryCycle to the live entry and
// queues any produced cycle-close snapshot for the next flush.
func (m *Manager) refreshAutomaticCycleLocked(entry *ledgerEntry, at time.Time) error {
	closeSnap, err := refreshEntryCycle(entry, m.defaultTimezone, at)
	if err != nil {
		return err
	}
	if closeSnap != nil {
		m.nextClosure++
		m.closures = append(m.closures, queuedClose{
			sequence: m.nextClosure,
			snapshot: *closeSnap,
		})
	}
	return nil
}

// refreshEntryCycle advances the entry's automatic cycle window, queuing
// nothing: it only mutates the given entry and returns any rollover close
// snapshot for the caller to handle. Because it never touches manager state,
// the same function backs the read-only post-shutdown display projection on
// a scratch entry copy.
func refreshEntryCycle(entry *ledgerEntry, defaultTimezone string, at time.Time) (*ClosedCycleSnapshot, error) {
	row := &entry.row
	if !isAutomaticMode(row.Mode) {
		return nil, nil
	}
	start, end, err := cycleBoundsWithDefault(configFromModel(*row), defaultTimezone, at)
	if err != nil || start == nil || end == nil {
		return nil, err
	}
	if row.CycleStartAt == nil {
		row.CycleStartAt, row.CycleEndAt = start, end
		row.UpdatedAt = at.UTC()
		markDirty(entry)
		return nil, nil
	}
	if start.After(row.CycleStartAt.UTC()) {
		// A boundary shift caused purely by a timezone configuration change
		// still describes the SAME accounting cycle in absolute time: the old
		// end has not passed yet, so the accumulated usage must be preserved
		// and only the boundaries updated in place. A real rollover is only
		// when the current cycle has actually expired.
		if row.CycleEndAt != nil && at.Before(row.CycleEndAt.UTC()) {
			row.CycleStartAt, row.CycleEndAt = cloneTime(start), cloneTime(end)
			row.UpdatedAt = at.UTC()
			markDirty(entry)
			return nil, nil
		}
		rebaseline := row.RawLastAt == nil || at.Sub(row.RawLastAt.UTC()) > crossCycleGapLimit
		closeSnap := rolloverEntry(entry, start, end, rebaseline)
		return closeSnap, nil
	}
	if !start.Equal(row.CycleStartAt.UTC()) || row.CycleEndAt == nil || !end.Equal(row.CycleEndAt.UTC()) {
		row.CycleStartAt, row.CycleEndAt = start, end
		row.UpdatedAt = at.UTC()
		markDirty(entry)
	}
	return nil, nil
}

// rolloverEntry resets the entry into a fresh cycle and returns the close
// snapshot for the cycle it left behind, or nil when there was nothing to close.
func rolloverEntry(entry *ledgerEntry, start, end *time.Time, rebaseline bool) *ClosedCycleSnapshot {
	row := &entry.row
	var closeSnap *ClosedCycleSnapshot
	if row.CycleStartAt != nil {
		closeAt := start.UTC()
		if row.CycleEndAt != nil && row.CycleEndAt.Before(closeAt) {
			closeAt = row.CycleEndAt.UTC()
		}
		snap := closeSnapshot(*row, closeAt, CloseReasonAutoRollover)
		closeSnap = &snap
	}
	clearCycleCounters(row)
	row.CycleStartAt, row.CycleEndAt = cloneTime(start), cloneTime(end)
	if rebaseline {
		row.HasBaseline = false
	}
	row.UpdatedAt = time.Now().UTC()
	markDirty(entry)
	return closeSnap
}

func (m *Manager) snapshotClosuresLocked(extra []ClosedCycleSnapshot) ([]ClosedCycleSnapshot, uint64) {
	out := make([]ClosedCycleSnapshot, 0, len(m.closures)+len(extra))
	var maxSeq uint64
	for _, item := range m.closures {
		out = append(out, item.snapshot)
		if item.sequence > maxSeq {
			maxSeq = item.sequence
		}
	}
	out = append(out, extra...)
	return out, maxSeq
}

func (m *Manager) trimClosuresLocked(maxSeq uint64) {
	if maxSeq == 0 {
		return
	}
	kept := m.closures[:0]
	for _, item := range m.closures {
		if item.sequence > maxSeq {
			kept = append(kept, item)
		}
	}
	m.closures = kept
}

func (m *Manager) commitLedgerLocked(ctx context.Context, rows []models.ClientTrafficLedger, closures []ClosedCycleSnapshot) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := upsertLedgers(tx, rows); err != nil {
			return err
		}
		if len(closures) > 0 && m.hook != nil {
			return m.hook(tx, closures)
		}
		return nil
	})
}

func isAutomaticMode(mode string) bool {
	return mode == ModeMonthly || mode == ModeFixedDays
}

func needsRollover(row models.ClientTrafficLedger, now time.Time) bool {
	if !isAutomaticMode(row.Mode) {
		return false
	}
	return row.CycleStartAt == nil || row.CycleEndAt == nil || !now.Before(row.CycleEndAt.UTC())
}

func clearCycleCounters(row *models.ClientTrafficLedger) {
	row.AccumulatedUp, row.AccumulatedDown = 0, 0
	row.OffsetUp, row.OffsetDown = 0, 0
	row.CalibratedAt = nil
}

func clearBaseline(row *models.ClientTrafficLedger) {
	row.HasBaseline = false
	row.RawLastUp, row.RawLastDown, row.RawLastUptime = 0, 0, 0
	row.RawLastAt = nil
}

func seedBaseline(row *models.ClientTrafficLedger, latestRaw *v2.Report, fallbackAt time.Time) {
	if latestRaw == nil {
		clearBaseline(row)
		return
	}
	at := latestRaw.UpdatedAt
	if at.IsZero() {
		at = fallbackAt
	}
	row.RawLastUp = max(latestRaw.Network.TotalUp, 0)
	row.RawLastDown = max(latestRaw.Network.TotalDown, 0)
	row.RawLastUptime = latestRaw.Uptime
	row.RawLastAt = timePointer(at)
	row.HasBaseline = true
}

func markDirty(entry *ledgerEntry) {
	entry.dirty = true
	entry.version++
}

func validateCalibration(req CalibrateRequest) error {
	if (req.TargetUp != nil && *req.TargetUp < 0) ||
		(req.TargetDown != nil && *req.TargetDown < 0) ||
		(req.TargetTotal != nil && *req.TargetTotal < 0) {
		return errors.New("calibration targets must be non-negative")
	}
	if req.TargetTotal != nil && (req.TargetUp != nil || req.TargetDown != nil || req.DeltaUp != nil || req.DeltaDown != nil) {
		return errors.New("target_total cannot be combined with directional calibration")
	}
	if (req.TargetUp != nil && req.DeltaUp != nil) || (req.TargetDown != nil && req.DeltaDown != nil) {
		return errors.New("a direction cannot specify both target and delta")
	}
	if req.TargetTotal == nil && req.TargetUp == nil && req.TargetDown == nil && req.DeltaUp == nil && req.DeltaDown == nil {
		return errors.New("calibration target or delta is required")
	}
	return nil
}

func splitTargetTotal(total, currentUp, currentDown int64) (int64, int64) {
	sum := new(big.Int).Add(big.NewInt(currentUp), big.NewInt(currentDown))
	if sum.Sign() == 0 {
		return total, 0
	}
	up := new(big.Int).Mul(big.NewInt(total), big.NewInt(currentUp))
	up.Quo(up, sum)
	upVal := up.Int64()
	return upVal, total - upVal
}

func sameCycleConfig(a, b CycleConfig) bool {
	return a.Mode == b.Mode && a.ResetDay == b.ResetDay && a.IntervalDays == b.IntervalDays &&
		a.Timezone == b.Timezone && sameOptionalTime(a.AnchorAt, b.AnchorAt)
}

func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func cloneLedger(row models.ClientTrafficLedger) models.ClientTrafficLedger {
	row.AnchorAt = cloneTime(row.AnchorAt)
	row.CycleStartAt = cloneTime(row.CycleStartAt)
	row.CycleEndAt = cloneTime(row.CycleEndAt)
	row.RawLastAt = cloneTime(row.RawLastAt)
	row.CalibratedAt = cloneTime(row.CalibratedAt)
	return row
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func upsertLedgers(tx *gorm.DB, rows []models.ClientTrafficLedger) error {
	if len(rows) == 0 {
		return nil
	}
	columns := []string{
		"mode", "reset_day", "interval_days", "anchor_at", "timezone",
		"cycle_start_at", "cycle_end_at", "accumulated_up", "accumulated_down",
		"offset_up", "offset_down", "has_baseline", "raw_last_up", "raw_last_down",
		"raw_last_uptime", "raw_last_at", "calibrated_at", "updated_at",
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uuid"}},
		DoUpdates: clause.AssignmentColumns(columns),
	}).Create(&rows).Error
}

// --- Process-wide singleton wrapper ---

var (
	defaultInitMu sync.Mutex
	defaultLedger atomic.Pointer[Manager]
	pendingHook   CycleCloseHook
	pendingTZ     string
)

func getOrInitDefault(ctx context.Context) (*Manager, error) {
	if m := defaultLedger.Load(); m != nil {
		return m, nil
	}
	defaultInitMu.Lock()
	defer defaultInitMu.Unlock()
	if m := defaultLedger.Load(); m != nil {
		return m, nil
	}
	if err := dbcore.Initialize(); err != nil {
		return nil, err
	}
	tz := pendingTZ
	if tz == "" {
		if settings, err := appconfig.GetManyAs[appconfig.Settings](); err == nil && settings != nil {
			tz = settings.TrafficDefaultTimezone
		}
	}
	m := NewManager(dbcore.GetDBInstance(), tz)
	if pendingHook != nil {
		m.SetCycleCloseHook(pendingHook)
	}
	if err := m.Initialize(ctx); err != nil {
		return nil, err
	}
	defaultLedger.Store(m)
	return m, nil
}

func Initialize(ctx context.Context) error {
	_, err := getOrInitDefault(ctx)
	return err
}

func ObserveReport(report v2.Report) error {
	m := defaultLedger.Load()
	if m == nil {
		var err error
		if m, err = getOrInitDefault(context.Background()); err != nil {
			return err
		}
	}
	return m.ObserveReport(report)
}

func ResolveDisplay(uuid string, rawUp, rawDown int64) DisplayTraffic {
	m := defaultLedger.Load()
	if m == nil {
		return DisplayTraffic{Up: max(rawUp, 0), Down: max(rawDown, 0), Mode: ModeAgent}
	}
	return m.ResolveDisplay(uuid, rawUp, rawDown)
}

func Get(uuid string) (LedgerSnapshot, bool) {
	m, err := getOrInitDefault(context.Background())
	if err != nil {
		return LedgerSnapshot{}, false
	}
	return m.Get(uuid)
}

func DefaultTimezone() string {
	if m := defaultLedger.Load(); m != nil {
		return m.DefaultTimezone()
	}
	defaultInitMu.Lock()
	defer defaultInitMu.Unlock()
	return pendingTZ
}

func FlushDirty(ctx context.Context) error {
	m, err := getOrInitDefault(ctx)
	if err != nil {
		return err
	}
	return m.FlushDirty(ctx)
}

func Shutdown(ctx context.Context) error {
	m := defaultLedger.Load()
	if m == nil {
		return nil
	}
	return m.Shutdown(ctx)
}

func UpdateConfig(ctx context.Context, uuid string, req UpdateConfigRequest, latestRaw *v2.Report) error {
	m, err := getOrInitDefault(ctx)
	if err != nil {
		return err
	}
	return m.UpdateConfig(ctx, uuid, req, latestRaw)
}

func Calibrate(ctx context.Context, uuid string, req CalibrateRequest) (CalibrationResult, error) {
	m, err := getOrInitDefault(ctx)
	if err != nil {
		return CalibrationResult{}, err
	}
	return m.Calibrate(ctx, uuid, req)
}

func ResetCycle(ctx context.Context, uuid string, at time.Time) error {
	m, err := getOrInitDefault(ctx)
	if err != nil {
		return err
	}
	return m.ResetCycle(ctx, uuid, at)
}

func DeleteClient(uuid string) error {
	m, err := getOrInitDefault(context.Background())
	if err != nil {
		return err
	}
	return m.DeleteClient(uuid)
}

// DeleteClientWithRecord is the process-wide atomic client+ledger deletion
// entry point used by admin removal.
func DeleteClientWithRecord(ctx context.Context, uuid string) error {
	m, err := getOrInitDefault(ctx)
	if err != nil {
		return err
	}
	return m.DeleteClientWithRecord(ctx, uuid)
}

func SetCycleCloseHook(hook CycleCloseHook) {
	defaultInitMu.Lock()
	pendingHook = hook
	m := defaultLedger.Load()
	defaultInitMu.Unlock()
	if m != nil {
		m.SetCycleCloseHook(hook)
	}
}

func SetDefaultTimezone(timezone string) error {
	timezone = strings.TrimSpace(timezone)
	if timezone != "" {
		if _, err := loadLocationCached(timezone); err != nil {
			return fmt.Errorf("invalid traffic timezone %q: %w", timezone, err)
		}
	}
	defaultInitMu.Lock()
	pendingTZ = timezone
	m := defaultLedger.Load()
	defaultInitMu.Unlock()
	if m != nil {
		return m.SetDefaultTimezone(timezone)
	}
	return nil
}
