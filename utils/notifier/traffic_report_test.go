package notifier

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetClientTrafficInRangeAvoidsOverlappingRawAndLongTermRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-overlap"
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sharedSlot := start.Add(15 * time.Minute)

	assert.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:      clientUUID,
		Time:        models.FromTime(sharedSlot),
		TrafficUp:   100,
		TrafficDown: 200,
	}).Error)

	rawRecords := []models.Record{
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(1 * time.Minute)), TrafficUp: 40, TrafficDown: 80},
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(5 * time.Minute)), TrafficUp: 60, TrafficDown: 120},
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(16 * time.Minute)), TrafficUp: 30, TrafficDown: 50},
	}
	for _, record := range rawRecords {
		assert.NoError(t, db.Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, sharedSlot.Add(30*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(380), used)
}

func TestGetClientTrafficInRangeNormalizesLongTermSlotForOverlap(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-overlap-normalized"
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	sharedSlot := start.Add(15 * time.Minute)

	assert.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:      clientUUID,
		Time:        models.FromTime(sharedSlot.Add(8 * time.Minute)),
		TrafficUp:   100,
		TrafficDown: 200,
	}).Error)

	rawRecords := []models.Record{
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(1 * time.Minute)), TrafficUp: 40, TrafficDown: 80},
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(5 * time.Minute)), TrafficUp: 60, TrafficDown: 120},
		{Client: clientUUID, Time: models.FromTime(sharedSlot.Add(16 * time.Minute)), TrafficUp: 30, TrafficDown: 50},
	}
	for _, record := range rawRecords {
		assert.NoError(t, db.Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, sharedSlot.Add(30*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(380), used)
}

func TestGetClientTrafficInRangeSumsPersistedDeltasAcrossCounterReset(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-reset"
	start := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	records := []models.Record{
		{Client: clientUUID, Time: models.FromTime(start.Add(0 * time.Minute)), NetTotalUp: 100, NetTotalDown: 200, TrafficUp: 0, TrafficDown: 0},
		{Client: clientUUID, Time: models.FromTime(start.Add(5 * time.Minute)), NetTotalUp: 150, NetTotalDown: 260, TrafficUp: 50, TrafficDown: 60},
		{Client: clientUUID, Time: models.FromTime(start.Add(10 * time.Minute)), NetTotalUp: 10, NetTotalDown: 30, TrafficUp: 10, TrafficDown: 30},
		{Client: clientUUID, Time: models.FromTime(start.Add(15 * time.Minute)), NetTotalUp: 25, NetTotalDown: 40, TrafficUp: 15, TrafficDown: 10},
	}
	for _, record := range records {
		assert.NoError(t, db.Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, start.Add(20*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(175), used)
}

func TestGetClientTrafficInRangeFallsBackForPersistedZeroDeltas(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-zero-deltas"
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	records := []models.Record{
		{Client: clientUUID, Time: models.FromTime(start.Add(-5 * time.Minute)), NetTotalUp: 100, NetTotalDown: 200},
		{Client: clientUUID, Time: models.FromTime(start.Add(0 * time.Minute)), NetTotalUp: 130, NetTotalDown: 250},
		{Client: clientUUID, Time: models.FromTime(start.Add(5 * time.Minute)), NetTotalUp: 160, NetTotalDown: 310},
		{Client: clientUUID, Time: models.FromTime(start.Add(10 * time.Minute)), NetTotalUp: 10, NetTotalDown: 30},
		{Client: clientUUID, Time: models.FromTime(start.Add(15 * time.Minute)), NetTotalUp: 25, NetTotalDown: 40},
	}
	for _, record := range records {
		assert.NoError(t, db.Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, start.Add(20*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(235), used)
}

func TestGetClientTrafficInRangeFallsBackForLongTermZeroDeltas(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-long-term-zero-deltas"
	start := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	assert.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(start.Add(-15 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	longTermRecords := []models.Record{
		{Client: clientUUID, Time: models.FromTime(start), NetTotalUp: 140, NetTotalDown: 260},
		{Client: clientUUID, Time: models.FromTime(start.Add(15 * time.Minute)), NetTotalUp: 180, NetTotalDown: 330},
	}
	for _, record := range longTermRecords {
		assert.NoError(t, db.Table("records_long_term").Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, start.Add(30*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(210), used)
}

func TestGetClientTrafficInRangePrefersRawSlotOverZeroLongTermSlot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)
	assert.NoError(t, db.AutoMigrate(&models.Record{}))
	assert.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))

	clientUUID := "client-zero-long-term-with-raw-reset"
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	slot := start.Add(15 * time.Minute)
	assert.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(start.Add(-5 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	assert.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(slot),
		NetTotalUp:   15,
		NetTotalDown: 25,
		TrafficUp:    0,
		TrafficDown:  0,
	}).Error)

	rawRecords := []models.Record{
		{Client: clientUUID, Time: models.FromTime(slot.Add(1 * time.Minute)), NetTotalUp: 130, NetTotalDown: 240},
		{Client: clientUUID, Time: models.FromTime(slot.Add(5 * time.Minute)), NetTotalUp: 10, NetTotalDown: 20},
		{Client: clientUUID, Time: models.FromTime(slot.Add(10 * time.Minute)), NetTotalUp: 15, NetTotalDown: 25},
	}
	for _, record := range rawRecords {
		assert.NoError(t, db.Create(&record).Error)
	}

	used, err := getClientTrafficInRangeWithDB(db, clientUUID, "sum", start, slot.Add(15*time.Minute))
	assert.NoError(t, err)
	assert.Equal(t, int64(110), used)
}
