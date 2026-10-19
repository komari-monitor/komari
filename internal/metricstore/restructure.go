package metricstore

import (
	"context"
	"fmt"

	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

// RestructureProgress is the authenticated guide's stable progress payload.
type RestructureProgress struct {
	Phase         string
	CurrentMetric string
	RowsDone      int64
	RowsTotal     int64
	MetricsDone   int
	MetricsTotal  int
}

type RestructureResult struct {
	BeforeBytes int64
	AfterBytes  int64
	RowsCopied  int64
	Metrics     int
}

// StructureUpgradeRequired checks the configured metric store without creating
// tables. It is called before normal metric-store initialization so an existing
// installation is never rebuilt at startup without an administrator action.
func StructureUpgradeRequired(ctx context.Context) (bool, error) {
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return false, err
	}
	return structureUpgradeRequiredForConfig(ctx, cfg)
}

func structureUpgradeRequiredForConfig(ctx context.Context, cfg *MetricStoreConfig) (bool, error) {
	s, err := openWithoutMigration(ctx, cfg)
	if err != nil {
		return false, err
	}
	defer s.Close()
	return s.NeedsRestructure(ctx)
}

// openWithoutMigration opens cfg without creating or altering any table.
func openWithoutMigration(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return nil, err
	}
	return metric.Open(ctx, metricCfg)
}

// RestructureConfiguredStore performs the explicit data copy, table swap and
// one physical reclaim pass used by the upgrade guide.
func RestructureConfiguredStore(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	return rebuildConfiguredStore(ctx, report, (*metric.Store).Restructure)
}

// DiscardConfiguredStoreHistory completes the same schema upgrade as
// RestructureConfiguredStore but drops every historical sample instead of
// copying it. Metric definitions remain available for new ingest.
func DiscardConfiguredStoreHistory(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	return rebuildConfiguredStore(ctx, report, (*metric.Store).DiscardHistory)
}

type storeRebuild func(*metric.Store, context.Context, func(metric.RestructureProgress)) (metric.RestructureResult, error)

func rebuildConfiguredStore(ctx context.Context, report func(RestructureProgress), rebuild storeRebuild) (RestructureResult, error) {
	if report == nil {
		report = func(RestructureProgress) {}
	}
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return RestructureResult{}, err
	}
	s, err := openWithoutMigration(ctx, cfg)
	if err != nil {
		return RestructureResult{}, err
	}
	defer s.Close()

	before, err := s.LegacyStorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure legacy metric storage: %w", err)
	}
	result, err := rebuild(s, ctx, func(progress metric.RestructureProgress) {
		report(RestructureProgress{Phase: progress.Phase, CurrentMetric: progress.Current, RowsDone: progress.RowsDone, RowsTotal: progress.RowsTotal, MetricsDone: progress.MetricsDone, MetricsTotal: progress.MetricsTotal})
	})
	if err != nil {
		return RestructureResult{}, err
	}
	report(RestructureProgress{Phase: "reclaiming", RowsDone: result.RowsCopied, RowsTotal: result.RowsCopied, MetricsDone: result.Metrics, MetricsTotal: result.Metrics})
	if err := s.ReclaimSpace(ctx); err != nil {
		return RestructureResult{}, fmt.Errorf("reclaim metric storage: %w", err)
	}
	after, err := s.StorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure rebuilt metric storage: %w", err)
	}
	return RestructureResult{BeforeBytes: before, AfterBytes: after, RowsCopied: result.RowsCopied, Metrics: result.Metrics}, nil
}
