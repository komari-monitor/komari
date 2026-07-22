package notifier

import (
	"fmt"
	"sort"
	"strings"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

	"context"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/scheduler"
	"github.com/komari-monitor/komari/utils/messageSender"
)

// InitTrafficReportSchedule 注册三个定时任务：日报、周报、月报
func InitTrafficReportSchedule() {
	// 日报：每天凌晨 0 点
	if err := scheduler.AddFunc("traffic-report-daily", "0 0 0 * * *", func() {
		sendTrafficReport(true, false, false)
	}); err != nil {
		logger.ErrorArgs("notifier", "Failed to register daily traffic report job:", err)
	}

	// 周报：每周一凌晨 0 点 (dow=1)
	if err := scheduler.AddFunc("traffic-report-weekly", "0 0 0 * * 1", func() {
		sendTrafficReport(false, true, false)
	}); err != nil {
		logger.ErrorArgs("notifier", "Failed to register weekly traffic report job:", err)
	}

	// 月报：每月 1 日凌晨 0 点
	if err := scheduler.AddFunc("traffic-report-monthly", "0 0 0 1 * *", func() {
		sendTrafficReport(false, false, true)
	}); err != nil {
		logger.ErrorArgs("notifier", "Failed to register monthly traffic report job:", err)
	}
}

// sendTrafficReport 汇聚所有启用了指定报告类型的服务器流量，合并成一条通知发送
func sendTrafficReport(daily, weekly, monthly bool) {
	// 检查全局通知开关
	enabled, err := config.GetAs[bool](config.NotificationEnabledKey, false)
	if err != nil || !enabled {
		return
	}

	db := dbcore.GetDBInstance()
	now := time.Now().UTC()

	var eventType, label, suffix string

	switch {
	case daily:
		eventType = messageevent.DReport
		label = "daily"
		suffix = "昨日流量"
	case weekly:
		eventType = messageevent.WReport
		label = "weekly"
		suffix = "上周流量"
	case monthly:
		eventType = messageevent.MReport
		label = "monthly"
		suffix = "上个月流量"
	default:
		return
	}
	start, end := previousTrafficReportRange(now, label)

	// 查询所有启用该类型报告的服务器配置
	var notifications []models.TrafficReportNotification
	query := db.Model(&models.TrafficReportNotification{}).Where("enable = ?", true)
	if daily {
		query = query.Where("daily = ?", true)
	} else if weekly {
		query = query.Where("weekly = ?", true)
	} else if monthly {
		query = query.Where("monthly = ?", true)
	}
	if err := query.Find(&notifications).Error; err != nil {
		logger.Errorf("notifier", "Failed to query traffic report notifications (%s): %v", label, err)
		return
	}
	if len(notifications) == 0 {
		return
	}

	// 获取客户端信息
	clientUUIDs := make([]string, 0, len(notifications))
	for _, n := range notifications {
		clientUUIDs = append(clientUUIDs, n.Client)
	}
	var clientList []models.Client
	if err := db.Where("uuid IN ?", clientUUIDs).Find(&clientList).Error; err != nil {
		logger.Errorf("notifier", "Failed to query clients for traffic report (%s): %v", label, err)
		return
	}
	clientMap := make(map[string]models.Client, len(clientList))
	for _, c := range clientList {
		clientMap[c.UUID] = c
	}

	// 为每个服务器统计流量并拼接消息
	var lines []string
	eventClients := make([]models.Client, 0, len(notifications))
	for _, n := range notifications {
		c, ok := clientMap[n.Client]
		if !ok {
			continue
		}

		used, err := getClientTrafficInRange(n.Client, c.TrafficLimitType, start, end)
		if err != nil {
			logger.Errorf("notifier", "Failed to compute traffic for client %s (%s): %v", n.Client, label, err)
			continue
		}

		lines = append(lines, fmt.Sprintf("%s%s：%s", c.Name, suffix, humanBytes(used)))
		eventClients = append(eventClients, c)
	}

	if len(lines) == 0 {
		return
	}

	message := strings.Join(lines, "\n")
	var emoji string
	switch {
	case daily:
		emoji = "📊"
	case weekly:
		emoji = "📈"
	case monthly:
		emoji = "📅"
	}

	if err := messageSender.SendEvent(models.EventMessage{
		Event:   eventType,
		Clients: eventClients,
		Time:    now,
		Emoji:   emoji,
		Message: message,
	}); err != nil {
		logger.Errorf("notifier", "Failed to send %s traffic report: %v", label, err)
	}
}

func previousTrafficReportRange(now time.Time, period string) (time.Time, time.Time) {
	localNow := now.In(time.Local)
	var startLocal, endLocal time.Time

	switch period {
	case "daily":
		yesterday := localNow.AddDate(0, 0, -1)
		startLocal = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.Local)
		endLocal = startLocal.AddDate(0, 0, 1)
	case "weekly":
		weekday := int(localNow.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		lastMonday := localNow.AddDate(0, 0, -(weekday-1)-7)
		startLocal = time.Date(lastMonday.Year(), lastMonday.Month(), lastMonday.Day(), 0, 0, 0, 0, time.Local)
		endLocal = startLocal.AddDate(0, 0, 7)
	case "monthly":
		endLocal = time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, time.Local)
		startLocal = endLocal.AddDate(0, -1, 0)
	default:
		return time.Time{}, time.Time{}
	}

	return startLocal.UTC(), endLocal.Add(-time.Nanosecond).UTC()
}

// getClientTrafficInRange 查询某客户端在指定时间段内的流量增量。
//
// 历史监控数据已完全迁移到 metric store，这里从 metric store 读取区间内记录并
// 累加精确的流量增量字段计算用量；缺失增量时回退到累计流量差值。
func getClientTrafficInRange(clientUUID string, trafficType string, start, end time.Time) (int64, error) {
	ctx := context.Background()
	recs, err := metricstore.GetRecordsByClientAndTime(ctx, clientUUID, start, end)
	if err != nil {
		return 0, err
	}

	records := make([]trafficDeltaRecord, 0, len(recs))
	for _, r := range recs {
		records = append(records, trafficDeltaRecord{
			Time:         r.Time,
			NetTotalUp:   r.NetTotalUp,
			NetTotalDown: r.NetTotalDown,
			TrafficUp:    r.TrafficUp,
			TrafficDown:  r.TrafficDown,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Time.Before(records[j].Time)
	})

	// 计算增量基线（区间开始前最后一条累计流量）
	var previous *trafficDeltaRecord
	baseline, err := metricstore.GetLatestTrafficBefore(ctx, []string{clientUUID}, start)
	if err != nil {
		return 0, err
	}
	if base, ok := baseline[clientUUID]; ok {
		previous = &trafficDeltaRecord{
			Time:         base.Time,
			NetTotalUp:   base.NetTotalUp,
			NetTotalDown: base.NetTotalDown,
		}
	}

	totalUp, totalDown := sumTrafficDeltas(records, previous)
	return computeUsedByType(strings.ToLower(trafficType), totalUp, totalDown), nil
}

type trafficDeltaRecord struct {
	Time         time.Time
	NetTotalUp   int64
	NetTotalDown int64
	TrafficUp    int64
	TrafficDown  int64
}

func sumTrafficDeltas(records []trafficDeltaRecord, previous *trafficDeltaRecord) (int64, int64) {
	var totalUp int64
	var totalDown int64

	for i := range records {
		up := records[i].TrafficUp
		down := records[i].TrafficDown

		if previous != nil {
			up = trafficDeltaOrFallback(up, records[i].NetTotalUp, previous.NetTotalUp)
			down = trafficDeltaOrFallback(down, records[i].NetTotalDown, previous.NetTotalDown)
		}
		totalUp += up
		totalDown += down
		previous = &records[i]
	}

	return totalUp, totalDown
}

func trafficDeltaOrFallback(storedDelta, currentTotal, previousTotal int64) int64 {
	if storedDelta > 0 {
		return storedDelta
	}
	return metricstore.TrafficCounterDelta(currentTotal, previousTotal)
}
