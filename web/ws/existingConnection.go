package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/common"
)

var (
	connectedClients = make(map[string]*SafeConn)
	ConnectedUsers   = []*websocket.Conn{}
	latestReport     = make(map[string]*common.Report)
	// presenceOnly stores online state for non-WebSocket agents (e.g., Nezha gRPC)
	// value keeps connectionID and a soft expiration to avoid flicker
	presenceOnly = make(map[string]struct {
		id     int64
		expire time.Time
	})
	mu = sync.RWMutex{}
)

func GetConnectedClients() map[string]*SafeConn {
	mu.RLock()
	defer mu.RUnlock()
	clientsCopy := make(map[string]*SafeConn)
	for k, v := range connectedClients {
		clientsCopy[k] = v
	}
	return clientsCopy
}

func SetConnectedClients(uuid string, conn *SafeConn) {
	mu.Lock()
	defer mu.Unlock()
	connectedClients[uuid] = conn
}
func DeleteClientConditionally(uuid string, connToRemove *SafeConn) {
	mu.Lock()
	defer mu.Unlock()

	// 检查当前 map 里的 conn 是否就是要删除的这一个
	if currentConn, exists := connectedClients[uuid]; exists && currentConn == connToRemove {
		delete(connectedClients, uuid)
	}
}
func DeleteConnectedClients(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	// 只从 map 中删除，不再负责关闭连接
	delete(connectedClients, uuid)
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
func GetLatestReport() map[string]*common.Report {
	mu.RLock()
	defer mu.RUnlock()
	reportCopy := make(map[string]*common.Report)
	for k, v := range latestReport {
		reportCopy[k] = v
	}
	return reportCopy
}
func SetLatestReport(uuid string, report *common.Report) {
	mu.Lock()
	defer mu.Unlock()
	latestReport[uuid] = report
}
func DeleteLatestReport(uuid string) {
	mu.Lock()
	defer mu.Unlock()
	delete(latestReport, uuid)
}
