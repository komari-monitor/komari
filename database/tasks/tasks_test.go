package tasks

import (
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

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

func TestUpdatePingTaskOrderAllowsUnchangedWeights(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:ping_task_update_order?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()

	items := []models.PingTask{
		{Name: "first", Weight: 0, Type: "icmp", Target: "first.example", Interval: 60},
		{Name: "second", Weight: 1, Type: "icmp", Target: "second.example", Interval: 60},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatalf("create ping tasks: %v", err)
	}

	if err := UpdatePingTaskOrder(map[uint]int{
		items[0].Id: 0,
		items[1].Id: 2,
	}); err != nil {
		t.Fatalf("update ping task order: %v", err)
	}

	var first, second models.PingTask
	if err := db.First(&first, items[0].Id).Error; err != nil {
		t.Fatalf("load first ping task: %v", err)
	}
	if err := db.First(&second, items[1].Id).Error; err != nil {
		t.Fatalf("load second ping task: %v", err)
	}
	if first.Weight != 0 || second.Weight != 2 {
		t.Fatalf("weights = [%d, %d], want [0, 2]", first.Weight, second.Weight)
	}
}

func TestGetPingTasksByClientOrdersByWeightThenID(t *testing.T) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:ping_task_client_order?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()

	items := []models.PingTask{
		{Name: "third", Weight: 2, Clients: models.StringArray{"node-a"}, Type: "icmp", Target: "third.example", Interval: 60},
		{Name: "first", Weight: 0, Clients: models.StringArray{"node-a"}, Type: "icmp", Target: "first.example", Interval: 60},
		{Name: "other", Weight: 1, Clients: models.StringArray{"node-b"}, Type: "icmp", Target: "other.example", Interval: 60},
	}
	if err := db.Create(&items).Error; err != nil {
		t.Fatalf("create ping tasks: %v", err)
	}

	ordered := GetPingTasksByClient("node-a")
	if len(ordered) != 2 || ordered[0].Id != items[1].Id || ordered[1].Id != items[0].Id {
		t.Fatalf("ping task order = %#v, want ids [%d %d]", ordered, items[1].Id, items[0].Id)
	}
}
