package clients

import (
	"math"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateBillingUsageUpdatesInitializesFirstReport(t *testing.T) {
	now := time.Date(2026, time.January, 31, 12, 0, 0, 0, time.UTC)
	report := v1.Report{UpdatedAt: now}
	report.Network.TotalUp = 100
	report.Network.TotalDown = 200

	updates := calculateBillingUsageUpdates(models.Client{}, report, now)

	assert.Equal(t, models.FromTime(now), updates["first_agent_reported_at"])
	assert.Equal(t, models.FromTime(time.Date(2026, time.February, 28, 12, 0, 0, 0, time.UTC)), updates["expired_at"])
	assert.Equal(t, true, updates["billing_startup_fee_applied"])
	assert.Equal(t, int64(100), updates["billing_last_total_up"])
	assert.Equal(t, int64(200), updates["billing_last_total_down"])
	_, hasTraffic := updates["billing_traffic_bytes"]
	assert.False(t, hasTraffic)
}

func TestCalculateBillingUsageUpdatesPreservesEstimatedAnchor(t *testing.T) {
	createdAt := time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	client := models.Client{
		FirstAgentReportedAt:          models.FromTime(createdAt),
		FirstAgentReportedAtEstimated: true,
	}
	report := v1.Report{UpdatedAt: now}

	updates := calculateBillingUsageUpdates(client, report, now)

	_, overwroteAnchor := updates["first_agent_reported_at"]
	assert.False(t, overwroteAnchor)
	assert.Equal(t, models.FromTime(now.AddDate(0, 1, 0)), updates["expired_at"])
	assert.Equal(t, true, updates["billing_startup_fee_applied"])
}

func TestCalculateBillingUsageUpdatesKeepsAppliedStartupFee(t *testing.T) {
	now := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	client := models.Client{
		FirstAgentReportedAt:     models.FromTime(now.Add(-time.Hour)),
		BillingStartupFeeApplied: true,
	}

	updates := calculateBillingUsageUpdates(client, v1.Report{UpdatedAt: now}, now)

	_, rewroteStartupFeeState := updates["billing_startup_fee_applied"]
	assert.False(t, rewroteStartupFeeState)
}

func TestCalculateBillingUsageUpdatesHandlesCounterReset(t *testing.T) {
	now := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	client := models.Client{
		FirstAgentReportedAt:      models.FromTime(now.Add(-time.Hour)),
		BillingTrafficBaselineSet: true,
		BillingTrafficBytes:       500,
		BillingLastTotalUp:        1000,
		BillingLastTotalDown:      2000,
	}
	report := v1.Report{UpdatedAt: now}
	report.Network.TotalUp = 25
	report.Network.TotalDown = 40

	updates := calculateBillingUsageUpdates(client, report, now)

	assert.Equal(t, int64(565), updates["billing_traffic_bytes"])
}

func TestSaturatingAddPreventsOverflow(t *testing.T) {
	result := saturatingAdd(math.MaxInt64-2, 1, 5)
	require.Equal(t, int64(math.MaxInt64), result)
}
