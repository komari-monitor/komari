package metricstore

import (
	"errors"
	"sync"
)

var ErrMetricWriteBlocked = errors.New("metric writes are blocked for a deleted target")

var deletionGuards = struct {
	sync.RWMutex
	entities  map[string]struct{}
	pingTasks map[uint]struct{}
}{
	entities:  make(map[string]struct{}),
	pingTasks: make(map[uint]struct{}),
}

func BlockEntityWrites(entityID string) {
	if entityID == "" {
		return
	}
	deletionGuards.Lock()
	deletionGuards.entities[entityID] = struct{}{}
	deletionGuards.Unlock()
}

func UnblockEntityWrites(entityID string) {
	deletionGuards.Lock()
	delete(deletionGuards.entities, entityID)
	deletionGuards.Unlock()
}

func EntityWritesBlocked(entityID string) bool {
	deletionGuards.RLock()
	_, blocked := deletionGuards.entities[entityID]
	deletionGuards.RUnlock()
	return blocked
}

func BlockPingTaskWrites(taskIDs []uint) {
	deletionGuards.Lock()
	for _, taskID := range taskIDs {
		deletionGuards.pingTasks[taskID] = struct{}{}
	}
	deletionGuards.Unlock()
}

func UnblockPingTaskWrites(taskIDs []uint) {
	deletionGuards.Lock()
	for _, taskID := range taskIDs {
		delete(deletionGuards.pingTasks, taskID)
	}
	deletionGuards.Unlock()
}

func PingTaskWritesBlocked(taskID uint) bool {
	deletionGuards.RLock()
	_, blocked := deletionGuards.pingTasks[taskID]
	deletionGuards.RUnlock()
	return blocked
}
