package client

import (
	"math"
	"time"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/pkg/utils"

	"fmt"

	"github.com/google/uuid"
)

func DeleteClient(clientUuid string) error {
	db := dbcore.GetDBInstance()
	c := models.Client{}
	err := db.Model(&models.Client{}).Where("uuid = ?", clientUuid).First(&c).Error
	if err != nil {
		return err
	}
	err = db.Delete(&models.Client{}, "uuid = ?", clientUuid).Error
	if err != nil {
		return err
	}
	event.Trigger(eventType.ClientDeleted, event.M{
		"client": c})
	return nil
}

func PartialUpdate(update map[string]interface{}) error {
	db := dbcore.GetDBInstance()
	clientUUID, ok := update["uuid"].(string)
	if !ok || clientUUID == "" {
		return fmt.Errorf("invalid client UUID")
	}

	// 确保更新的字段不为空
	if len(update) == 0 {
		return fmt.Errorf("no fields to update")
	}

	c := models.Client{}
	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).First(&c).Error
	if err != nil {
		return err
	}

	update["updated_at"] = time.Now()

	checkInt64 := func(name string, val float64) error {
		if val < 0 {
			return fmt.Errorf("%s must be non-negative, got %d", name, int64(val))
		}
		if val > math.MaxInt64-1 {
			return fmt.Errorf("%s exceeds int64 max limit: %d", name, int64(val))
		}
		return nil
	}

	verify := func(update map[string]interface{}) error {
		if update["cpu_cores"].(float64) < 0 || update["cpu_cores"].(float64) > math.MaxInt-1 {
			return fmt.Errorf("Cpu.Cores be not a valid int64 number: %d", update["cpu_cores"])
		}
		if err := checkInt64("Ram.Total", update["mem_total"].(float64)); err != nil {
			return err
		}
		if err := checkInt64("Swap.Total", update["swap_total"].(float64)); err != nil {
			return err
		}
		if err := checkInt64("Disk.Total", update["disk_total"].(float64)); err != nil {
			return err
		}
		return nil
	}

	if err := verify(update); err != nil {
		return err
	}

	err = db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(update).Error
	if err != nil {
		return err
	}

	new_c := models.Client{}
	err = db.Model(&models.Client{}).Where("uuid = ?", clientUUID).First(&new_c).Error
	if err != nil {
		return err
	}

	event.Trigger(eventType.ClientUpdated, event.M{
		"old": c,
		"new": new_c,
	})
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
		CreatedAt: models.FromTime(time.Now()),
		UpdatedAt: models.FromTime(time.Now()),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
	}

	event.Trigger(eventType.ClientCreated, event.M{
		"client": client,
	})
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
		CreatedAt: models.FromTime(time.Now()),
		UpdatedAt: models.FromTime(time.Now()),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
	}
	event.Trigger(eventType.ClientCreated, event.M{
		"client": client,
	})
	return clientUUID, token, nil
}

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
