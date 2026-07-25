package metric

// MigrationProgress is a point-in-time snapshot of an automatic storage
// migration. Current and Total describe the active phase, while Preserved is
// the number of source rows that have already passed round-trip validation.
type MigrationProgress struct {
	Phase     string
	Current   int64
	Total     int64
	Preserved int64
}

// MigrationProgressFunc observes automatic storage migration progress.
type MigrationProgressFunc func(MigrationProgress)

const (
	MigrationPhasePreparing             = "preparing"
	MigrationPhaseNormalizingSeries     = "normalizing_series"
	MigrationPhaseNormalizingPoints     = "normalizing_points"
	MigrationPhaseNormalizingRollups    = "normalizing_rollups"
	MigrationPhaseEncodingPoints        = "encoding_points"
	MigrationPhaseEncodingRollups       = "encoding_rollups"
	MigrationPhaseUpgradingRollupBlocks = "upgrading_rollup_blocks"
	MigrationPhaseValidating            = "validating"
	MigrationPhaseCommitting            = "committing"
	MigrationPhaseReclaiming            = "reclaiming"
	MigrationPhaseCompleted             = "completed"
)

func (s *Store) reportMigrationProgress(phase string, current, total, preserved int64) {
	if s == nil || s.cfg.MigrationProgress == nil {
		return
	}
	if current < 0 {
		current = 0
	}
	if total < current {
		total = current
	}
	s.cfg.MigrationProgress(MigrationProgress{
		Phase:     phase,
		Current:   current,
		Total:     total,
		Preserved: preserved,
	})
}
