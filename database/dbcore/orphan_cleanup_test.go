package dbcore

import (
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCleanupOrphanedClientDataRepairsAssociations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:full-client-orphan-cleanup?mode=memory&cache=shared&_foreign_keys=off"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&models.Client{}, &models.PingTask{},
		&models.OfflineNotification{}, &models.TrafficReportNotification{},
		&models.LoadNotification{}, &models.Task{}, &models.TaskResult{},
	))
	for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
		require.NoError(t, db.Exec("CREATE TABLE "+table+" (client TEXT NOT NULL, task_id INTEGER)").Error)
		require.NoError(t, db.Exec("INSERT INTO "+table+" (client, task_id) VALUES (?, ?), (?, ?)", "client-a", 1, "deleted-client", 999).Error)
	}
	require.NoError(t, db.Create(&models.Client{UUID: "client-a", Token: "token-a"}).Error)
	pingTask := models.PingTask{Name: "DNS", Clients: models.StringArray{"client-a", "deleted-client"}, Type: "icmp", Target: "1.1.1.1", Interval: 10}
	require.NoError(t, db.Create(&pingTask).Error)
	require.NoError(t, db.Create([]models.OfflineNotification{{Client: "client-a"}, {Client: "deleted-client"}}).Error)
	require.NoError(t, db.Create([]models.TrafficReportNotification{{Client: "client-a"}, {Client: "deleted-client"}}).Error)
	require.NoError(t, db.Create([]models.LoadNotification{
		{Name: "shared", Clients: models.StringArray{"client-a", "deleted-client"}, Metric: "cpu", Interval: 15},
		{Name: "orphan", Clients: models.StringArray{"deleted-client"}, Metric: "cpu", Interval: 15},
	}).Error)
	require.NoError(t, db.Create([]models.Task{
		{TaskId: "shared", Clients: models.StringArray{"client-a", "deleted-client"}, Command: "uptime"},
		{TaskId: "orphan", Clients: models.StringArray{"deleted-client"}, Command: "uptime"},
	}).Error)
	require.NoError(t, db.Create([]models.TaskResult{
		{TaskId: "shared", Client: "client-a"},
		{TaskId: "shared", Client: "deleted-client"},
		{TaskId: "missing-task", Client: "client-a"},
	}).Error)

	require.NoError(t, cleanupOrphanedClientData(db))
	var gotPingTask models.PingTask
	require.NoError(t, db.First(&gotPingTask, pingTask.Id).Error)
	assert.Equal(t, models.StringArray{"client-a"}, gotPingTask.Clients)
	var loadNotifications []models.LoadNotification
	require.NoError(t, db.Find(&loadNotifications).Error)
	require.Len(t, loadNotifications, 1)
	assert.Equal(t, models.StringArray{"client-a"}, loadNotifications[0].Clients)
	var tasks []models.Task
	require.NoError(t, db.Find(&tasks).Error)
	require.Len(t, tasks, 1)
	assert.Equal(t, models.StringArray{"client-a"}, tasks[0].Clients)
	for _, model := range []any{&models.OfflineNotification{}, &models.TrafficReportNotification{}, &models.TaskResult{}} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	}
	for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		assert.Equal(t, int64(1), count, table)
	}
}
