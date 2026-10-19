package metric

import "time"

// DashboardBucketsReady reports whether the dashboard_buckets mirror has
// finished a full retention-window reconcile and may serve 5-minute reads
// that start at or after DashboardCoveredFrom.
func (s *Store) DashboardBucketsReady() bool {
	return s.dashboardReady.Load() && s.dashboardCoveredFromMilli.Load() > 0
}

// DashboardCoveredFrom returns the earliest time the mirror guarantees
// coverage for. Zero means the mirror is not ready for reads.
func (s *Store) DashboardCoveredFrom() time.Time {
	milli := s.dashboardCoveredFromMilli.Load()
	if milli <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(milli).UTC()
}
