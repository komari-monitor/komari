package renewal

import (
	"fmt"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/utils/messageSender"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

func CheckAndAutoRenewal(client models.Client) {
	// 自动续费检查
	//type renewedClient struct {
	//	Name          string
	//	NewExpireTime time.Time
	//}
	//var renewedClients []renewedClient

	if !client.AutoRenewal {
		return
	}
	// 不在线则不续费
	if _, ok := agent_runtime.GetConnectedClients()[client.UUID]; !ok {
		return
	}

	clientExpireTime := client.ExpiredAt.ToTime()
	checkTime := time.Now()

	// 如果到期时间小于0002年，跳过
	if clientExpireTime.Year() < 2 {
		return
	}

	// 检查是否已过期或当天过期
	if clientExpireTime.Before(checkTime) || clientExpireTime.Format("2006-01-02") == checkTime.Format("2006-01-02") {
		// 计算过期时间距离创建时间的总天数，判断是否为长期账单
		now := time.Now()
		hundredYearsFromNow := now.AddDate(100, 0, 0)

		// 如果过期时间超过当前时间100年，视为长期/一次性账单，不续费
		if clientExpireTime.After(hundredYearsFromNow) {
			return
		}

		// 如果有账单周期且不为0，进行自动续费
		if client.BillingCycle > 0 {
			// 根据账单周期计算新的过期时间
			var newExpireTime time.Time
			billingCycle := client.BillingCycle

			// 如果服务器的过期时间太早了，那么直接设置为从当前时间算的下一个到期时间
			baseTime := clientExpireTime
			if clientExpireTime.Before(now.AddDate(0, 0, -30)) { // 过期时间超过30天前
				baseTime = now
			}

			if billingCycle >= 27 && billingCycle <= 32 {
				// 月度计费 - 加1个月
				newExpireTime = baseTime.AddDate(0, 1, 0)
			} else if billingCycle >= 87 && billingCycle <= 95 {
				// 季度计费 - 加3个月
				newExpireTime = baseTime.AddDate(0, 3, 0)
			} else if billingCycle >= 175 && billingCycle <= 185 {
				// 半年计费 - 加6个月
				newExpireTime = baseTime.AddDate(0, 6, 0)
			} else if billingCycle >= 360 && billingCycle <= 370 {
				// 年度计费 - 加1年
				newExpireTime = baseTime.AddDate(1, 0, 0)
			} else if billingCycle >= 720 && billingCycle <= 750 {
				// 两年计费 - 加2年
				newExpireTime = baseTime.AddDate(2, 0, 0)
			} else if billingCycle >= 1080 && billingCycle <= 1150 {
				// 三年计费 - 加3年
				newExpireTime = baseTime.AddDate(3, 0, 0)
			} else if billingCycle >= 1800 && billingCycle <= 1850 {
				// 五年计费 - 加5年
				newExpireTime = baseTime.AddDate(5, 0, 0)
			} else {
				// 其他情况，直接加上账单周期天数
				newExpireTime = baseTime.AddDate(0, 0, billingCycle)
			}

			// 更新客户端过期时间
			updates := map[string]interface{}{
				"uuid":       client.UUID,
				"expired_at": models.FromTime(newExpireTime),
			}

			err := clients.SaveClient(updates)
			if err != nil {
				auditlog.EventLog("renewal", fmt.Sprintf("Failed to renew client %s (%s): %v", client.Name, client.UUID, err))
				return
			}

			//renewedClients = append(renewedClients, renewedClient{
			//	Name:          client.Name,
			//	NewExpireTime: newExpireTime,
			//})

			auditlog.EventLog("renewal", fmt.Sprintf("Auto-renewed client: %s until %s",
				client.Name, newExpireTime.Format("2006-01-02")))

			messageSender.SendEvent(models.EventMessage{
				Event:   messageevent.Renew,
				Clients: []models.Client{client},
				Time:    time.Now(),
				Emoji:   "🔄",
				Message: fmt.Sprintf("• %s until %s\n", client.Name, newExpireTime.Format("2006-01-02")),
			})
		}
	}

	// 发送续费通知
	// if len(renewedClients) > 0 {
	// 	message := ""
	// 	for _, clientInfo := range renewedClients {
	// 		message += fmt.Sprintf("• %s until %s\n", clientInfo.Name, clientInfo.NewExpireTime.Format("2006-01-02"))
	// 	}
	// 	messageSender.SendEvent(models.EventMessage{
	// 		Event:   messageevent.Renew,
	// 		Clients: []models.Client{client},
	// 		Time:    time.Now(),
	// 		Emoji:   "🔄",
	// 		Message: message,
	// 	})
	// }
}
