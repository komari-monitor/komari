package records

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/komari-monitor/komari/internal/database/models"
)

var uuid = "7901508c-304f-49aa-b84f-957c33ae6f8a"

var _ = func() bool {
	// 确保 Test 环境中使用 sqlite 内存数据库
	return true
}()

// TestCompactRecord tests the database compaction logic by inserting 4h30m of data (one record per minute),
// then running migrateOldRecords and verifying the aggregation and cleanup.
func TestCompactRecord(t *testing.T) {
	const totalMinutes = 12*60 + 30
	now := time.Now()
	threshold := now.Add(-4 * time.Hour)

	// 使用 sqlite 内存数据库并迁移表结构
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	expectedGroups := make(map[time.Time]struct{})
	expectedRemain := 0

	// 插入数据
	for i := 0; i < totalMinutes; i++ {
		recTime := now.Add(-time.Duration(i) * time.Minute)
		rec := models.Record{Client: uuid, Time: models.FromTime(recTime), Cpu: float32(i), Gpu: float32(i), Load: float32(i), Temp: float32(i), Ram: int64(i)}
		err := db.Create(&rec).Error
		assert.NoError(t, err)

		if recTime.Before(threshold) {
			slot := recTime.Truncate(time.Hour)
			expectedGroups[slot] = struct{}{}
		} else {
			expectedRemain++
		}
	}

	// 导出原始数据到 CSV
	os.MkdirAll("../../data", 0755)
	var origRecs []models.Record
	db.Order("time desc").Find(&origRecs)
	fOrig, err := os.Create("../../data/original.csv")
	assert.NoError(t, err)
	defer fOrig.Close()
	wOrig := csv.NewWriter(fOrig)
	defer wOrig.Flush()
	wOrig.Write([]string{"Client", "Time", "Cpu", "Gpu", "Load", "Temp", "Ram"})
	for _, r := range origRecs {
		wOrig.Write([]string{
			r.Client,
			r.Time.ToTime().Format(time.RFC3339),
			strconv.FormatFloat(float64(r.Cpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Gpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Load), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Temp), 'f', -1, 32),
			strconv.FormatInt(r.Ram, 10),
		})
	}

	// 运行压缩（迁移）逻辑
	err = migrateOldRecords(db)
	assert.NoError(t, err)

	// 验证 long-term 表中的聚合记录数
	var longCount int64
	assert.NoError(t, db.Table("records_long_term").Count(&longCount).Error)
	assert.Equal(t, int64(len(expectedGroups)), longCount)

	// 验证原始表中剩余记录数
	var remainCount int64
	assert.NoError(t, db.Table("records").Count(&remainCount).Error)
	assert.Equal(t, int64(expectedRemain), remainCount+1)

	// 导出压缩后的数据到 CSV
	var compRecs []models.Record
	db.Table("records_long_term").Order("time desc").Find(&compRecs)
	fComp, err := os.Create("../../data/compressed.csv")
	assert.NoError(t, err)
	defer fComp.Close()
	wComp := csv.NewWriter(fComp)
	defer wComp.Flush()
	wComp.Write([]string{"Client", "Time", "Cpu", "Gpu", "Load", "Temp", "Ram"})
	for _, r := range compRecs {
		wComp.Write([]string{
			r.Client,
			r.Time.ToTime().Format(time.RFC3339),
			strconv.FormatFloat(float64(r.Cpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Gpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Load), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Temp), 'f', -1, 32),
			strconv.FormatInt(r.Ram, 10),
		})
	}

	db.Table("records").Order("time desc").Find(&compRecs)
	fComp, err = os.Create("../../data/compressed_records.csv")
	assert.NoError(t, err)
	defer fComp.Close()
	wComp = csv.NewWriter(fComp)
	defer wComp.Flush()
	wComp.Write([]string{"Client", "Time", "Cpu", "Gpu", "Load", "Temp", "Ram"})
	for _, r := range compRecs {
		wComp.Write([]string{
			r.Client,
			r.Time.ToTime().Format(time.RFC3339),
			strconv.FormatFloat(float64(r.Cpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Gpu), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Load), 'f', -1, 32),
			strconv.FormatFloat(float64(r.Temp), 'f', -1, 32),
			strconv.FormatInt(r.Ram, 10),
		})
	}
}
