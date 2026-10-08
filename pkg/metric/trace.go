package metric

// DashboardBucketsReady reports whether the dashboard_buckets mirror is
// populated and eligible for 5-minute dashboard reads.
func (s *Store) DashboardBucketsReady() bool {
	return s.dashboardReady.Load()
}
