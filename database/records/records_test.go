package records

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/komari-monitor/komari/database/models"
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
	loc := models.GetAppLocation()
	now := time.Date(2026, 6, 15, 14, 30, 0, 0, loc)
	threshold := compactRecordCutoff(now)
	overlapCutoff := threshold.Add(-1 * time.Hour)

	// 使用 sqlite 内存数据库并迁移表结构
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	expectedGroups := make(map[time.Time]struct{})
	expectedRawRemain := 0

	// 插入数据
	for i := 0; i < totalMinutes; i++ {
		recTime := now.Add(-time.Duration(i) * time.Minute)
		rec := models.Record{Client: uuid, Time: models.FromTime(recTime), Cpu: float32(i), Gpu: float32(i), Load: float32(i), Temp: float32(i), Ram: int64(i)}
		err := db.Create(&rec).Error
		assert.NoError(t, err)

		if recTime.Before(threshold) {
			slot := recTime.Truncate(15 * time.Minute)
			expectedGroups[slot] = struct{}{}
		}
		if !recTime.Before(overlapCutoff) {
			expectedRawRemain++
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
	err = migrateOldRecordsAt(db, now)
	assert.NoError(t, err)

	// 验证 long-term 表中的聚合记录数
	var longCount int64
	assert.NoError(t, db.Table("records_long_term").Count(&longCount).Error)
	assert.Equal(t, int64(len(expectedGroups)), longCount)

	// 验证原始表中剩余记录数
	var remainCount int64
	assert.NoError(t, db.Table("records").Count(&remainCount).Error)
	assert.Equal(t, int64(expectedRawRemain), remainCount)

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

func TestCompactRecordPreservesExactTrafficDelta(t *testing.T) {
	loc := models.GetAppLocation()
	currentTime := time.Date(2026, 6, 15, 14, 30, 0, 0, loc)
	now := currentTime.Truncate(15 * time.Minute).Add(-5*time.Hour + time.Minute)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	records := []models.Record{
		{
			Client:       uuid,
			Time:         models.FromTime(now),
			NetTotalUp:   100,
			NetTotalDown: 200,
			TrafficUp:    0,
			TrafficDown:  0,
		},
		{
			Client:       uuid,
			Time:         models.FromTime(now.Add(5 * time.Minute)),
			NetTotalUp:   150,
			NetTotalDown: 260,
			TrafficUp:    50,
			TrafficDown:  60,
		},
		{
			Client:       uuid,
			Time:         models.FromTime(now.Add(10 * time.Minute)),
			NetTotalUp:   10,
			NetTotalDown: 30,
			TrafficUp:    10,
			TrafficDown:  30,
		},
	}

	for _, rec := range records {
		assert.NoError(t, db.Create(&rec).Error)
	}

	assert.NoError(t, migrateOldRecordsAt(db, currentTime))

	var compacted []models.Record
	assert.NoError(t, db.Table("records_long_term").Find(&compacted).Error)
	require.Len(t, compacted, 1)
	assert.Equal(t, int64(60), compacted[0].TrafficUp)
	assert.Equal(t, int64(90), compacted[0].TrafficDown)
	assert.Equal(t, int64(10), compacted[0].NetTotalUp)
	assert.Equal(t, int64(30), compacted[0].NetTotalDown)
	assert.True(t, compacted[0].Time.ToTime().Equal(records[2].Time.ToTime().Truncate(15*time.Minute)))
}

func TestRepairZeroTrafficDeltasPreservesRawResetDetailBeforeCompaction(t *testing.T) {
	loc := models.GetAppLocation()
	start := time.Date(2026, 6, 6, 0, 0, 0, 0, loc)
	records := []models.Record{
		{Client: uuid, Time: models.FromTime(start), NetTotalUp: 100, NetTotalDown: 200},
		{Client: uuid, Time: models.FromTime(start.Add(5 * time.Minute)), NetTotalUp: 140, NetTotalDown: 260},
		{Client: uuid, Time: models.FromTime(start.Add(10 * time.Minute)), NetTotalUp: 10, NetTotalDown: 20},
		{Client: uuid, Time: models.FromTime(start.Add(15 * time.Minute)), NetTotalUp: 25, NetTotalDown: 35},
	}

	repairZeroTrafficDeltas(records, nil)

	assert.Equal(t, int64(0), records[0].TrafficUp)
	assert.Equal(t, int64(0), records[0].TrafficDown)
	assert.Equal(t, int64(40), records[1].TrafficUp)
	assert.Equal(t, int64(60), records[1].TrafficDown)
	assert.Equal(t, int64(10), records[2].TrafficUp)
	assert.Equal(t, int64(20), records[2].TrafficDown)
	assert.Equal(t, int64(15), records[3].TrafficUp)
	assert.Equal(t, int64(15), records[3].TrafficDown)
}

func TestRepairZeroTrafficDeltasUsesPreviousPersistedBaseline(t *testing.T) {
	loc := models.GetAppLocation()
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, loc)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	assert.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:       uuid,
		Time:         models.FromTime(now.Add(-10 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	rawRecords := []models.Record{
		{Client: uuid, Time: models.FromTime(now), NetTotalUp: 150, NetTotalDown: 260},
		{Client: uuid, Time: models.FromTime(now.Add(5 * time.Minute)), NetTotalUp: 175, NetTotalDown: 300},
	}

	previousByClient, err := getPreviousTrafficRecordsBefore(db, rawRecords)
	assert.NoError(t, err)
	repairZeroTrafficDeltas(rawRecords, previousByClient)

	assert.Equal(t, int64(50), rawRecords[0].TrafficUp)
	assert.Equal(t, int64(60), rawRecords[0].TrafficDown)
	assert.Equal(t, int64(25), rawRecords[1].TrafficUp)
	assert.Equal(t, int64(40), rawRecords[1].TrafficDown)
}

func TestRepairZeroTrafficDeltasIgnoresSameSlotLongTermBaseline(t *testing.T) {
	loc := models.GetAppLocation()
	now := time.Date(2026, 6, 7, 12, 0, 30, 0, loc)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	assert.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:       uuid,
		Time:         models.FromTime(now.Truncate(15 * time.Minute)),
		NetTotalUp:   900,
		NetTotalDown: 900,
	}).Error)

	rawRecords := []models.Record{
		{Client: uuid, Time: models.FromTime(now), NetTotalUp: 150, NetTotalDown: 260},
		{Client: uuid, Time: models.FromTime(now.Add(5 * time.Minute)), NetTotalUp: 175, NetTotalDown: 300},
	}

	previousByClient, err := getPreviousTrafficRecordsBefore(db, rawRecords)
	assert.NoError(t, err)
	repairZeroTrafficDeltas(rawRecords, previousByClient)

	assert.Equal(t, int64(0), rawRecords[0].TrafficUp)
	assert.Equal(t, int64(0), rawRecords[0].TrafficDown)
	assert.Equal(t, int64(25), rawRecords[1].TrafficUp)
	assert.Equal(t, int64(40), rawRecords[1].TrafficDown)
}

func TestCompactRecordRetainsOneHourOverlapWindow(t *testing.T) {
	loc := models.GetAppLocation()
	now := time.Date(2026, 6, 7, 12, 7, 0, 0, loc)
	cutoff := compactRecordCutoff(now)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	records := []models.Record{
		{Client: uuid, Time: models.FromTime(cutoff.Add(-time.Hour - time.Minute)), TrafficUp: 5},
		{Client: uuid, Time: models.FromTime(cutoff.Add(-30 * time.Minute)), TrafficUp: 7},
		{Client: uuid, Time: models.FromTime(now.Add(-3 * time.Hour)), TrafficUp: 11},
	}
	for _, rec := range records {
		assert.NoError(t, db.Create(&rec).Error)
	}

	assert.NoError(t, migrateOldRecordsAt(db, now))

	var remainTimes []models.Record
	assert.NoError(t, db.Table("records").Order("time ASC").Find(&remainTimes).Error)
	require.Len(t, remainTimes, 2)
	assert.True(t, remainTimes[0].Time.ToTime().Equal(records[1].Time.ToTime()))
	assert.True(t, remainTimes[1].Time.ToTime().Equal(records[2].Time.ToTime()))
}

func TestCompactRecordOnlyMigratesCompleteFifteenMinuteBuckets(t *testing.T) {
	loc := models.GetAppLocation()
	now := time.Date(2026, 6, 7, 12, 7, 0, 0, loc)
	cutoff := compactRecordCutoff(now)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	compactable := models.Record{Client: uuid, Time: models.FromTime(cutoff.Add(-time.Minute)), TrafficUp: 5}
	partialSlot := models.Record{Client: uuid, Time: models.FromTime(cutoff.Add(time.Minute)), TrafficUp: 7}
	assert.NoError(t, db.Create(&compactable).Error)
	assert.NoError(t, db.Create(&partialSlot).Error)

	assert.NoError(t, migrateOldRecordsAt(db, now))

	var compacted []models.Record
	assert.NoError(t, db.Table("records_long_term").Order("time ASC").Find(&compacted).Error)
	require.Len(t, compacted, 1)
	assert.True(t, compacted[0].Time.ToTime().Equal(compactable.Time.ToTime().Truncate(15*time.Minute)))

	var rawCount int64
	assert.NoError(t, db.Table("records").Where("time = ?", partialSlot.Time).Count(&rawCount).Error)
	assert.Equal(t, int64(1), rawCount)
}
