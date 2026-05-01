package tasks

import (
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils"
	"gorm.io/gorm"
)

// AddPingTask 创建延迟监测任务。defaultOn 表示新加入的服务器是否自动开启此监测。
func AddPingTask(clients []string, defaultOn bool, name string, target, task_type string, interval int) (uint, error) {
	db := dbcore.GetDBInstance()
	normalizedClients := normalizePingClients(models.StringArray(clients))
	task := models.PingTask{
		Clients:   normalizedClients,
		DefaultOn: defaultOn,
		Name:      name,
		Type:      task_type,
		Target:    target,
		Interval:  interval,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&task).Error; err != nil {
			return err
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
		task.Clients = normalizePingClients(task.Clients)
		// 使用 map 显式更新，避免 GORM struct Updates 跳过 false/0/空切片等零值。
		updates := map[string]interface{}{
			"name":        task.Name,
			"clients":     task.Clients,
			"all_clients": task.DefaultOn,
			"type":        task.Type,
			"target":      task.Target,
			"interval":    task.Interval,
		}
		result := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Updates(updates)
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
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

// GetPingTasksByClient 获取指定服务器需要执行的延迟监测任务。
func GetPingTasksByClient(uuid string) []models.PingTask {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Where("clients LIKE ?", `%"`+uuid+`"%`).Find(&tasks).Error; err != nil {
		return nil
	}
	return tasks
}

func UpdatePingTaskOrder(order map[uint]int) error {
	db := dbcore.GetDBInstance()
	err := db.Transaction(func(tx *gorm.DB) error {
		for id, weight := range order {
			result := tx.Model(&models.PingTask{}).Where("id = ?", id).Update("weight", weight)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
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

func SavePingRecord(record models.PingRecord) error {
	db := dbcore.GetDBInstance()
	return db.Create(&record).Error
}

func DeletePingRecordsBefore(time time.Time) error {
	db := dbcore.GetDBInstance()
	err := db.Where("time < ?", time).Delete(&models.PingRecord{}).Error
	return err
}

func DeletePingRecords(id []uint) error {
	db := dbcore.GetDBInstance()
	result := db.Where("task_id IN ?", id).Delete(&models.PingRecord{})
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func DeleteAllPingRecords() error {
	db := dbcore.GetDBInstance()
	result := db.Exec("DELETE FROM ping_records")
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}
func ReloadPingSchedule() error {
	db := dbcore.GetDBInstance()
	var pingTasks []models.PingTask
	if err := db.Find(&pingTasks).Error; err != nil {
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

// MigrateAllClientsExpansion 启动时把旧版 default_on=true 且 clients 为空的任务展开为当前所有客户端 UUID。
// 迁移后 clients 始终是显式列表，调度路径不再依赖 default_on。
func MigrateAllClientsExpansion() error {
	db := dbcore.GetDBInstance()
	var tasks []models.PingTask
	if err := db.Where("all_clients = ?", true).Find(&tasks).Error; err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	var clients []models.Client
	if err := db.Select("uuid").Find(&clients).Error; err != nil {
		return err
	}
	if len(clients) == 0 {
		return nil
	}
	allUUIDs := make(models.StringArray, 0, len(clients))
	for _, c := range clients {
		if c.UUID != "" {
			allUUIDs = append(allUUIDs, c.UUID)
		}
	}
	for _, task := range tasks {
		if len(task.Clients) > 0 {
			continue
		}
		if err := db.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("clients", allUUIDs).Error; err != nil {
			return err
		}
	}
	return nil
}

func GetPingRecords(uuid string, taskId int, start, end time.Time) ([]models.PingRecord, error) {
	db := dbcore.GetDBInstance()
	var records []models.PingRecord
	dbQuery := db.Model(&models.PingRecord{})
	if uuid != "" {
		dbQuery = dbQuery.Where("client = ?", uuid)
	}
	if taskId >= 0 {
		dbQuery = dbQuery.Where("task_id = ?", uint(taskId))
	}
	if err := dbQuery.Where("time >= ? AND time <= ?", start, end).Order("time DESC").Find(&records).Error; err != nil {
		return nil, err
	}
	return records, nil
}
