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
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return false, err
	}
	store, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return false, err
	}
	defer store.Close()
	return store.NeedsRestructure(ctx)
}

// RestructureConfiguredStore performs the explicit data copy, table swap and
// one physical reclaim pass used by the upgrade guide.
func RestructureConfiguredStore(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return RestructureResult{}, err
	}
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return RestructureResult{}, err
	}
	store, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return RestructureResult{}, err
	}
	defer store.Close()

	before, err := store.LegacyStorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure legacy metric storage: %w", err)
	}
	result, err := store.Restructure(ctx, func(progress metric.RestructureProgress) {
		if report == nil {
			return
		}
		report(RestructureProgress{Phase: progress.Phase, CurrentMetric: progress.Current, RowsDone: progress.RowsDone, RowsTotal: progress.RowsTotal, MetricsDone: progress.MetricsDone, MetricsTotal: progress.MetricsTotal})
	})
	if err != nil {
		return RestructureResult{}, err
	}
	if report != nil {
		report(RestructureProgress{Phase: "reclaiming", RowsDone: result.RowsCopied, RowsTotal: result.RowsCopied, MetricsDone: result.Metrics, MetricsTotal: result.Metrics})
	}
	if err := store.ReclaimSpace(ctx); err != nil {
		return RestructureResult{}, fmt.Errorf("reclaim metric storage: %w", err)
	}
	after, err := store.StorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure rebuilt metric storage: %w", err)
	}
	return RestructureResult{BeforeBytes: before, AfterBytes: after, RowsCopied: result.RowsCopied, Metrics: result.Metrics}, nil
}

// DiscardConfiguredStoreHistory completes the same schema upgrade as
// RestructureConfiguredStore but drops every historical sample instead of
// copying it. Metric definitions remain available for new ingest.
func DiscardConfiguredStoreHistory(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return RestructureResult{}, err
	}
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return RestructureResult{}, err
	}
	store, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return RestructureResult{}, err
	}
	defer store.Close()

	before, err := store.LegacyStorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure legacy metric storage: %w", err)
	}
	result, err := store.DiscardHistory(ctx, func(progress metric.RestructureProgress) {
		if report == nil {
			return
		}
		report(RestructureProgress{Phase: progress.Phase, CurrentMetric: progress.Current, RowsDone: progress.RowsDone, RowsTotal: progress.RowsTotal, MetricsDone: progress.MetricsDone, MetricsTotal: progress.MetricsTotal})
	})
	if err != nil {
		return RestructureResult{}, err
	}
	if report != nil {
		report(RestructureProgress{Phase: "reclaiming", RowsDone: result.RowsCopied, RowsTotal: result.RowsCopied, MetricsDone: result.Metrics, MetricsTotal: result.Metrics})
	}
	if err := store.ReclaimSpace(ctx); err != nil {
		return RestructureResult{}, fmt.Errorf("reclaim metric storage: %w", err)
	}
	after, err := store.StorageSize(ctx)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("measure rebuilt metric storage: %w", err)
	}
	return RestructureResult{BeforeBytes: before, AfterBytes: after, RowsCopied: result.RowsCopied, Metrics: result.Metrics}, nil
}
