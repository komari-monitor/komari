package agent

import (
	"sort"
	"sync"
	"time"

	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/web/connection"
)

var (
	connectedClients  = make(map[string]*connection.SafeConn)
	connectedClientV2 = make(map[string]bool)
	latestReport      = make(map[string]*v1.Report)
	recentReports     = make(map[string][]v1.Report)
	// presenceOnly stores online state for non-WebSocket agents.
	// value keeps connectionID and a soft expiration to avoid flicker
	presenceOnly = make(map[string]struct {
		id     int64
		expire time.Time
	})
	mu = sync.RWMutex{}
)

const recentReportRetention = time.Minute

func GetConnectedClients() map[string]*connection.SafeConn {
	mu.RLock()
	defer mu.RUnlock()
	clientsCopy := make(map[string]*connection.SafeConn)
	for k, v := range connectedClients {
		clientsCopy[k] = v
	}
	return clientsCopy
}

func SetConnectedClients(uuid string, conn *connection.SafeConn) {
	mu.Lock()
	defer mu.Unlock()
	connectedClients[uuid] = conn
}

func SetClientProtocolVersion(uuid string, version int) {
	mu.Lock()
	defer mu.Unlock()
	connectedClientV2[uuid] = version >= 2
}

func IsV2Client(uuid string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return connectedClientV2[uuid]
}

func DeleteClientConditionally(uuid string, connToRemove *connection.SafeConn) {
	mu.Lock()
	defer mu.Unlock()

	// 检查当前 map 里的 conn 是否就是要删除的这一个
	if currentConn, exists := connectedClients[uuid]; exists && currentConn == connToRemove {
		delete(connectedClients, uuid)
		delete(connectedClientV2, uuid)
	}
}
func DeleteConnectedClients(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	// 只从 map 中删除，不再负责关闭连接
	delete(connectedClients, uuid)
	delete(connectedClientV2, uuid)
}

// SetPresence sets or clears presence for non-WebSocket agents.
// When present=false, it only clears if the connectionID matches current one.
// KeepAlivePresence sets presence with TTL for non-WebSocket agents.
func KeepAlivePresence(uuid string, connectionID int64, ttl time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	presenceOnly[uuid] = struct {
		id     int64
		expire time.Time
	}{id: connectionID, expire: time.Now().Add(ttl)}
}

var defaultPresenceTTL = 20 * time.Second

// SetPresence keeps compatibility with existing callers.
func SetPresence(uuid string, connectionID int64, present bool) {
	mu.Lock()
	defer mu.Unlock()
	if present {
		presenceOnly[uuid] = struct {
			id     int64
			expire time.Time
		}{id: connectionID, expire: time.Now().Add(defaultPresenceTTL)}
		return
	}
	if cur, ok := presenceOnly[uuid]; ok && cur.id == connectionID {
		delete(presenceOnly, uuid)
	}
}

// GetAllOnlineUUIDs returns a de-duplicated list of online UUIDs from both WebSocket and non-WebSocket agents.
func GetAllOnlineUUIDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	set := make(map[string]struct{})
	for k := range connectedClients {
		set[k] = struct{}{}
	}
	now := time.Now()
	for k, v := range presenceOnly {
		if v.expire.After(now) {
			set[k] = struct{}{}
		}
	}
	res := make([]string, 0, len(set))
	for k := range set {
		res = append(res, k)
	}
	return res
}
func GetLatestReport() map[string]*v1.Report {
	mu.RLock()
	defer mu.RUnlock()
	reportCopy := make(map[string]*v1.Report)
	for k, v := range latestReport {
		if v == nil {
			continue
		}
		item := *v
		reportCopy[k] = &item
	}
	return reportCopy
}

// RecordReport updates the latest runtime state and keeps only the short raw
// window used by recent-status compatibility endpoints.
func RecordReport(report v1.Report) {
	if report.UUID == "" {
		return
	}
	if report.UpdatedAt.IsZero() {
		report.UpdatedAt = time.Now().UTC()
	}
	mu.Lock()
	defer mu.Unlock()
	if latest := latestReport[report.UUID]; latest == nil || !report.UpdatedAt.Before(latest.UpdatedAt) {
		item := report
		latestReport[report.UUID] = &item
	}
	cutoff := time.Now().UTC().Add(-recentReportRetention)
	reports := reportsAfter(recentReports[report.UUID], cutoff)
	if report.UpdatedAt.Before(cutoff) {
		recentReports[report.UUID] = reports
		return
	}
	insertAt := sort.Search(len(reports), func(i int) bool {
		return reports[i].UpdatedAt.After(report.UpdatedAt)
	})
	reports = append(reports, v1.Report{})
	copy(reports[insertAt+1:], reports[insertAt:])
	reports[insertAt] = report
	recentReports[report.UUID] = reports
}

func GetRecentReports(uuid string) []v1.Report {
	mu.Lock()
	defer mu.Unlock()
	reports := reportsAfter(recentReports[uuid], time.Now().UTC().Add(-recentReportRetention))
	if len(reports) == 0 {
		delete(recentReports, uuid)
		return []v1.Report{}
	}
	recentReports[uuid] = reports
	return append([]v1.Report(nil), reports...)
}

func reportsAfter(reports []v1.Report, cutoff time.Time) []v1.Report {
	first := 0
	for first < len(reports) && reports[first].UpdatedAt.Before(cutoff) {
		first++
	}
	out := make([]v1.Report, len(reports)-first)
	copy(out, reports[first:])
	return out
}

func DeleteLatestReport(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	delete(latestReport, uuid)
	delete(recentReports, uuid)
}
