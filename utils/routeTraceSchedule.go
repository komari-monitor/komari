package utils

import (
	"encoding/json"
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

func RouteFamiliesForClient(clientUUID string) []string {
	raw, err := config.GetAs[string](config.RouteTraceFamiliesKey, "")
	if err == nil && raw != "" {
		var choices map[string]string
		if json.Unmarshal([]byte(raw), &choices) == nil {
			switch choices[clientUUID] {
			case "ipv4":
				return []string{"ipv4"}
			case "ipv6":
				return []string{"ipv6"}
			case "both":
				return []string{"ipv4", "ipv6"}
			}
		}
	}
	return []string{"ipv4", "ipv6"} // Trace each enabled measurement family by default.
}

func dispatchRouteTrace(clientUUID string, task models.PingTask, targetOverride string, interval time.Duration, force bool) int {
	if task.Type != "tcp" || !task.Active() || !task.AppliesToClient(clientUUID) || !agent_runtime.IsAgentOnline(clientUUID) {
		return 0
	}
	family := task.Family
	if family == "" {
		family = "ipv4"
	}
	selected := false
	for _, candidate := range RouteFamiliesForClient(clientUUID) {
		selected = selected || candidate == family
	}
	if !selected {
		return 0
	}
	target := task.Target
	if targetOverride != "" {
		target = targetOverride
	}
	key := fmt.Sprintf("%s:%d:%s:%s", clientUUID, task.Id, target, family)
	routeTraceSchedule.Lock()
	last := routeTraceSchedule.last[key]
	if !force && time.Since(last) < interval {
		routeTraceSchedule.Unlock()
		return 0
	}
	routeTraceSchedule.last[key] = time.Now()
	routeTraceSchedule.Unlock()
	if agent_runtime.DispatchV2Event(clientUUID, v2.MethodAgentRouteTrace, v2.PingParams{
		TaskID: task.Id, Type: task.Type, Target: target, Family: family,
	}) {
		return 1
	}
	routeTraceSchedule.Lock()
	delete(routeTraceSchedule.last, key)
	routeTraceSchedule.Unlock()
	return 0
}

// TriggerRouteTraceForClient dispatches every TCP measurement point assigned
// to the selected server. The task ID keeps result labels tied to task names.
func TriggerRouteTraceForClient(clientUUID string, tasks []models.PingTask) int {
	target, interval := routeTraceSettings()
	count := 0
	for _, task := range tasks {
		count += dispatchRouteTrace(clientUUID, task, target, interval, true)
	}
	return count
}
