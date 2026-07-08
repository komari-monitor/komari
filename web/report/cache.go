package report

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils"
	"github.com/patrickmn/go-cache"
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

// SaveClientReportToDB 把缓存中的报告聚合后写入 metric store。
//
// 历史监控数据已完全迁移到 metric store（默认 SQLite ./data/metrics.db，或配置的
// MySQL/PostgreSQL），运行期的读写全部走 metric store，不再写旧 records 表。
func SaveClientReportToDB() error {
	return saveClientReportToDB(time.Now())
}

func saveClientReportToDB(now time.Time) error {
	saveClientReportMu.Lock()
	defer saveClientReportMu.Unlock()

	lastMinute := now.Add(-time.Minute).Unix()
	var records []models.Record
	var gpuRecords []models.GPURecord
	trafficByRecord := make(map[string]cachedTrafficSummary)

	reportCacheMu.Lock()
	// 先收集所有需要保存的数据，但不修改缓存
	filteredByUUID := make(map[string][]v1.Report)
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

			filteredByUUID[uuid] = filtered

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
		// 增量基线来自 metric store，记录写入 metric store。
		if err := fillTrafficDeltasFromMetricStore(deduped, dedupedTraffic); err != nil {
			log.Printf("Failed to fill traffic deltas from metric store: %v", err)
			return err
		}
		ctx := context.Background()
		for i := range deduped {
			if err := metricstore.WriteRecord(ctx, deduped[i]); err != nil {
				log.Printf("Failed to write record to metric store: %v", err)
				return err
			}
		}
	}

	if len(gpuRecords) > 0 {
		gpuUnique := make(map[string]models.GPURecord)
		for _, rec := range gpuRecords {
			key := rec.Client + "_" + strconv.Itoa(rec.DeviceIndex) + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
			gpuUnique[key] = rec
		}
		ctx := context.Background()
		for _, rec := range gpuUnique {
			if err := metricstore.WriteGPURecord(ctx, rec); err != nil {
				log.Printf("Failed to write GPU record to metric store: %v", err)
				return err
			}
		}
	}

	// 数据成功写入后，才清理缓存中已处理的旧数据。
	// 这里重新从当前缓存读取并按时间过滤，保留最近一分钟内的报告
	// （包括写库期间新到达的报告），避免写库失败时丢失尚未持久化的历史数据。
	reportCacheMu.Lock()
	for uuid := range filteredByUUID {
		cached, ok := Records.Get(uuid)
		if !ok || cached == nil {
			continue
		}
		reports, ok := cached.([]v1.Report)
		if !ok {
			continue
		}
		var remaining []v1.Report
		for _, r := range reports {
			if r.UpdatedAt.Unix() >= lastMinute {
				remaining = append(remaining, r)
			}
		}
		Records.Set(uuid, remaining, cache.DefaultExpiration)
	}
	reportCacheMu.Unlock()

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
	Client       string
	Time         models.LocalTime
	NetTotalUp   int64
	NetTotalDown int64
}

// fillTrafficDeltasFromMetricStore 计算流量增量，基线（上一周期累计流量）从
// metric store 查询。
func fillTrafficDeltasFromMetricStore(records []models.Record, trafficByRecord map[string]cachedTrafficSummary) error {
	ctx := context.Background()
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

		baseline, err := metricstore.GetLatestTrafficBefore(ctx, clientUUIDs, before)
		if err != nil {
			return fmt.Errorf("load previous traffic from metric store before %s: %w", before.Format(time.RFC3339), err)
		}

		for _, index := range indexes {
			var prev *previousTrafficRecord
			if base, ok := baseline[records[index].Client]; ok {
				prev = &previousTrafficRecord{
					Client:       base.Client,
					Time:         base.Time,
					NetTotalUp:   base.NetTotalUp,
					NetTotalDown: base.NetTotalDown,
				}
			}

			key := recordDedupKey(records[index])
			if summary, ok := trafficByRecord[key]; ok && len(summary.Points) > 0 {
				records[index].TrafficUp, records[index].TrafficDown = sumCachedTrafficDeltas(summary, prev)
				continue
			}

			if prev == nil {
				continue
			}
			records[index].TrafficUp = utils.ComputeTrafficDelta(records[index].NetTotalUp, prev.NetTotalUp)
			records[index].TrafficDown = utils.ComputeTrafficDelta(records[index].NetTotalDown, prev.NetTotalDown)
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
