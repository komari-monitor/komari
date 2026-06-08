package report

import (
	"log"
	"strconv"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils"
	"github.com/patrickmn/go-cache"
)

var Records = cache.New(1*time.Minute, 1*time.Minute)

func SaveClientReportToDB() error {
	lastMinute := time.Now().Add(-time.Minute).Unix()
	var records []models.Record
	var gpuRecords []models.GPURecord

	for uuid, x := range Records.Items() {
		if uuid == "" {
			continue
		}

		reports, ok := x.Object.([]v1.Report)
		if !ok {
			log.Printf("Invalid report type for UUID %s", uuid)
			continue
		}

		var filtered []v1.Report
		for _, r := range reports {
			if r.UpdatedAt.Unix() >= lastMinute {
				filtered = append(filtered, r)
			}
		}

		Records.Set(uuid, filtered, cache.DefaultExpiration)

		if len(filtered) > 0 {
			r := utils.AverageReport(uuid, time.Now(), filtered, 0.3)
			records = append(records, r)
			gpuRecords = append(gpuRecords, utils.AverageGPUReports(uuid, time.Now(), filtered, 0.3)...)
		}
	}

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
