package notifier

import (
	"fmt"
	"math"
	"time"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/clients"
	"github.com/komari-monitor/komari/internal/database/models"
	messageevent "github.com/komari-monitor/komari/internal/database/models/messageEvent"
	"github.com/komari-monitor/komari/internal/messageSender"
	"github.com/komari-monitor/komari/internal/renewal"
)

func CheckExpireScheduledWork() {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location()) // UTC 9AM = CST 17PM
		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}
		duration := next.Sub(now)
		time.Sleep(duration)

		cfg, err := conf.GetWithV1Format()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		clients_all, err := clients.GetAllClientBasicInfo()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		checkTime := time.Now()

		// 过期提醒检查（仅当启用过期通知时）
		if cfg.ExpireNotificationEnabled {
			notificationLeadDays := cfg.ExpireNotificationLeadDays

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

		// 等待1秒，防止多次触发
		time.Sleep(time.Second)
		for _, client := range clients_all {
			renewal.CheckAndAutoRenewal(client)
		}
	}

}
