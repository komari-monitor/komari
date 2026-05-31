package notifier

import (
	"fmt"
	"math"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/renewal"
)

func CheckExpireScheduledWork() {
	CheckExpire()
}

func CheckExpire() {
	cfg, err := config.GetMany(map[string]any{
		config.ExpireNotificationEnabledKey:  false,
		config.ExpireNotificationLeadDaysKey: 7,
	})
	if err != nil {
		return
	}

	clients_all, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return
	}

	checkTime := time.Now()

	// 过期提醒检查（仅当启用过期通知时）
	if cfg[config.ExpireNotificationEnabledKey].(bool) {
		notificationLeadDays := int(cfg[config.ExpireNotificationLeadDaysKey].(float64)) // Json unmarshal 会将数字解析为 float64

		type clientToExpireInfo struct {
			Name     string
			DaysLeft int
		}

		var clientLeadToExpire []clientToExpireInfo

		for _, client := range clients_all {
			clientExpireTime := client.ExpiredAt.ToTime()

			if clientExpireTime.Before(checkTime) {
				continue
			}

			notificationThreshold := checkTime.Add(time.Duration(notificationLeadDays) * 24 * time.Hour)

			if clientExpireTime.Before(notificationThreshold) || clientExpireTime.Equal(notificationThreshold) {
				remainingDuration := clientExpireTime.Sub(checkTime)
				daysLeft := int(math.Ceil(remainingDuration.Hours() / 24))

				clientLeadToExpire = append(clientLeadToExpire, clientToExpireInfo{
					Name:     client.Name,
					DaysLeft: daysLeft,
				})
			}
		}

		if len(clientLeadToExpire) > 0 {
			message := ""
			for _, clientInfo := range clientLeadToExpire {
				message += fmt.Sprintf("• %s (%dd)\n", clientInfo.Name, clientInfo.DaysLeft)
			}
			messageSender.SendEvent(models.EventMessage{
				Event:   messageevent.Expire,
				Time:    time.Now(),
				Message: message,
				Emoji:   "⏳",
			})
		}
	}

	for _, client := range clients_all {
		renewal.CheckAndAutoRenewal(client)
	}
}
