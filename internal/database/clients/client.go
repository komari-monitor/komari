package clients

import (
	"math"
	"time"

	"github.com/komari-monitor/komari/internal/common"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"fmt"

	"github.com/google/uuid"
)

// Deprecated: DeleteClientConfig is deprecated and will be removed in a future release. Use DeleteClient instead.
func DeleteClientConfig(clientUuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Delete(&common.ClientConfig{ClientUUID: clientUuid}).Error
	if err != nil {
		return err
	}
	return nil
}
func DeleteClient(clientUuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Delete(&models.Client{}, "uuid = ?", clientUuid).Error
	if err != nil {
		return err
	}
	return nil
}

// Deprecated: UpdateOrInsertBasicInfo is deprecated and will be removed in a future release. Use SaveClientInfo instead.
func UpdateOrInsertBasicInfo(cbi common.ClientInfo) error {
	db := dbcore.GetDBInstance()
	updates := make(map[string]interface{})

	if cbi.Name != "" {
		updates["name"] = cbi.Name
	}
	if cbi.CpuName != "" {
		updates["cpu_name"] = cbi.CpuName
	}
	if cbi.Arch != "" {
		updates["arch"] = cbi.Arch
	}
	if cbi.CpuCores > 0 || cbi.CpuCores < math.MaxInt-1 {
		updates["cpu_cores"] = cbi.CpuCores
	}
	if cbi.OS != "" {
		updates["os"] = cbi.OS
	}
	if cbi.GpuName != "" {
		updates["gpu_name"] = cbi.GpuName
	}
	if cbi.IPv4 != "" {
		updates["ipv4"] = cbi.IPv4
	}
	if cbi.IPv6 != "" {
		updates["ipv6"] = cbi.IPv6
	}
	if cbi.Region != "" {
		updates["region"] = cbi.Region
	}
	if cbi.Remark != "" {
		updates["remark"] = cbi.Remark
	}
	updates["mem_total"] = cbi.MemTotal
	updates["swap_total"] = cbi.SwapTotal
	updates["disk_total"] = cbi.DiskTotal
	updates["version"] = cbi.Version
	updates["updated_at"] = time.Now()

	// 转换为更新Client表
	client := models.Client{
		UUID: cbi.UUID,
	}

	err := db.Model(&client).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "uuid"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(map[string]interface{}{
		"uuid":       cbi.UUID,
		"name":       cbi.Name,
		"cpu_name":   cbi.CpuName,
		"arch":       cbi.Arch,
		"cpu_cores":  cbi.CpuCores,
		"os":         cbi.OS,
		"gpu_name":   cbi.GpuName,
		"ipv4":       cbi.IPv4,
		"ipv6":       cbi.IPv6,
		"region":     cbi.Region,
		"remark":     cbi.Remark,
		"mem_total":  cbi.MemTotal,
		"swap_total": cbi.SwapTotal,
		"disk_total": cbi.DiskTotal,
		"version":    cbi.Version,
		"updated_at": time.Now(),
	}).Error

	if err != nil {
		return err
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

	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(update).Error
	if err != nil {
		return err
	}
	return nil
}

// 更新客户端设置
func UpdateClientConfig(config common.ClientConfig) error {
	db := dbcore.GetDBInstance()
	err := db.Save(&config).Error
	if err != nil {
		return err
	}
	return nil
}

func EditClientName(clientUUID, clientName string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Update("name", clientName).Error
	if err != nil {
		return err
	}
	return nil
}

/*
// UpdateClientByUUID 更新指定 UUID 的客户端配置

	func UpdateClientByUUID(config common.ClientConfig) error {
		db := dbcore.GetDBInstance()
		result := db.Model(&common.ClientConfig{}).Where("client_uuid = ?", config.ClientUUID).Updates(config)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	}
*/
func EditClientToken(clientUUID, token string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Update("token", token).Error
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
		CreatedAt: models.FromTime(time.Now()),
		UpdatedAt: models.FromTime(time.Now()),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
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
		CreatedAt: models.FromTime(time.Now()),
		UpdatedAt: models.FromTime(time.Now()),
	}

	err = db.Create(&client).Error
	if err != nil {
		return "", "", err
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

// GetClientBasicInfo 获取指定 UUID 的客户端基本信息
func GetClientBasicInfo(uuid string) (client models.Client, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&client).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return models.Client{}, fmt.Errorf("客户端不存在: %s", uuid)
		}
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

	updates["updated_at"] = time.Now()

	err := db.Model(&models.Client{}).Where("uuid = ?", clientUUID).Updates(updates).Error
	if err != nil {
		return err
	}
	return nil
}
