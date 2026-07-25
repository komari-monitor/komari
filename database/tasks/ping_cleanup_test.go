package tasks

import (
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDeletePingTaskRowsCleansLegacyRecords(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:delete-ping-task-cleanup?mode=memory&cache=shared&_foreign_keys=off"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.Client{}, &models.PingTask{}))
	require.NoError(t, db.Create(&models.Client{UUID: "client-a", Token: "token-a", Name: "Server A"}).Error)
	tasks := []models.PingTask{
		{Name: "Target C", Clients: models.StringArray{"client-a"}, Type: "icmp", Target: "c.example.com", Interval: 10},
		{Name: "Target D", Clients: models.StringArray{"client-a"}, Type: "icmp", Target: "d.example.com", Interval: 10},
	}
	require.NoError(t, db.Create(&tasks).Error)
	require.NoError(t, db.Exec("CREATE TABLE ping_records (client TEXT NOT NULL, task_id INTEGER NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO ping_records (client, task_id) VALUES (?, ?), (?, ?)", "client-a", tasks[0].Id, "client-a", tasks[1].Id).Error)

	require.NoError(t, deletePingTaskRows(db, []uint{tasks[0].Id}))
	var remainingTasks []models.PingTask
	require.NoError(t, db.Find(&remainingTasks).Error)
	require.Len(t, remainingTasks, 1)
	assert.Equal(t, tasks[1].Id, remainingTasks[0].Id)
	var count int64
	require.NoError(t, db.Table("ping_records").Where("task_id = ?", tasks[0].Id).Count(&count).Error)
	assert.Zero(t, count)
}
