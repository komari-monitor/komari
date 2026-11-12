package renewal

import (
	"fmt"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/pkg/timeutil"
	"github.com/komari-monitor/komari/utils/messageSender"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

func CheckAndAutoRenewal(client models.Client) {
	if !client.AutoRenewal {
		return
	}
	if _, ok := agent_runtime.GetConnectedClients()[client.UUID]; !ok {
		return
	}
	if client.ExpiredAt == nil {
		return
	}

	clientExpireTime := client.ExpiredAt.UTC()
	checkTime := time.Now().UTC()

	if clientExpireTime.Year() < 2 {
		return
	}

	if clientExpireTime.Before(checkTime) || timeutil.SameSystemDate(clientExpireTime, checkTime) {
		localNow := checkTime.In(time.Local)
		if clientExpireTime.After(localNow.AddDate(100, 0, 0).UTC()) {
			return
		}

		if client.BillingCycle > 0 {
			var newExpireTime time.Time
			billingCycle := client.BillingCycle

			baseTime := clientExpireTime.In(time.Local)
			if clientExpireTime.Before(localNow.AddDate(0, 0, -30).UTC()) {
				baseTime = localNow
			}

			switch {
			case billingCycle >= 27 && billingCycle <= 32:
				newExpireTime = addMonths(baseTime, 1)
			case billingCycle >= 87 && billingCycle <= 95:
				newExpireTime = addMonths(baseTime, 3)
			case billingCycle >= 175 && billingCycle <= 185:
				newExpireTime = addMonths(baseTime, 6)
			case billingCycle >= 360 && billingCycle <= 370:
				newExpireTime = addMonths(baseTime, 12)
			case billingCycle >= 720 && billingCycle <= 750:
				newExpireTime = addMonths(baseTime, 24)
			case billingCycle >= 1080 && billingCycle <= 1150:
				newExpireTime = addMonths(baseTime, 36)
			case billingCycle >= 1800 && billingCycle <= 1850:
				newExpireTime = addMonths(baseTime, 60)
			default:
				newExpireTime = baseTime.AddDate(0, 0, billingCycle)
			}

			updates := map[string]interface{}{
				"uuid":       client.UUID,
				"expired_at": newExpireTime.UTC(),
			}

			if err := clients.SaveClient(updates); err != nil {
				auditlog.EventLog("renewal", fmt.Sprintf("Failed to renew client %s (%s): %v", client.Name, client.UUID, err))
				return
			}

			auditlog.EventLog("renewal", fmt.Sprintf("Auto-renewed client: %s until %s",
				client.Name, timeutil.FormatSystemDate(newExpireTime)))

			_ = messageSender.SendNotification(models.EventMessage{
				Event:   messageevent.Renew,
				Clients: []models.Client{client},
				Time:    time.Now().UTC(),
				Emoji:   "🔄",
				Message: fmt.Sprintf("• %s until %s\n", client.Name, timeutil.FormatSystemDate(newExpireTime)),
			})
		}
	}
}

// addMonths adds n months and keeps the day within the target month, so a
// renewal due on Oct 31 moves to Nov 30, not Dec 1. time.AddDate would carry
// the overflow into the next month and shift every later renewal.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, n, 0)
	lastDay := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(t.Day(), lastDay)-1)
}
