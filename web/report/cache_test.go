package report

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openReportCacheTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.Record{}))
	require.NoError(t, db.Table("records_long_term").AutoMigrate(&models.Record{}))
	require.NoError(t, db.AutoMigrate(&models.GPURecord{}))
	return db
}

func resetReportCache(t *testing.T) {
	t.Helper()
	Records.Flush()
	t.Cleanup(func() {
		Records.Flush()
	})
}

func TestAppendClientReportSerializesCacheMutation(t *testing.T) {
	resetReportCache(t)

	clientUUID := "client-append-helper"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	first, err := AppendClientReport(clientUUID, v1.Report{
		UpdatedAt: now.Add(-10 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 100, TotalDown: 200},
	})
	require.NoError(t, err)
	second, err := AppendClientReport(clientUUID, v1.Report{
		UpdatedAt: now.Add(-5 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 150, TotalDown: 260},
	})
	require.NoError(t, err)

	cached, ok := Records.Get(clientUUID)
	require.True(t, ok)
	reports, ok := cached.([]v1.Report)
	require.True(t, ok)
	require.Len(t, reports, 2)
	assert.Equal(t, int64(100), reports[0].Network.TotalUp)
	assert.Equal(t, int64(150), reports[1].Network.TotalUp)
	assert.Equal(t, first.UpdatedAt, reports[0].UpdatedAt)
	assert.Equal(t, second.UpdatedAt, reports[1].UpdatedAt)
	assert.True(t, reports[0].UpdatedAt.After(now.Add(-10*time.Second)))
	assert.True(t, reports[1].UpdatedAt.After(now.Add(-5*time.Second)))
}

func TestAppendClientReportRejectsCorruptedCacheValue(t *testing.T) {
	resetReportCache(t)

	Records.Set("client-bad-cache", "not reports", cache.DefaultExpiration)

	_, err := AppendClientReport("client-bad-cache", v1.Report{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid report type")
}

func TestSaveClientReportToDBStoresCachedTrafficDelta(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-cached-delta"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-2 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	Records.Set(clientUUID, []v1.Report{
		{
			UpdatedAt: now.Add(-30 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 130, TotalDown: 260},
		},
		{
			UpdatedAt: now.Add(-10 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 175, TotalDown: 320},
		},
	}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, now))

	var saved models.Record
	require.NoError(t, db.Where("client = ? AND time = ?", clientUUID, models.FromTime(now)).First(&saved).Error)
	assert.Equal(t, int64(175), saved.NetTotalUp)
	assert.Equal(t, int64(320), saved.NetTotalDown)
	assert.Equal(t, int64(75), saved.TrafficUp)
	assert.Equal(t, int64(120), saved.TrafficDown)
}

func TestSaveClientReportToDBStoresCachedTrafficDeltaAfterCounterReset(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-cached-reset"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-2 * time.Minute)),
		NetTotalUp:   500,
		NetTotalDown: 700,
	}).Error)

	Records.Set(clientUUID, []v1.Report{
		{
			UpdatedAt: now.Add(-20 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 30, TotalDown: 45},
		},
		{
			UpdatedAt: now.Add(-5 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 40, TotalDown: 55},
		},
	}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, now))

	var saved models.Record
	require.NoError(t, db.Where("client = ? AND time = ?", clientUUID, models.FromTime(now)).First(&saved).Error)
	assert.Equal(t, int64(40), saved.NetTotalUp)
	assert.Equal(t, int64(55), saved.NetTotalDown)
	assert.Equal(t, int64(40), saved.TrafficUp)
	assert.Equal(t, int64(55), saved.TrafficDown)
}

func TestSaveClientReportToDBSumsCachedTrafficWithoutPersistedBaseline(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-cached-no-baseline"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	Records.Set(clientUUID, []v1.Report{
		{
			UpdatedAt: now.Add(-40 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 100, TotalDown: 200},
		},
		{
			UpdatedAt: now.Add(-20 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 130, TotalDown: 250},
		},
		{
			UpdatedAt: now.Add(-5 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 155, TotalDown: 280},
		},
	}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, now))

	var saved models.Record
	require.NoError(t, db.Where("client = ? AND time = ?", clientUUID, models.FromTime(now)).First(&saved).Error)
	assert.Equal(t, int64(155), saved.NetTotalUp)
	assert.Equal(t, int64(280), saved.NetTotalDown)
	assert.Equal(t, int64(55), saved.TrafficUp)
	assert.Equal(t, int64(80), saved.TrafficDown)
}

func TestSaveClientReportToDBSumsCachedTrafficAcrossIntraMinuteReset(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-cached-intra-minute-reset"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-2 * time.Minute)),
		NetTotalUp:   500,
		NetTotalDown: 700,
	}).Error)

	Records.Set(clientUUID, []v1.Report{
		{
			UpdatedAt: now.Add(-45 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 540, TotalDown: 760},
		},
		{
			UpdatedAt: now.Add(-25 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 10, TotalDown: 20},
		},
		{
			UpdatedAt: now.Add(-5 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 25, TotalDown: 35},
		},
	}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, now))

	var saved models.Record
	require.NoError(t, db.Where("client = ? AND time = ?", clientUUID, models.FromTime(now)).First(&saved).Error)
	assert.Equal(t, int64(25), saved.NetTotalUp)
	assert.Equal(t, int64(35), saved.NetTotalDown)
	assert.Equal(t, int64(65), saved.TrafficUp)
	assert.Equal(t, int64(95), saved.TrafficDown)
}

func TestSaveClientReportToDBDoesNotRecountRetainedCachePoints(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-retained-cache-point"
	firstFlush := time.Date(2026, 6, 13, 12, 0, 30, 0, time.UTC)
	secondFlush := firstFlush.Add(30 * time.Second)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(firstFlush.Add(-2 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	Records.Set(clientUUID, []v1.Report{{
		UpdatedAt: firstFlush.Add(-10 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 175, TotalDown: 320},
	}}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, firstFlush))

	reports, ok := Records.Get(clientUUID)
	require.True(t, ok)
	retained := reports.([]v1.Report)
	retained = append(retained, v1.Report{
		UpdatedAt: secondFlush.Add(-5 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 190, TotalDown: 360},
	})
	Records.Set(clientUUID, retained, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, secondFlush))

	var saved []models.Record
	require.NoError(t, db.Where("client = ?", clientUUID).Order("time ASC").Find(&saved).Error)
	require.Len(t, saved, 3)
	assert.Equal(t, int64(75), saved[1].TrafficUp)
	assert.Equal(t, int64(120), saved[1].TrafficDown)
	assert.Equal(t, int64(15), saved[2].TrafficUp)
	assert.Equal(t, int64(40), saved[2].TrafficDown)
}

func TestSaveClientReportToDBSkipsInvalidCachedReports(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-invalid-report"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-2 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)

	Records.Set(clientUUID, []v1.Report{
		{
			UpdatedAt: now.Add(-30 * time.Second),
			Network:   v1.NetworkReport{TotalUp: -1, TotalDown: 260},
		},
		{
			UpdatedAt: now.Add(-10 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 175, TotalDown: 320},
		},
	}, cache.DefaultExpiration)

	require.NoError(t, saveClientReportToDB(db, now))

	var saved models.Record
	require.NoError(t, db.Where("client = ? AND time = ?", clientUUID, models.FromTime(now)).First(&saved).Error)
	assert.Equal(t, int64(175), saved.NetTotalUp)
	assert.Equal(t, int64(320), saved.NetTotalDown)
	assert.Equal(t, int64(75), saved.TrafficUp)
	assert.Equal(t, int64(120), saved.TrafficDown)
}

func TestSaveClientReportToDBIgnoresSameTimeRowsWhenComputingDelta(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-same-time"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now),
		NetTotalUp:   999,
		NetTotalDown: 999,
	}).Error)

	records := []models.Record{{
		Client:       clientUUID,
		Time:         models.FromTime(now),
		NetTotalUp:   175,
		NetTotalDown: 320,
	}}
	require.NoError(t, fillTrafficDeltas(db, records, nil))

	assert.Equal(t, int64(75), records[0].TrafficUp)
	assert.Equal(t, int64(120), records[0].TrafficDown)
}

func TestSaveClientReportToDBUsesLatestLongTermRecordWhenNewer(t *testing.T) {
	resetReportCache(t)
	db := openReportCacheTestDB(t)

	clientUUID := "client-long-term-previous"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-10 * time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}).Error)
	require.NoError(t, db.Table("records_long_term").Create(&models.Record{
		Client:       clientUUID,
		Time:         models.FromTime(now.Add(-2 * time.Minute)),
		NetTotalUp:   150,
		NetTotalDown: 260,
	}).Error)

	records := []models.Record{{
		Client:       clientUUID,
		Time:         models.FromTime(now),
		NetTotalUp:   175,
		NetTotalDown: 320,
	}}
	require.NoError(t, fillTrafficDeltas(db, records, nil))

	assert.Equal(t, int64(25), records[0].TrafficUp)
	assert.Equal(t, int64(60), records[0].TrafficDown)
}
