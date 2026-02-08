package notifier

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/ws"
	cache "github.com/patrickmn/go-cache"
)

// trafficCache 用于记录每个客户端已触发的阈值步进，避免重复提醒
// key: "traffic:"+clientUUID, value: int 步进百分比（例如 80, 85, 90 ... 100）
var trafficCache = cache.New(30*24*time.Hour, time.Hour) // 30天缓存，1小时清理

// CheckTraffic 检查各客户端流量使用情况，并在达到阈值和每+5%时提醒一次；100%时额外提醒一次
// 由外部协程每分钟调用一次
func CheckTraffic() {
	// 获取最新上报与客户端配置
	reports := ws.GetLatestReport()
	if len(reports) == 0 {
		return
	}
	cfg, err := config.GetAs[float64](config.TrafficLimitPercentageKey, 80.0)
	if err != nil {
		slog.Error("failed to get traffic limit percentage", "error", err)
	}

	if cfg <= 0 {
		return
	}

	// 起始阈值：例如 80%，非5的倍数则从上取整到最近的5的倍数，例如 83->85
	startThreshold := cfg
	if startThreshold < 0 {
		startThreshold = 0
	}
	baseStep := int(math.Ceil(startThreshold/5.0) * 5.0)
	if baseStep > 100 {
		baseStep = 100
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
		if !ok || r == nil {
			continue
		}

		// 计算不同类型的使用值
		used := computeUsedByType(strings.ToLower(c.TrafficLimitType), r.Network.TotalUp, r.Network.TotalDown)
		if used <= 0 {
			continue
		}

		pct := float64(used) / float64(c.TrafficLimit) * 100.0
		if pct < startThreshold {
			continue
		}

		// 当前所在阈值步进（5%的倍数）
		curStep := int(math.Floor(pct/5.0) * 5.0)
		if curStep < baseStep {
			curStep = baseStep
		}
		// if curStep > 100 {
		// 	curStep = 100
		// }

		key := "traffic:" + c.UUID
		last, _ := trafficCache.Get(key)
		lastStep, _ := last.(int)

		if curStep > lastStep { // 只在进入新步进时提醒一次
			trafficCache.SetDefault(key, curStep)

			msg := fmt.Sprintf("used %d%% (%s / %s), type=%s", curStep, humanBytes(used), humanBytes(c.TrafficLimit), strings.ToLower(c.TrafficLimitType))
			// 发送通知（内部会检查 NotificationEnabled）
			_ = messageSender.SendEvent(models.EventMessage{
				Event:   "Traffic",
				Clients: []models.Client{c},
				Time:    time.Now(),
				Emoji:   "⚠️",
				Message: msg,
			})
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
		if up < down {
			return up
		}
		return down
	case "max":
		fallthrough
	default:
		if up > down {
			return up
		}
		return down
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
	// KMGTPE
	prefixes := []string{"K", "M", "G", "T", "P", "E"}
	if exp >= len(prefixes) {
		exp = len(prefixes) - 1
	}
	return fmt.Sprintf("%.2f %sB", float64(b)/float64(div), prefixes[exp])
}
