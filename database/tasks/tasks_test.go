package tasks

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func TestClearTaskResultsByTimeBeforeUsesUTCTimeValue(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:task_cleanup_time?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()

	taskID := "task-cleanup-time"
	if err := db.Create(&models.Task{TaskId: taskID, Command: "true"}).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	cutoff := time.Date(2026, 7, 17, 12, 0, 0, 123456789, time.UTC)
	results := []models.TaskResult{
		{TaskId: taskID, Client: "old", CreatedAt: cutoff.Add(-time.Nanosecond)},
		{TaskId: taskID, Client: "boundary", CreatedAt: cutoff},
		{TaskId: taskID, Client: "new", CreatedAt: cutoff.Add(time.Nanosecond)},
	}
	if err := db.Create(&results).Error; err != nil {
		t.Fatalf("create task results: %v", err)
	}

	localCutoff := cutoff.In(time.FixedZone("UTC+8", 8*60*60))
	if err := ClearTaskResultsByTimeBefore(localCutoff); err != nil {
		t.Fatalf("clear task results: %v", err)
	}
	var remaining []models.TaskResult
	if err := db.Where("task_id = ?", taskID).Order("created_at").Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining results: %v", err)
	}
	if len(remaining) != 2 || remaining[0].Client != "boundary" || remaining[1].Client != "new" {
		t.Fatalf("remaining results = %#v, want boundary and new", remaining)
	}
}

func TestGetAllPingTasksOrdersByWeightThenID(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:ping_task_order?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()

	tasks := []models.PingTask{
		{Name: "third", Weight: 2, Type: "icmp", Target: "third.example", Interval: 60},
		{Name: "first", Weight: 0, Type: "icmp", Target: "first.example", Interval: 60},
		{Name: "second", Weight: 0, Type: "icmp", Target: "second.example", Interval: 60},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatalf("create ping tasks: %v", err)
	}

	ordered, err := GetAllPingTasks()
	if err != nil {
		t.Fatalf("get ordered ping tasks: %v", err)
	}
	if len(ordered) != len(tasks) {
		t.Fatalf("ordered ping task count = %d, want %d", len(ordered), len(tasks))
	}
	if ordered[0].Id != tasks[1].Id || ordered[1].Id != tasks[2].Id || ordered[2].Id != tasks[0].Id {
		t.Fatalf("ping task order = %#v, want ids [%d %d %d]", ordered, tasks[1].Id, tasks[2].Id, tasks[0].Id)
	}
}
