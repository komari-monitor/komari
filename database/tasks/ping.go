package tasks

import (
	"context"
	"sort"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/utils"
	"gorm.io/gorm"
)

// AddPingTask 创建延迟监测任务。defaultOn 表示新加入的服务器是否自动开启此监测。
func AddPingTask(clients []string, defaultOn bool, name string, target, task_type string, interval int) (uint, error) {
	return AddDualStackPingTask(clients, defaultOn, name, target, "", task_type, interval, nil)
}

func AddDualStackPingTask(clients []string, defaultOn bool, name, target, targetIPv6, taskType string, interval int, families models.StringArray) (uint, error) {
	db := dbcore.GetDBInstance()
	normalizedClients := normalizePingClients(models.StringArray(clients))
	task := models.PingTask{
		Clients:    normalizedClients,
		DefaultOn:  defaultOn,
		Name:       name,
		Type:       taskType,
		Target:     target,
		IPFamilies: families,
		Family:     "ipv4",
		Enabled:    true,
		Interval:   interval,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
		}
		if task.HasFamily("ipv6") {
			child := task
			child.Id = 0
			child.ParentID = task.Id
			child.Family = "ipv6"
			child.Weight = int(task.Id)
			child.Target = targetIPv6
			child.IPFamilies = nil
			if err := tx.Create(&child).Error; err != nil {
				return err
			}
		}

		// Append by id to avoid races between concurrent create requests.
		result := tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("weight", int(task.Id))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	ReloadPingSchedule()
	return task.Id, nil
}

func DeletePingTask(id []uint) error {
	db := dbcore.GetDBInstance()
	var childIDs []uint
	if err := db.Model(&models.PingTask{}).Where("parent_id IN ?", id).Pluck("id", &childIDs).Error; err != nil {
		return err
	}
	id = append(id, childIDs...)
	// The metric store is independent from the main database, so clean it first
	// to avoid leaving history that can no longer be addressed through the task.
	if err := DeletePingRecords(id); err != nil {
		return err
	}

	result := db.Where("id IN ?", id).Delete(&models.PingTask{})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	ReloadPingSchedule()
	return result.Error
}

// EditPingTask 批量更新延迟监测任务配置。
func EditPingTask(tasks []*models.PingTask) error {
	db := dbcore.GetDBInstance()
	for _, task := range tasks {
		var current models.PingTask
		if err := db.First(&current, task.Id).Error; err != nil {
			return err
		}
		if task.IPFamilies == nil {
			task.IPFamilies = current.IPFamilies
		}
		task.Clients = normalizePingClients(task.Clients)
		// 使用 map 显式更新，避免 GORM struct Updates 跳过 false/0/空切片等零值。
		updates := map[string]interface{}{
			"name":        task.Name,
			"clients":     task.Clients,
			"all_clients": task.DefaultOn,
			"type":        task.Type,
			"target":      task.Target,
			"ip_families": task.IPFamilies,
			"interval":    task.Interval,
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.PingTask{}).Where("id = ? AND parent_id = 0", task.Id).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			var child models.PingTask
			err := tx.Where("parent_id = ? AND family = ?", task.Id, "ipv6").First(&child).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			if err == gorm.ErrRecordNotFound {
				if !task.HasFamily("ipv6") {
					return nil
				}
				child = *task
				child.Id = 0
				child.ParentID = task.Id
				child.Family = "ipv6"
				child.Weight = int(task.Id)
				child.Target = task.TargetIPv6
				child.Enabled = true
				child.IPFamilies = nil
				return tx.Create(&child).Error
			}
			childTarget := task.TargetIPv6
			if childTarget == "" {
				childTarget = child.Target
			}
			return tx.Model(&child).Updates(map[string]interface{}{
				"name": task.Name, "clients": task.Clients, "all_clients": task.DefaultOn,
				"type": task.Type, "target": childTarget, "interval": task.Interval,
				"enabled": task.HasFamily("ipv6"),
			}).Error
		}); err != nil {
			return err
		}
	}
	ReloadPingSchedule()
	return nil
}

// normalizePingClients 保持 clients 字段序列化为 JSON 数组，避免空值变成 null。
func normalizePingClients(clients models.StringArray) models.StringArray {
	if clients == nil {
		return models.StringArray{}
	}
	return clients
}

func GetAllPingTasks() ([]models.PingTask, error) {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Order("weight ASC").Order("id ASC").Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

func GetEditablePingTasks() ([]models.PingTask, error) {
	all, err := GetAllPingTasks()
	if err != nil {
		return nil, err
	}
	children := make(map[uint]models.PingTask)
	for _, task := range all {
		if task.ParentID != 0 && task.Family == "ipv6" {
			children[task.ParentID] = task
		}
	}
	out := make([]models.PingTask, 0, len(all))
	for _, task := range all {
		if task.ParentID != 0 {
			continue
		}
		if len(task.IPFamilies) == 0 {
			task.IPFamilies = models.StringArray{"ipv4"}
		}
		if child, ok := children[task.Id]; ok {
			task.TargetIPv6 = child.Target
		}
		out = append(out, task)
	}
	return out, nil
}

// GetPingTasksByClient 获取指定服务器需要执行的延迟监测任务。
func GetPingTasksByClient(uuid string) []models.PingTask {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Where("clients LIKE ?", `%"`+uuid+`"%`).Order("weight ASC").Order("id ASC").Find(&tasks).Error; err != nil {
		return nil
	}
	return tasks
}

func UpdatePingTaskOrder(order map[uint]int) error {
	if len(order) == 0 {
		return nil
	}

	db := dbcore.GetDBInstance()
	err := db.Transaction(func(tx *gorm.DB) error {
		// Validate all ids before changing any weights. The update response is
		// not a reliable existence check because some drivers report zero rows
		// when the new value equals the current value.
		ids := make([]uint, 0, len(order))
		for id := range order {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

		var existing int64
		if err := tx.Model(&models.PingTask{}).Where("id IN ?", ids).Count(&existing).Error; err != nil {
			return err
		}
		if existing != int64(len(ids)) {
			return gorm.ErrRecordNotFound
		}

		for _, id := range ids {
			weight := order[id]
			result := tx.Model(&models.PingTask{}).Where("id = ?", id).Update("weight", weight)
			if result.Error != nil {
				return result.Error
			}
			if err := tx.Model(&models.PingTask{}).Where("parent_id = ?", id).Update("weight", weight).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	ReloadPingSchedule()
	return nil
}

// ping 记录已完全迁移到 metric store（指标 ping.latency_ms），运行期读写全部走
// metric store，旧 ping_records 表不再参与。

func SavePingRecord(record models.PingRecord) error {
	return metricstore.WritePingRecord(context.Background(), record)
}

func DeletePingRecords(id []uint) error {
	return metricstore.DeletePingRecordsByTask(context.Background(), id)
}

func DeleteAllPingRecords() error {
	return metricstore.DeleteAllPingRecords(context.Background())
}

func ReloadPingSchedule() error {
	pingTasks, err := GetAllPingTasks()
	if err != nil {
		return err
	}
	return utils.ReloadPingSchedule(pingTasks)
}

// AddDefaultOnClientUUID 在新客户端注册后，把该 UUID 追加到所有 default_on=true 的任务的 clients 中（去重）。
func AddDefaultOnClientUUID(uuid string) error {
	if uuid == "" {
		return nil
	}
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Where("all_clients = ?", true).Find(&tasks).Error; err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	changed := false
	for _, task := range tasks {
		exists := false
		for _, c := range task.Clients {
			if c == uuid {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		next := append(models.StringArray{}, task.Clients...)
		next = append(next, uuid)
		if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("clients", next).Error; err != nil {
			return err
		}
		changed = true
	}
	if changed {
		return ReloadPingSchedule()
	}
	return nil
}

func GetPingRecords(uuid string, taskId int, start, end time.Time) ([]models.PingRecord, error) {
	return metricstore.GetPingRecords(context.Background(), uuid, taskId, start, end)
}
