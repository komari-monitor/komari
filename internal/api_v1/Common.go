package api_v1

import (
	"log"
	"time"

	"github.com/komari-monitor/komari/internal/api_v1/vars"
	"github.com/komari-monitor/komari/internal/common"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/patrickmn/go-cache"

	"strconv"

	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/pkg/utils"
)

func SaveClientReportToDB() error {
	lastMinute := time.Now().Add(-time.Minute).Unix()
	var records []models.Record
	var gpuRecords []models.GPURecord

	// 遍历所有客户端记录
	for uuid, x := range vars.Records.Items() {
		if uuid == "" {
			continue
		}

		reports, ok := x.Object.([]common.Report)
		if !ok {
			log.Printf("Invalid report type for UUID %s", uuid)
			continue
		}

		// 过滤一分钟前的记录
		var filtered []common.Report
		for _, r := range reports {
			if r.UpdatedAt.Unix() >= lastMinute {
				filtered = append(filtered, r)
			}
		}

		// 更新缓存
		vars.Records.Set(uuid, filtered, cache.DefaultExpiration)

		// 计算平均报告并添加到记录列表
		if len(filtered) > 0 {
			r := utils.AverageReport(uuid, time.Now(), filtered, 0.3)
			records = append(records, r)

			// 使用与其他数据相同的聚合逻辑处理GPU数据
			gpuAggregated := utils.AverageGPUReports(uuid, time.Now(), filtered, 0.3)
			gpuRecords = append(gpuRecords, gpuAggregated...)
		}
	}

	// 批量插入数据库前去重（client与time共同构成唯一键）
	db := dbcore.GetDBInstance()

	if len(records) > 0 {
		unique := make(map[string]models.Record)
		for _, rec := range records {
			key := rec.Client + "_" + strconv.FormatInt(rec.Time.ToTime().Unix(), 10)
			unique[key] = rec
		}
		var deduped []models.Record
		for _, rec := range unique {
			deduped = append(deduped, rec)
		}
		if err := db.Model(&models.Record{}).Create(&deduped).Error; err != nil {
			log.Printf("Failed to save records to database: %v", err)
			return err
		}
	}

	// 批量插入GPU记录
	if len(gpuRecords) > 0 {
		// GPU记录也需要去重，防止重复插入
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

func isApiKeyValid(apiKey string) bool {
	cfg, err := conf.GetWithV1Format()
	if err != nil {
		return false
	}
	if cfg.ApiKey == "" || len(cfg.ApiKey) < 12 {
		return false
	}
	return apiKey == "Bearer "+cfg.ApiKey
}
