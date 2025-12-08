package patch

import (
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	// 1.1.4 迁移配置表 - 在 ProcessStart 事件之前执行，在数据库初始化前进行
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		v1_1_4_PreMigration()
		return nil
	}), event.Max+4) // Max+4 在备份恢复之后，配置加载之前
	// 1.1.5 迁移 compact.nezha 到 extensions.nezha

	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
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
		// 1.1.4 清理配置表
		if db.Migrator().HasTable(&conf.V1Struct{}) {
			v1_1_4(db)
		}
		return nil
	}), event.Max)
}
