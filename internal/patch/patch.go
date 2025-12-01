package patch

import (
	"log"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
)

func Apply() {

	db := dbcore.GetDBInstance()
	// 0.0.5 迁移ClientInfo
	if db.Migrator().HasTable("client_infos") {
		v0_0_5(db)
	}
	// 0.0.5a 修正cors拼写错误
	if db.Migrator().HasColumn(&conf.V1Struct{}, "allow_cros") {
		v0_0_5a(db)
	}
	// 0.1.4 重建LoadNotification表
	if db.Migrator().HasColumn(&models.LoadNotification{}, "client") {
		log.Println("[>0.1.4] Rebuilding LoadNotification table....")
		db.Migrator().DropTable(&models.LoadNotification{})
	}
	// 1.0.2 合并OIDC提供商表
	if !db.Migrator().HasTable(&models.OidcProvider{}) && db.Migrator().HasTable(&conf.V1Struct{}) {
		v1_0_2_Oidc(db)
	}
	// 1.0.2 迁移消息发送配置到单独的表
	if !db.Migrator().HasTable(&models.MessageSenderProvider{}) && db.Migrator().HasTable(&conf.V1Struct{}) {
		v1_0_2_MessageSender(db)
	}
	// 1.1.4 迁移配置表
	if db.Migrator().HasColumn(&conf.V1Struct{}, "id") {
		v1_1_4(db)
	}
}
