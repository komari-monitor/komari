package clients

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/komari-monitor/komari/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const billingPersistInterval = 30 * time.Second

var (
	billingUsageMu       sync.Mutex
	billingLastPersisted = make(map[string]time.Time)
)

// UpdateBillingUsage records the counters needed for a live cost estimate. It
// deliberately stores usage, not invoice rows: changing a rate recalculates the
// estimate and does not rewrite a historical ledger.
func UpdateBillingUsage(uuid string, report v1.Report) error {
	if err := ReportVerify(report); err != nil {
		return err
	}

	billingUsageMu.Lock()
	defer billingUsageMu.Unlock()
	if lastPersisted, ok := billingLastPersisted[uuid]; ok {
		elapsed := report.UpdatedAt.Sub(lastPersisted)
		if elapsed >= 0 && elapsed < billingPersistInterval {
			return nil
		}
	}

	err := dbcore.GetDBInstance().Transaction(func(tx *gorm.DB) error {
		var client models.Client
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("uuid = ?", uuid).First(&client).Error; err != nil {
			return fmt.Errorf("load client billing state: %w", err)
		}

		updates := calculateBillingUsageUpdates(client, report, report.UpdatedAt)
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&models.Client{}).Where("uuid = ?", uuid).Updates(updates).Error; err != nil {
			return fmt.Errorf("update client billing state: %w", err)
		}
		return nil
	})
	if err == nil {
		billingLastPersisted[uuid] = report.UpdatedAt
	}
	return err
}

func forgetBillingUsage(uuid string) {
	billingUsageMu.Lock()
	delete(billingLastPersisted, uuid)
	billingUsageMu.Unlock()
}

func calculateBillingUsageUpdates(client models.Client, report v1.Report, now time.Time) map[string]interface{} {
	if now.IsZero() {
		now = time.Now()
	}

	updates := map[string]interface{}{
		"billing_last_total_up":        report.Network.TotalUp,
		"billing_last_total_down":      report.Network.TotalDown,
		"billing_traffic_baseline_set": true,
	}

	if client.FirstAgentReportedAt.ToTime().IsZero() {
		updates["first_agent_reported_at"] = models.FromTime(now)
		updates["first_agent_reported_at_estimated"] = false
	}

	if client.ExpiredAt.ToTime().IsZero() {
		updates["expired_at"] = models.FromTime(nextNaturalMonth(now))
	}

	// The first report establishes a baseline. Existing OS counters may include
	// traffic from before Komari was installed and must not be charged.
	if !client.BillingTrafficBaselineSet {
		return updates
	}

	deltaUp := utils.ComputeTrafficDelta(report.Network.TotalUp, client.BillingLastTotalUp)
	deltaDown := utils.ComputeTrafficDelta(report.Network.TotalDown, client.BillingLastTotalDown)
	updates["billing_traffic_bytes"] = saturatingAdd(client.BillingTrafficBytes, deltaUp, deltaDown)
	return updates
}

func nextNaturalMonth(value time.Time) time.Time {
	firstOfNextMonth := time.Date(value.Year(), value.Month()+1, 1, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location())
	lastOfNextMonth := firstOfNextMonth.AddDate(0, 1, -1).Day()
	day := value.Day()
	if day > lastOfNextMonth {
		day = lastOfNextMonth
	}
	return time.Date(firstOfNextMonth.Year(), firstOfNextMonth.Month(), day, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location())
}

func saturatingAdd(base int64, values ...int64) int64 {
	if base < 0 {
		base = 0
	}
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if value > math.MaxInt64-base {
			return math.MaxInt64
		}
		base += value
	}
	return base
}
