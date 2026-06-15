package report

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils"
	"github.com/patrickmn/go-cache"
	"gorm.io/gorm"
)

var Records = cache.New(1*time.Minute, 1*time.Minute)
var reportCacheMu sync.Mutex
var saveClientReportMu sync.Mutex

func AppendClientReport(uuid string, report v1.Report) (v1.Report, error) {
	reportCacheMu.Lock()
	defer reportCacheMu.Unlock()

	reports, ok := cachedReports(uuid)
	if !ok {
		return v1.Report{}, fmt.Errorf("invalid report type for UUID %s", uuid)
	}
	report.UUID = uuid
	report.UpdatedAt = time.Now()
	reports = append(reports, report)
	Records.Set(uuid, reports, cache.DefaultExpiration)
	return report, nil
}

func SaveClientReportToDB() error {
	return saveClientReportToDB(dbcore.GetDBInstance(), time.Now())
}

func saveClientReportToDB(db *gorm.DB, now time.Time) error {
	saveClientReportMu.Lock()
	defer saveClientReportMu.Unlock()

	lastMinute := now.Add(-time.Minute).Unix()
	var records []models.Record
	var gpuRecords []models.GPURecord
	trafficByRecord := make(map[string]cachedTrafficSummary)

	reportCacheMu.Lock()
	for uuid, x := range Records.Items() {
		func() {
			if uuid == "" {
				return
			}

			reports, ok := x.Object.([]v1.Report)
			if !ok {
				log.Printf("Invalid report type for UUID %s", uuid)
				return
			}

			var filtered []v1.Report
			for _, r := range reports {
				if r.UpdatedAt.Unix() >= lastMinute {
					if err := clients.ReportVerify(r); err != nil {
						log.Printf("Invalid report data for UUID %s: %v", uuid, err)
						continue
					}
					filtered = append(filtered, r)
				}
			}

			Records.Set(uuid, filtered, cache.DefaultExpiration)

			if len(filtered) > 0 {
				r := utils.AverageReport(uuid, now, filtered, 0.3)
				key := recordDedupKey(r)
				trafficByRecord[key] = summarizeCachedTraffic(filtered)
				records = append(records, r)
				gpuRecords = append(gpuRecords, utils.AverageGPUReports(uuid, now, filtered, 0.3)...)
			}
		}()
	}
	reportCacheMu.Unlock()

	if len(records) > 0 {
		unique := make(map[string]models.Record)
		for _, rec := range records {
			unique[recordDedupKey(rec)] = rec
		}
		var deduped []models.Record
		dedupedTraffic := make(map[string]cachedTrafficSummary, len(unique))
		for key, rec := range unique {
			deduped = append(deduped, rec)
			if summary, ok := trafficByRecord[key]; ok {
				dedupedTraffic[key] = summary
			}
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := fillTrafficDeltas(tx, deduped, dedupedTraffic); err != nil {
				return err
			}
			if err := tx.Model(&models.Record{}).Create(&deduped).Error; err != nil {
				return err
			}
			return nil
		}); err != nil {
			log.Printf("Failed to save records to database: %v", err)
			return err
		}
	}

	if len(gpuRecords) > 0 {
		gpuUnique := make(map[string]models.GPURecord)
		for _, rec := range gpuRecords {
			key := rec.Client + "_" + strconv.Itoa(rec.DeviceIndex) + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
			gpuUnique[key] = rec
		}
		var gpuDeduped []models.GPURecord
		for _, rec := range gpuUnique {
			gpuDeduped = append(gpuDeduped, rec)
		}
		if err := db.Model(&models.GPURecord{}).Create(&gpuDeduped).Error; err != nil {
			log.Printf("Failed to save GPU records to database: %v", err)
			return err
		}
	}

	return nil
}

func cachedReports(uuid string) ([]v1.Report, bool) {
	cached, ok := Records.Get(uuid)
	if !ok || cached == nil {
		return []v1.Report{}, true
	}
	reports, ok := cached.([]v1.Report)
	return reports, ok
}

func recordDedupKey(rec models.Record) string {
	return rec.Client + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
}

type trafficTotalPoint struct {
	Time      time.Time
	TotalUp   int64
	TotalDown int64
}

type cachedTrafficSummary struct {
	Points []trafficTotalPoint
}

func summarizeCachedTraffic(reports []v1.Report) cachedTrafficSummary {
	points := make([]trafficTotalPoint, 0, len(reports))
	for _, report := range reports {
		points = append(points, trafficTotalPoint{
			Time:      report.UpdatedAt,
			TotalUp:   report.Network.TotalUp,
			TotalDown: report.Network.TotalDown,
		})
	}
	sort.SliceStable(points, func(i, j int) bool {
		return points[i].Time.Before(points[j].Time)
	})
	return cachedTrafficSummary{Points: points}
}

type previousTrafficRecord struct {
	Client       string           `gorm:"column:client"`
	Time         models.LocalTime `gorm:"column:time"`
	NetTotalUp   int64            `gorm:"column:net_total_up"`
	NetTotalDown int64            `gorm:"column:net_total_down"`
}

func fillTrafficDeltas(db *gorm.DB, records []models.Record, trafficByRecord map[string]cachedTrafficSummary) error {
	recordsByTime := make(map[time.Time][]int)
	for i := range records {
		before := records[i].Time.ToTime().Round(0)
		recordsByTime[before] = append(recordsByTime[before], i)
	}

	for before, indexes := range recordsByTime {
		clientUUIDs := make([]string, 0, len(indexes))
		seen := make(map[string]struct{}, len(indexes))
		for _, index := range indexes {
			clientUUID := records[index].Client
			if clientUUID == "" {
				continue
			}
			if _, exists := seen[clientUUID]; exists {
				continue
			}
			seen[clientUUID] = struct{}{}
			clientUUIDs = append(clientUUIDs, clientUUID)
		}

		previousByClient, err := getLatestTrafficRecordsBefore(db, clientUUIDs, before)
		if err != nil {
			return fmt.Errorf("load previous traffic records before %s: %w", before.Format(time.RFC3339), err)
		}

		for _, index := range indexes {
			key := recordDedupKey(records[index])
			if summary, ok := trafficByRecord[key]; ok && len(summary.Points) > 0 {
				if previous, exists := previousByClient[records[index].Client]; exists {
					records[index].TrafficUp, records[index].TrafficDown = sumCachedTrafficDeltas(summary, &previous)
				} else {
					records[index].TrafficUp, records[index].TrafficDown = sumCachedTrafficDeltas(summary, nil)
				}
				continue
			}

			previous, exists := previousByClient[records[index].Client]
			if !exists {
				continue
			}
			records[index].TrafficUp = utils.ComputeTrafficDelta(records[index].NetTotalUp, previous.NetTotalUp)
			records[index].TrafficDown = utils.ComputeTrafficDelta(records[index].NetTotalDown, previous.NetTotalDown)
		}
	}

	return nil
}

func sumCachedTrafficDeltas(summary cachedTrafficSummary, previous *previousTrafficRecord) (int64, int64) {
	if len(summary.Points) == 0 {
		return 0, 0
	}

	startIndex := 0
	var previousUp int64
	var previousDown int64
	var previousTime time.Time
	if previous != nil {
		previousUp = previous.NetTotalUp
		previousDown = previous.NetTotalDown
		previousTime = previous.Time.ToTime()
	} else {
		previousUp = summary.Points[0].TotalUp
		previousDown = summary.Points[0].TotalDown
		previousTime = summary.Points[0].Time
		startIndex = 1
	}

	var totalUp int64
	var totalDown int64
	for _, point := range summary.Points[startIndex:] {
		if !point.Time.After(previousTime) {
			continue
		}
		totalUp += utils.ComputeTrafficDelta(point.TotalUp, previousUp)
		totalDown += utils.ComputeTrafficDelta(point.TotalDown, previousDown)
		previousUp = point.TotalUp
		previousDown = point.TotalDown
		previousTime = point.Time
	}
	return totalUp, totalDown
}

func getLatestTrafficRecordsBefore(db *gorm.DB, clientUUIDs []string, before time.Time) (map[string]previousTrafficRecord, error) {
	previousByClient := make(map[string]previousTrafficRecord, len(clientUUIDs))
	if len(clientUUIDs) == 0 {
		return previousByClient, nil
	}

	for _, table := range []string{"records", "records_long_term"} {
		records, err := latestTrafficRecordsBeforeFromTable(db, table, clientUUIDs, before)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			previous, exists := previousByClient[record.Client]
			if !exists || record.Time.ToTime().After(previous.Time.ToTime()) {
				previousByClient[record.Client] = record
			}
		}
	}
	return previousByClient, nil
}

func latestTrafficRecordsBeforeFromTable(db *gorm.DB, table string, clientUUIDs []string, before time.Time) ([]previousTrafficRecord, error) {
	var records []previousTrafficRecord
	latestPerClient := db.Table(table).
		Select("client, MAX(time) AS time").
		Where("client IN ? AND time < ?", clientUUIDs, models.FromTime(before)).
		Group("client")

	err := db.Table(table+" AS r").
		Select("r.client, r.time, r.net_total_up, r.net_total_down").
		Joins("JOIN (?) AS latest ON latest.client = r.client AND latest.time = r.time", latestPerClient).
		Find(&records).Error
	return records, err
}
