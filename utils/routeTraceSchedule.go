package utils

import (
	"fmt"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

var routeTraceSchedule = struct {
	sync.Mutex
	last map[string]time.Time
}{last: make(map[string]time.Time)}

func routeTraceSettings() (string, time.Duration) {
	target, err := config.GetAs[string](config.RouteTraceTargetKey, "")
	if err != nil {
		target = ""
	}
	hours, err := config.GetAs[int](config.RouteTraceIntervalHoursKey, 6)
	if err != nil || hours < 1 || hours > 168 {
		hours = 6
	}
	return target, time.Duration(hours) * time.Hour
}

func dispatchRouteTrace(clientUUID string, task models.PingTask, targetOverride string, interval time.Duration, force bool) bool {
	if task.Type != "tcp" || !task.AppliesToClient(clientUUID) || !agent_runtime.IsAgentOnline(clientUUID) {
		return false
	}
	target := task.Target
	if targetOverride != "" {
		target = targetOverride
	}
	key := fmt.Sprintf("%s:%d:%s", clientUUID, task.Id, target)
	routeTraceSchedule.Lock()
	last := routeTraceSchedule.last[key]
	if !force && time.Since(last) < interval {
		routeTraceSchedule.Unlock()
		return false
	}
	routeTraceSchedule.last[key] = time.Now()
	routeTraceSchedule.Unlock()
	if agent_runtime.DispatchV2Event(clientUUID, v2.MethodAgentRouteTrace, v2.PingParams{
		TaskID: task.Id, Type: task.Type, Target: target,
	}) {
		return true
	}
	routeTraceSchedule.Lock()
	delete(routeTraceSchedule.last, key)
	routeTraceSchedule.Unlock()
	return false
}

// TriggerRouteTraceForClient dispatches every TCP measurement point assigned
// to the selected server. The task ID keeps result labels tied to task names.
func TriggerRouteTraceForClient(clientUUID string, tasks []models.PingTask) int {
	target, interval := routeTraceSettings()
	count := 0
	for _, task := range tasks {
		if dispatchRouteTrace(clientUUID, task, target, interval, true) {
			count++
		}
	}
	return count
}
