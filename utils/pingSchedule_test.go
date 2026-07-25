package utils

import (
	"testing"

	"github.com/komari-monitor/komari/database/models"
)

func TestIsPingTaskAssignedUsesCurrentSchedule(t *testing.T) {
	manager.mu.Lock()
	previous := manager.tasks
	manager.tasks = map[int][]models.PingTask{
		5: {{Id: 7, Clients: models.StringArray{"client-a"}, Interval: 5}},
	}
	manager.mu.Unlock()
	t.Cleanup(func() {
		manager.mu.Lock()
		manager.tasks = previous
		manager.mu.Unlock()
	})

	if !IsPingTaskAssigned(7, "client-a") {
		t.Fatal("assigned ping task was rejected")
	}
	if IsPingTaskAssigned(7, "client-b") || IsPingTaskAssigned(99, "client-a") {
		t.Fatal("unassigned ping task was accepted")
	}
}
