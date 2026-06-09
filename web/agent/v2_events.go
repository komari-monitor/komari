package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

const (
	v2EventQueueLimit = 128
	v2EventTTL        = 5 * time.Minute
	v2PingEventTTL    = 3 * time.Second
)

type v2EventQueue struct {
	events []v2.Event
	signal chan struct{}
}

var (
	v2EventMu     sync.Mutex
	v2EventQueues = make(map[string]*v2EventQueue)
)

func getV2EventQueueLocked(uuid string) *v2EventQueue {
	q := v2EventQueues[uuid]
	if q == nil {
		q = &v2EventQueue{signal: make(chan struct{})}
		v2EventQueues[uuid] = q
	}
	return q
}

func DispatchV2Event(uuid, method string, params any) bool {
	if conn := GetConnectedClients()[uuid]; conn != nil {
		payload := v2.Request{JSONRPC: v2.Version, Method: method, Params: params}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	if !IsV2Client(uuid) {
		return false
	}
	EnqueueV2Event(uuid, method, params)
	return true
}

func DispatchPing(uuid string, legacy any, params v2.PingParams) bool {
	if conn := GetConnectedClients()[uuid]; conn != nil {
		payload := legacy
		if IsV2Client(uuid) {
			payload = v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPing, Params: params}
		}
		if conn.WriteJSON(payload) == nil {
			return true
		}
	}
	if !IsV2Client(uuid) {
		return false
	}
	EnqueueV2Event(uuid, v2.MethodAgentPing, params)
	return true
}

func IsAgentOnline(uuid string) bool {
	if GetConnectedClients()[uuid] != nil {
		return true
	}
	return IsV2Client(uuid)
}

func EnqueueV2Event(uuid, method string, params any) v2.Event {
	now := time.Now()
	ttl := v2EventTTL
	if method == v2.MethodAgentPing {
		ttl = v2PingEventTTL
	}
	event := v2.Event{
		ID:        newV2EventID(),
		Method:    method,
		Params:    params,
		CreatedAt: now.Format(time.RFC3339Nano),
		ExpiresAt: now.Add(ttl).Format(time.RFC3339Nano),
	}

	v2EventMu.Lock()
	q := getV2EventQueueLocked(uuid)
	pruneExpiredV2EventsLocked(q)
	coalesceV2EventLocked(q, event)
	q.events = append(q.events, event)
	if len(q.events) > v2EventQueueLimit {
		q.events = q.events[len(q.events)-v2EventQueueLimit:]
	}
	close(q.signal)
	q.signal = make(chan struct{})
	v2EventMu.Unlock()

	return event
}

func newV2EventID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func coalesceV2EventLocked(q *v2EventQueue, event v2.Event) {
	key := v2EventCoalesceKey(event)
	if key == "" {
		return
	}
	filtered := q.events[:0]
	for _, existing := range q.events {
		if v2EventCoalesceKey(existing) != key {
			filtered = append(filtered, existing)
		}
	}
	q.events = filtered
}

func v2EventCoalesceKey(event v2.Event) string {
	if event.Method != v2.MethodAgentPing {
		return ""
	}
	var params v2.PingParams
	if err := bindV2EventParams(event.Params, &params); err != nil || params.TaskID == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", event.Method, params.TaskID)
}

func bindV2EventParams(raw any, target any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}

func ackV2EventsLocked(q *v2EventQueue, ackIDs []string) {
	if len(ackIDs) == 0 || len(q.events) == 0 {
		return
	}
	acked := make(map[string]struct{}, len(ackIDs))
	for _, id := range ackIDs {
		acked[id] = struct{}{}
	}
	filtered := q.events[:0]
	for _, event := range q.events {
		if _, ok := acked[event.ID]; !ok {
			filtered = append(filtered, event)
		}
	}
	q.events = filtered
}

func pruneExpiredV2EventsLocked(q *v2EventQueue) {
	if len(q.events) == 0 {
		return
	}
	now := time.Now()
	filtered := q.events[:0]
	for _, event := range q.events {
		if event.ExpiresAt == "" {
			filtered = append(filtered, event)
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, event.ExpiresAt)
		if err != nil || expiresAt.After(now) {
			filtered = append(filtered, event)
		}
	}
	q.events = filtered
}

func TakeV2Events(uuid string, ackIDs []string, limit int) []v2.Event {
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := getV2EventQueueLocked(uuid)
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q)
	return takeV2EventsLocked(q, limit)
}

func AckV2Events(uuid string, ackIDs []string) {
	if len(ackIDs) == 0 {
		return
	}
	v2EventMu.Lock()
	defer v2EventMu.Unlock()

	q := v2EventQueues[uuid]
	if q == nil {
		return
	}
	ackV2EventsLocked(q, ackIDs)
}

func takeV2EventsLocked(q *v2EventQueue, limit int) []v2.Event {
	if limit <= 0 || limit > len(q.events) {
		limit = len(q.events)
	}
	events := make([]v2.Event, limit)
	copy(events, q.events[:limit])
	return events
}

func WaitV2Events(uuid string, ackIDs []string, timeout time.Duration) []v2.Event {
	v2EventMu.Lock()
	q := getV2EventQueueLocked(uuid)
	ackV2EventsLocked(q, ackIDs)
	pruneExpiredV2EventsLocked(q)
	events := takeV2EventsLocked(q, v2EventQueueLimit)
	if len(events) > 0 || timeout <= 0 {
		v2EventMu.Unlock()
		return events
	}
	signal := q.signal
	v2EventMu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
	}
	return TakeV2Events(uuid, nil, v2EventQueueLimit)
}
