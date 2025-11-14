package config

import (
	"sync"

	"github.com/komari-monitor/komari/internal/database/models"
)

// ConfigEvent represents a configuration change event
type ConfigEvent struct {
	Old models.Config
	New models.Config
}

// ConfigSubscriber handles config events
type ConfigSubscriber func(event ConfigEvent)

var (
	subscribersMu sync.RWMutex
	subscribers   []ConfigSubscriber
)

// Subscribe registers a subscriber for all config events.
func Subscribe(subscriber ConfigSubscriber) {
	subscribersMu.Lock()
	defer subscribersMu.Unlock()
	subscribers = append(subscribers, subscriber)
}

// publishEvent notifies all subscribers of a config change.
func publishEvent(oldVal, newVal models.Config) {
	subscribersMu.RLock()
	defer subscribersMu.RUnlock()
	event := ConfigEvent{Old: oldVal, New: newVal}
	for _, sub := range subscribers {
		go sub(event)
	}
}
