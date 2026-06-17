package notifier

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/corn"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/messageSender"
	"gorm.io/gorm"
)

// InitTrafficReportSchedule 注册三个定时任务：日报、周报、月报
func InitTrafficReportSchedule() {
	// 日报：每天凌晨 0 点
	if err := corn.AddFunc("traffic-report-daily", "0 0 0 * * *", func() {
		sendTrafficReport(true, false, false)
	}); err != nil {
		log.Println("Failed to register daily traffic report job:", err)
	}

	// 周报：每周一凌晨 0 点 (dow=1)
	if err := corn.AddFunc("traffic-report-weekly", "0 0 0 * * 1", func() {
		sendTrafficReport(false, true, false)
	}); err != nil {
		log.Println("Failed to register weekly traffic report job:", err)
	}

	// 月报：每月 1 日凌晨 0 点
	if err := corn.AddFunc("traffic-report-monthly", "0 0 0 1 * *", func() {
		sendTrafficReport(false, false, true)
	}); err != nil {
		log.Println("Failed to register monthly traffic report job:", err)
	}

	log.Println("Traffic report schedules registered: daily, weekly, monthly")
}

// sendTrafficReport 汇聚所有启用了指定报告类型的服务器流量，合并成一条通知发送
func sendTrafficReport(daily, weekly, monthly bool) {
	// 检查全局通知开关
	enabled, err := config.GetAs[bool](config.NotificationEnabledKey, false)
	if err != nil || !enabled {
		return
	}

	db := dbcore.GetDBInstance()
	now := time.Now()

	// 计算时间范围
	var start, end time.Time
	var eventType, label, suffix string

	switch {
	case daily:
		yesterday := now.AddDate(0, 0, -1)
		start = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, yesterday.Location())
		end = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 23, 59, 59, 0, yesterday.Location())
		eventType = messageevent.DReport
		label = "daily"
		suffix = "昨日流量"
	case weekly:
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		lastMonday := now.AddDate(0, 0, -(weekday-1)-7)
		lastSunday := lastMonday.AddDate(0, 0, 6)
		start = time.Date(lastMonday.Year(), lastMonday.Month(), lastMonday.Day(), 0, 0, 0, 0, lastMonday.Location())
		end = time.Date(lastSunday.Year(), lastSunday.Month(), lastSunday.Day(), 23, 59, 59, 0, lastSunday.Location())
		eventType = messageevent.WReport
		label = "weekly"
		suffix = "上周流量"
	case monthly:
		firstOfThisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		firstOfLastMonth := firstOfThisMonth.AddDate(0, -1, 0)
		lastDayOfLastMonth := firstOfThisMonth.Add(-time.Second)
		start = firstOfLastMonth
		end = lastDayOfLastMonth
		eventType = messageevent.MReport
		label = "monthly"
		suffix = "上个月流量"
	default:
		return
	}

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
		log.Printf("Failed to query traffic report notifications (%s): %v", label, err)
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
		log.Printf("Failed to query clients for traffic report (%s): %v", label, err)
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
			log.Printf("Failed to compute traffic for client %s (%s): %v", n.Client, label, err)
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
		log.Printf("Failed to send %s traffic report: %v", label, err)
	}
}

// getClientTrafficInRange 查询某客户端在指定时间段内的流量增量
// 通过累加持久化的精确流量增量字段计算用量
func getClientTrafficInRange(clientUUID string, trafficType string, start, end time.Time) (int64, error) {
	return getClientTrafficInRangeWithDB(dbcore.GetDBInstance(), clientUUID, trafficType, start, end)
}

type trafficDeltaRecord struct {
	Time         models.LocalTime `gorm:"column:time"`
	NetTotalUp   int64            `gorm:"column:net_total_up"`
	NetTotalDown int64            `gorm:"column:net_total_down"`
	TrafficUp    int64            `gorm:"column:traffic_up"`
	TrafficDown  int64            `gorm:"column:traffic_down"`
}

func getClientTrafficInRangeWithDB(db *gorm.DB, clientUUID string, trafficType string, start, end time.Time) (int64, error) {
	var recentRecords []trafficDeltaRecord
	if err := db.Table("records").
		Select("time, net_total_up, net_total_down, traffic_up, traffic_down").
		Where("client = ? AND time >= ? AND time <= ?", clientUUID, models.FromTime(start), models.FromTime(end)).
		Find(&recentRecords).Error; err != nil {
		return 0, err
	}

	var longTermRecords []trafficDeltaRecord
	if err := db.Table("records_long_term").
		Select("time, net_total_up, net_total_down, traffic_up, traffic_down").
		Where("client = ? AND time >= ? AND time <= ?", clientUUID, models.FromTime(start), models.FromTime(end)).
		Find(&longTermRecords).Error; err != nil {
		return 0, err
	}

	records := mergeTrafficRecords(recentRecords, longTermRecords)

	sort.Slice(records, func(i, j int) bool {
		return records[i].Time.ToTime().Before(records[j].Time.ToTime())
	})

	previous, err := getPreviousTrafficDeltaRecord(db, clientUUID, start)
	if err != nil {
		return 0, err
	}

	totalUp, totalDown := sumTrafficDeltas(records, previous)
	return computeUsedByType(strings.ToLower(trafficType), totalUp, totalDown), nil
}

func mergeTrafficRecords(recentRecords, longTermRecords []trafficDeltaRecord) []trafficDeltaRecord {
	rawSlots := make(map[time.Time]struct{}, len(recentRecords))
	for _, record := range recentRecords {
		rawSlots[record.Time.ToTime().Truncate(15*time.Minute)] = struct{}{}
	}

	longTermSlots := make(map[time.Time]struct{}, len(longTermRecords))
	records := make([]trafficDeltaRecord, 0, len(longTermRecords)+len(recentRecords))
	for _, record := range longTermRecords {
		slot := record.Time.ToTime().Truncate(15 * time.Minute)
		if _, hasRawSlot := rawSlots[slot]; hasRawSlot && record.TrafficUp == 0 && record.TrafficDown == 0 {
			continue
		}
		longTermSlots[slot] = struct{}{}
		records = append(records, record)
	}

	for _, record := range recentRecords {
		slot := record.Time.ToTime().Truncate(15 * time.Minute)
		if _, exists := longTermSlots[slot]; exists {
			continue
		}
		records = append(records, record)
	}

	return records
}

func getPreviousTrafficDeltaRecord(db *gorm.DB, clientUUID string, before time.Time) (*trafficDeltaRecord, error) {
	record, err := latestTrafficDeltaRecordBefore(db.Table("records"), clientUUID, before)
	if err != nil {
		return nil, err
	}

	longTermRecord, err := latestTrafficDeltaRecordBefore(db.Table("records_long_term"), clientUUID, before)
	if err != nil {
		return nil, err
	}

	if record == nil {
		return longTermRecord, nil
	}
	if longTermRecord == nil {
		return record, nil
	}
	if longTermRecord.Time.ToTime().After(record.Time.ToTime()) {
		return longTermRecord, nil
	}
	return record, nil
}

func latestTrafficDeltaRecordBefore(query *gorm.DB, clientUUID string, before time.Time) (*trafficDeltaRecord, error) {
	var record trafficDeltaRecord
	err := query.
		Select("time, net_total_up, net_total_down, traffic_up, traffic_down").
		Where("client = ? AND time < ?", clientUUID, models.FromTime(before)).
		Order("time DESC").
		First(&record).Error
	if err == nil {
		return &record, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return nil, err
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
	return utils.ComputeTrafficDelta(currentTotal, previousTotal)
}
