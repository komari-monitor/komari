package accounts

import (
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.ServerInitializeStart, event.ListenerFunc(func(e event.Event) error {
		var count int64 = 0
		if dbcore.GetDBInstance().Model(&models.User{}).Count(&count); count == 0 {
			user, passwd, err := CreateDefaultAdminAccount()
			if err != nil {
				return err
			}
			log.Println("Default admin account created. Username:", user, ", Password:", passwd)
		}
		return nil
	}))
}
