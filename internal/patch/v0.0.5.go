package patch

import (
	"log"

	"github.com/komari-monitor/komari/internal/common"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/models"
	"gorm.io/gorm"
)

func v0_0_5(db *gorm.DB) {
	log.Println("[>0.0.5] Legacy ClientInfo table detected, starting data migration...")
	var clientInfos []common.ClientInfo
	if err := db.Find(&clientInfos).Error; err != nil {
		log.Printf("Failed to read ClientInfo table: %v", err)
		return
	}

	for _, info := range clientInfos {
		var client models.Client
		if err := db.Where("uuid = ?", info.UUID).First(&client).Error; err != nil {
			log.Printf("Could not find Client record with UUID %s: %v", info.UUID, err)
			continue
		}

		// 更新Client记录
		client.Name = info.Name
		client.CpuName = info.CpuName
		client.Virtualization = info.Virtualization
		client.Arch = info.Arch
		client.CpuCores = info.CpuCores
		client.OS = info.OS
		client.GpuName = info.GpuName
		client.IPv4 = info.IPv4
		client.IPv6 = info.IPv6
		client.Region = info.Region
		client.Remark = info.Remark
		client.PublicRemark = info.PublicRemark
		client.MemTotal = info.MemTotal
		client.SwapTotal = info.SwapTotal
		client.DiskTotal = info.DiskTotal
		client.Version = info.Version
		client.Weight = info.Weight
		client.Price = info.Price
		client.BillingCycle = info.BillingCycle
		client.ExpiredAt = models.FromTime(info.ExpiredAt)
		// Save updated Client record
		if err := db.Save(&client).Error; err != nil {
			log.Printf("Failed to update Client record: %v", err)
			continue
		}
	}

	// Backup and rename old table after migration
	if err := db.Migrator().RenameTable("client_infos", "client_infos_backup"); err != nil {
		log.Printf("Failed to backup ClientInfo table: %v", err)
		return
	}
	log.Println("Data migration completed, old table has been backed up as client_infos_backup")
}

func v0_0_5a(db *gorm.DB) {
	log.Println("[>0.0.5a] Renaming column 'allow_cros' to 'allow_cors' in config table...")
	db.Migrator().RenameColumn(&conf.V1Struct{}, "allow_cros", "allow_cors")
}
