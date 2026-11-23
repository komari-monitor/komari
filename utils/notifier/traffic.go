package notifier

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/trafficledger"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/utils/messageSender"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	cache "github.com/patrickmn/go-cache"
)

// trafficCache 按客户端和流量周期记录已触发的阈值步进，避免重复提醒。
var trafficCache = cache.New(30*24*time.Hour, time.Hour)

// CheckTraffic 检查各客户端流量使用情况，并在达到阈值和每 +5% 时提醒一次；
// 由外部调度器每分钟调用一次。
func CheckTraffic() {
	reports := agent_runtime.GetLatestReport()
	if len(reports) == 0 {
		return
	}
	startThreshold, err := config.GetAs[float64](config.TrafficLimitPercentageKey, 80.0)
	if err != nil {
		logger.Error("notifier", "failed to get traffic limit percentage", "error", err)
	}
	if startThreshold <= 0 {
		return
	}

	allClients, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return
	}

	for _, c := range allClients {
		if c.TrafficLimit <= 0 {
			continue
		}
		r, ok := reports[c.UUID]
		if !ok {
			continue
		}

		traffic := trafficledger.ResolveDisplay(c.UUID, r.Network.TotalUp, r.Network.TotalDown)
		used := computeUsedByType(strings.ToLower(c.TrafficLimitType), traffic.Up, traffic.Down)
		curStep, reached := trafficAlertStep(used, c.TrafficLimit, startThreshold)
		if !reached {
			continue
		}

		key := trafficAlertCacheKey(c.UUID, trafficCycleKey(traffic))
		last, _ := trafficCache.Get(key)
		lastStep, _ := last.(int)

		// 当检测到当前进度小于历史记录时，说明流量已重置，将基准归零
		if curStep < lastStep {
			lastStep = 0
		}

		if curStep > lastStep {
			trafficCache.Set(key, curStep, trafficAlertTTL(traffic.Mode, traffic.CycleEndAt))
			msg := fmt.Sprintf("used %d%% (%s / %s), type=%s", curStep, humanBytes(used), humanBytes(c.TrafficLimit), strings.ToLower(c.TrafficLimitType))
			_ = messageSender.SendNotification(models.EventMessage{
				Event:   messageevent.Traffic,
				Clients: []models.Client{c},
				Time:    time.Now().UTC(),
				Emoji:   "⚠️",
				Message: msg,
			})
		}
	}
}

// SyncTrafficAlertForClient records the client's current alert step so an
// explicit calibration with alert suppression does not trigger a notification.
func SyncTrafficAlertForClient(c models.Client, traffic trafficledger.DisplayTraffic) {
	if c.UUID == "" || c.TrafficLimit <= 0 {
		return
	}
	startThreshold, _ := config.GetAs[float64](config.TrafficLimitPercentageKey, 80.0)
	syncTrafficAlertForClientWithThreshold(c, traffic, startThreshold)
}

func syncTrafficAlertForClientWithThreshold(c models.Client, traffic trafficledger.DisplayTraffic, startThreshold float64) {
	if c.UUID == "" || c.TrafficLimit <= 0 || startThreshold <= 0 {
		return
	}
	used := computeUsedByType(strings.ToLower(c.TrafficLimitType), traffic.Up, traffic.Down)
	step, _ := trafficAlertStep(used, c.TrafficLimit, startThreshold)
	SyncTrafficAlertStep(c.UUID, trafficCycleKey(traffic), step, trafficAlertTTL(traffic.Mode, traffic.CycleEndAt))
}

func trafficAlertStep(used, limit int64, startThreshold float64) (step int, reached bool) {
	if used <= 0 || limit <= 0 || startThreshold <= 0 {
		return 0, false
	}
	pct := float64(used) / float64(limit) * 100.0
	if pct < startThreshold {
		return 0, false
	}
	baseStep := min(int(math.Ceil(startThreshold/5.0)*5.0), 100)
	return max(int(math.Floor(pct/5.0)*5.0), baseStep), true
}

func trafficCycleKey(traffic trafficledger.DisplayTraffic) string {
	if traffic.Mode == trafficledger.ModeAgent {
		return "agent"
	}
	if traffic.CycleStartAt != nil {
		return strconv.FormatInt(traffic.CycleStartAt.UTC().Unix(), 10)
	}
	return "current"
}

func trafficAlertCacheKey(uuid, cycleKey string) string {
	return "traffic:" + uuid + ":" + cycleKey
}

// SyncTrafficAlertStep marks a specific cycle threshold as already notified.
func SyncTrafficAlertStep(uuid, cycleKey string, step int, expiration ...time.Duration) {
	if uuid == "" || cycleKey == "" {
		return
	}
	ttl := cache.DefaultExpiration
	if len(expiration) > 0 {
		ttl = expiration[0]
	}
	trafficCache.Set(trafficAlertCacheKey(uuid, cycleKey), step, ttl)
}

func trafficAlertTTL(mode string, cycleEnd *time.Time) time.Duration {
	if mode == trafficledger.ModeAgent {
		return cache.DefaultExpiration
	}
	if cycleEnd == nil {
		return 0
	}
	ttl := time.Until(cycleEnd.UTC()) + time.Minute
	if ttl <= 0 {
		return time.Minute
	}
	return ttl
}

// ResetTrafficAlertState clears all cached threshold steps for one client.
func ResetTrafficAlertState(uuid string) {
	prefix := "traffic:" + uuid + ":"
	for key := range trafficCache.Items() {
		if strings.HasPrefix(key, prefix) {
			trafficCache.Delete(key)
		}
	}
}

func computeUsedByType(t string, up, down int64) int64 {
	switch t {
	case "up":
		return up
	case "down":
		return down
	case "sum":
		return up + down
	case "min":
		return min(up, down)
	default: // "max"
		return max(up, down)
	}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
