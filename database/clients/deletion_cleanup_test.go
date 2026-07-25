package clients

import (
	"fmt"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeleteClientCleansRelatedRowsAndSharedAssignments(t *testing.T) {
	for _, foreignKeys := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign_keys_%t", foreignKeys), func(t *testing.T) {
			dsn := fmt.Sprintf("file:delete-client-cleanup-%t?mode=memory&cache=shared&_foreign_keys=%t", foreignKeys, foreignKeys)
			db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(
				&models.Client{}, &models.PingTask{},
				&models.OfflineNotification{}, &models.TrafficReportNotification{},
				&models.LoadNotification{}, &models.Task{}, &models.TaskResult{},
			))
			for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
				require.NoError(t, db.Exec("CREATE TABLE "+table+" (client TEXT NOT NULL, task_id INTEGER)").Error)
			}
			require.NoError(t, db.Create([]models.Client{
				{UUID: "client-a", Token: "token-a", Name: "Server A"},
				{UUID: "client-b", Token: "token-b", Name: "Server B"},
			}).Error)

			pingTask := models.PingTask{Name: "Public DNS", Clients: models.StringArray{"client-a", "client-b"}, Type: "icmp", Target: "1.1.1.1", Interval: 10}
			require.NoError(t, db.Create(&pingTask).Error)
			require.NoError(t, db.Create([]models.OfflineNotification{{Client: "client-a"}, {Client: "client-b"}}).Error)
			require.NoError(t, db.Create([]models.TrafficReportNotification{{Client: "client-a"}, {Client: "client-b"}}).Error)
			require.NoError(t, db.Create([]models.LoadNotification{
				{Name: "shared", Clients: models.StringArray{"client-a", "client-b"}, Metric: "cpu", Interval: 15},
				{Name: "only-a", Clients: models.StringArray{"client-a"}, Metric: "cpu", Interval: 15},
			}).Error)
			require.NoError(t, db.Create([]models.Task{
				{TaskId: "shared", Clients: models.StringArray{"client-a", "client-b"}, Command: "uptime"},
				{TaskId: "only-a", Clients: models.StringArray{"client-a"}, Command: "uptime"},
			}).Error)
			require.NoError(t, db.Create([]models.TaskResult{
				{TaskId: "shared", Client: "client-a"},
				{TaskId: "shared", Client: "client-b"},
				{TaskId: "only-a", Client: "client-a"},
			}).Error)
			for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
				require.NoError(t, db.Exec("INSERT INTO "+table+" (client, task_id) VALUES (?, ?), (?, ?)",
					"client-a", pingTask.Id, "client-b", pingTask.Id).Error)
			}

			changed, err := deleteClient(db, "client-a")
			require.NoError(t, err)
			assert.True(t, changed)
			assertModelCount(t, db, &models.Client{}, "uuid = ?", 0, "client-a")
			assertModelCount(t, db, &models.OfflineNotification{}, "client = ?", 0, "client-a")
			assertModelCount(t, db, &models.TrafficReportNotification{}, "client = ?", 0, "client-a")
			assertModelCount(t, db, &models.TaskResult{}, "client = ?", 0, "client-a")
			for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
				var count int64
				require.NoError(t, db.Table(table).Where("client = ?", "client-a").Count(&count).Error)
				assert.Zero(t, count, table)
			}

			var gotPingTask models.PingTask
			require.NoError(t, db.First(&gotPingTask, pingTask.Id).Error)
			assert.Equal(t, models.StringArray{"client-b"}, gotPingTask.Clients)
			var remainingLoadRules []models.LoadNotification
			require.NoError(t, db.Find(&remainingLoadRules).Error)
			require.Len(t, remainingLoadRules, 1)
			assert.Equal(t, models.StringArray{"client-b"}, remainingLoadRules[0].Clients)
			var remainingTasks []models.Task
			require.NoError(t, db.Find(&remainingTasks).Error)
			require.Len(t, remainingTasks, 1)
			assert.Equal(t, models.StringArray{"client-b"}, remainingTasks[0].Clients)
		})
	}
}

func TestDeleteClientRollsBackRelatedRowsWhenClientDeleteFails(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:delete-client-rollback?mode=memory&cache=shared&_foreign_keys=off"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&models.Client{}, &models.PingTask{},
		&models.OfflineNotification{}, &models.TrafficReportNotification{},
		&models.LoadNotification{}, &models.Task{}, &models.TaskResult{},
	))
	require.NoError(t, db.Create(&models.Client{UUID: "client-a", Token: "token-a"}).Error)
	require.NoError(t, db.Create(&models.OfflineNotification{Client: "client-a", Enable: true}).Error)
	require.NoError(t, db.Exec(`CREATE TRIGGER reject_client_delete BEFORE DELETE ON clients
		BEGIN SELECT RAISE(FAIL, 'delete rejected'); END`).Error)

	_, err = deleteClient(db, "client-a")
	require.Error(t, err)
	assertModelCount(t, db, &models.Client{}, "uuid = ?", 1, "client-a")
	assertModelCount(t, db, &models.OfflineNotification{}, "client = ?", 1, "client-a")
}

func assertModelCount(t *testing.T, db *gorm.DB, model any, query string, expected int64, args ...any) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(model).Where(query, args...).Count(&count).Error)
	assert.Equal(t, expected, count)
}
