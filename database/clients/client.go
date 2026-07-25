package clients

import (
	"encoding/json"
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"math"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func DeleteClient(clientUuid string) error {
	db := dbcore.GetDBInstance()
	pingTasksChanged, err := deleteClient(db, clientUuid)
	if err != nil {
		return err
	}
	if pingTasksChanged {
		return tasks.ReloadPingSchedule()
	}
	return nil
}

func deleteClient(db *gorm.DB, clientUuid string) (bool, error) {
	pingTasksChanged := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var clientCount int64
		if err := tx.Model(&models.Client{}).Where("uuid = ?", clientUuid).Count(&clientCount).Error; err != nil {
			return fmt.Errorf("find client: %w", err)
		}
		if clientCount == 0 {
			return gorm.ErrRecordNotFound
		}

		for label, model := range map[string]any{
			"offline notifications":        &models.OfflineNotification{},
			"traffic report notifications": &models.TrafficReportNotification{},
			"task results":                 &models.TaskResult{},
		} {
			if err := tx.Where("client = ?", clientUuid).Delete(model).Error; err != nil {
				return fmt.Errorf("delete client %s: %w", label, err)
			}
		}

		if err := deleteLegacyClientRows(tx, clientUuid); err != nil {
			return err
		}

		var pingTasks []models.PingTask
		if err := tx.Select("id", "clients").Find(&pingTasks).Error; err != nil {
			return fmt.Errorf("find client ping tasks: %w", err)
		}
		for _, task := range pingTasks {
			remaining, changed := removeClientUUID(task.Clients, clientUuid)
			if !changed {
				continue
			}
			if err := tx.Model(&models.PingTask{}).Where("id = ?", task.Id).Update("clients", remaining).Error; err != nil {
				return fmt.Errorf("remove client from ping task %d: %w", task.Id, err)
			}
			pingTasksChanged = true
		}

		var loadNotifications []models.LoadNotification
		if err := tx.Select("id", "clients").Find(&loadNotifications).Error; err != nil {
			return fmt.Errorf("find client load notifications: %w", err)
		}
		for _, notification := range loadNotifications {
			remaining, changed := removeClientUUID(notification.Clients, clientUuid)
			if !changed {
				continue
			}
			if len(remaining) == 0 {
				if err := tx.Delete(&models.LoadNotification{}, notification.Id).Error; err != nil {
					return fmt.Errorf("delete empty load notification %d: %w", notification.Id, err)
				}
				continue
			}
			if err := tx.Model(&models.LoadNotification{}).Where("id = ?", notification.Id).Update("clients", remaining).Error; err != nil {
				return fmt.Errorf("remove client from load notification %d: %w", notification.Id, err)
			}
		}

		var commandTasks []models.Task
		if err := tx.Select("task_id", "clients").Find(&commandTasks).Error; err != nil {
			return fmt.Errorf("find client command tasks: %w", err)
		}
		for _, task := range commandTasks {
			remaining, changed := removeClientUUID(task.Clients, clientUuid)
			if !changed {
				continue
			}
			if len(remaining) == 0 {
				if err := tx.Where("task_id = ?", task.TaskId).Delete(&models.TaskResult{}).Error; err != nil {
					return fmt.Errorf("delete command task %s results: %w", task.TaskId, err)
				}
				if err := tx.Where("task_id = ?", task.TaskId).Delete(&models.Task{}).Error; err != nil {
					return fmt.Errorf("delete empty command task %s: %w", task.TaskId, err)
				}
				continue
			}
			if err := tx.Model(&models.Task{}).Where("task_id = ?", task.TaskId).Update("clients", remaining).Error; err != nil {
				return fmt.Errorf("remove client from command task %s: %w", task.TaskId, err)
			}
		}

		if err := tx.Delete(&models.Client{}, "uuid = ?", clientUuid).Error; err != nil {
			return fmt.Errorf("delete client: %w", err)
		}
		return nil
	})
	return pingTasksChanged, err
}

func removeClientUUID(clients models.StringArray, clientUUID string) (models.StringArray, bool) {
	remaining := make(models.StringArray, 0, len(clients))
	changed := false
	for _, assignedClient := range clients {
		if assignedClient == clientUUID {
			changed = true
			continue
		}
		remaining = append(remaining, assignedClient)
	}
	return remaining, changed
}

func deleteLegacyClientRows(tx *gorm.DB, clientUUID string) error {
	for _, table := range []string{"records", "records_long_term", "gpu_records", "ping_records"} {
		if !tx.Migrator().HasTable(table) {
			continue
		}
		if err := tx.Exec("DELETE FROM "+table+" WHERE client = ?", clientUUID).Error; err != nil {
			return fmt.Errorf("delete client rows from legacy table %s: %w", table, err)
		}
	}
	return nil
}

func SaveClientInfo(update map[string]interface{}) error {
	db := dbcore.GetDBInstance()
	clientUUID, ok := update["uuid"].(string)
	if !ok || clientUUID == "" {
		return fmt.Errorf("invalid client UUID")
	}

	// 确保更新的字段不为空
	if len(update) == 0 {
		return fmt.Errorf("no fields to update")
	}

	update["updated_at"] = time.Now().UTC()

	toFloat64 := func(value interface{}) (float64, bool) {
		switch typed := value.(type) {
		case float64:
			return typed, true
		case float32:
			return float64(typed), true
		case int:
			return float64(typed), true
		case int8:
			return float64(typed), true
		case int16:
			return float64(typed), true
		case int32:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case uint:
			return float64(typed), true
		case uint8:
			return float64(typed), true
		case uint16:
			return float64(typed), true
		case uint32:
			return float64(typed), true
		case uint64:
			return float64(typed), true
		case json.Number:
			parsed, err := typed.Float64()
			if err != nil {
				return 0, false
			}
			return parsed, true
		default:
			return 0, false
		}
	}

	checkOptionalInt := func(name, key string, maxValue float64) error {
		value, exists := update[key]
		if !exists || value == nil {
			return nil
		}

		numericValue, ok := toFloat64(value)
		if !ok {
			return fmt.Errorf("%s must be a valid number", name)
		}
		if numericValue < 0 || numericValue > maxValue {
			return fmt.Errorf("%s must be a valid non-negative number: %v", name, value)
		}
		return nil
	}

	verify := func(update map[string]interface{}) error {
		if err := checkOptionalInt("Cpu.Cores", "cpu_cores", math.MaxInt-1); err != nil {
			return err
		}
		if err := checkOptionalInt("Cpu.PhysicalCores", "cpu_physical_cores", math.MaxInt-1); err != nil {
			return err
		}
		if err := checkOptionalInt("Ram.Total", "mem_total", math.MaxInt64-1); err != nil {
			return err
		}
		if err := checkOptionalInt("Swap.Total", "swap_total", math.MaxInt64-1); err != nil {
			return err
		}
		if err := checkOptionalInt("Disk.Total", "disk_total", math.MaxInt64-1); err != nil {
			return err
		}
		return nil
	}

	if err := verify(update); err != nil {
		return err
	}

	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(update).Error
	if err != nil {
		return err
	}
	return nil
}

// CreateClient 创建新客户端
func CreateClient() (clientUUID, token string, err error) {
	db := dbcore.GetDBInstance()
	token = utils.GenerateToken()
	clientUUID = uuid.New().String()

	client := models.Client{
		UUID:      clientUUID,
		Token:     token,
		Name:      "client_" + clientUUID[0:8],
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
	}
	if err := tasks.AddDefaultOnClientUUID(clientUUID); err != nil {
		logger.ErrorArgs("clients", "Failed to apply default-on ping tasks to new client:", err)
	}
	return clientUUID, token, nil
}

func CreateClientWithName(name string) (clientUUID, token string, err error) {
	if name == "" {
		return CreateClient()
	}
	db := dbcore.GetDBInstance()
	token = utils.GenerateToken()
	clientUUID = uuid.New().String()
	client := models.Client{
		UUID:      clientUUID,
		Token:     token,
		Name:      name,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
	}
	if err := tasks.AddDefaultOnClientUUID(clientUUID); err != nil {
		logger.ErrorArgs("clients", "Failed to apply default-on ping tasks to new client:", err)
	}
	return clientUUID, token, nil
}

/*
// GetAllClients 获取所有客户端配置

	func getAllClients() (clients []models.Client, err error) {
		db := dbcore.GetDBInstance()
		err = db.Find(&clients).Error
		if err != nil {
			return nil, err
		}
		return clients, nil
	}
*/
func GetClientByUUID(uuid string) (client models.Client, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&client).Error
	if err != nil {
		return models.Client{}, err
	}
	return client, nil
}

func GetClientTokenByUUID(uuid string) (token string, err error) {
	db := dbcore.GetDBInstance()
	var client models.Client
	err = db.Where("uuid = ?", uuid).First(&client).Error
	if err != nil {
		return "", err
	}
	return client.Token, nil
}

func GetAllClientBasicInfo() (clients []models.Client, err error) {
	db := dbcore.GetDBInstance()
	err = db.Find(&clients).Error
	if err != nil {
		return nil, err
	}
	return clients, nil
}

func SaveClient(updates map[string]interface{}) error {
	db := dbcore.GetDBInstance()
	clientUUID, ok := updates["uuid"].(string)
	if !ok || clientUUID == "" {
		return fmt.Errorf("invalid client UUID")
	}

	// 确保更新的字段不为空
	if len(updates) == 0 {
		return fmt.Errorf("no fields to update")
	}

	if v, exists := updates["traffic_limit"]; exists {
		if val, ok := v.(float64); ok {
			if val < 0 || val > math.MaxInt64-1 {
				return fmt.Errorf("traffic_limit must be a valid non-negative int64 value, got %v", val)
			}
		}
	}
	if value, exists := updates["expired_at"]; exists {
		switch typed := value.(type) {
		case nil:
			updates["expired_at"] = nil
		case time.Time:
			updates["expired_at"] = typed.UTC()
		case *time.Time:
			if typed == nil {
				updates["expired_at"] = nil
			} else {
				updates["expired_at"] = typed.UTC()
			}
		case string:
			stamp, err := time.Parse(time.RFC3339Nano, typed)
			if err != nil {
				return fmt.Errorf("expired_at must be an RFC3339 timestamp with a timezone: %w", err)
			}
			updates["expired_at"] = stamp.UTC()
		default:
			return fmt.Errorf("expired_at must be an RFC3339 timestamp with a timezone")
		}
	}

	updates["updated_at"] = time.Now().UTC()

	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(updates).Error
	if err != nil {
		return err
	}
	return nil
}
