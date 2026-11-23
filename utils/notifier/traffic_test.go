package notifier

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/trafficledger"
	cache "github.com/patrickmn/go-cache"
)

func TestTrafficAlertTTLTracksServerCycle(t *testing.T) {
	end := time.Now().Add(6 * time.Hour)
	if got := trafficAlertTTL(trafficledger.ModeMonthly, &end); got < 6*time.Hour || got > 6*time.Hour+time.Minute+time.Second {
		t.Fatalf("monthly alert TTL = %s, want about 6h", got)
	}
	if got := trafficAlertTTL(trafficledger.ModeManual, nil); got != 0 {
		t.Fatalf("manual alert TTL = %s, want no expiration", got)
	}
	if got := trafficAlertTTL(trafficledger.ModeAgent, nil); got != cache.DefaultExpiration {
		t.Fatalf("agent alert TTL = %s, want compatibility expiration %s", got, cache.DefaultExpiration)
	}
}

func TestSyncAndResetTrafficAlertState(t *testing.T) {
	uuid := "traffic-alert-cache-test"
	SyncTrafficAlertStep(uuid, "cycle-1", 85, time.Hour)
	if value, ok := trafficCache.Get(trafficAlertCacheKey(uuid, "cycle-1")); !ok || value != 85 {
		t.Fatalf("synced alert step = %v, %t, want 85", value, ok)
	}
	SyncTrafficAlertStep(uuid, "cycle-2", 90, time.Hour)
	ResetTrafficAlertState(uuid)
	if _, ok := trafficCache.Get(trafficAlertCacheKey(uuid, "cycle-1")); ok {
		t.Fatal("reset left cycle-1 alert state")
	}
	if _, ok := trafficCache.Get(trafficAlertCacheKey(uuid, "cycle-2")); ok {
		t.Fatal("reset left cycle-2 alert state")
	}
}

func TestTrafficAlertStepCalculation(t *testing.T) {
	if step, reached := trafficAlertStep(79, 100, 80); reached || step != 0 {
		t.Fatalf("below threshold = (%d, %v), want (0, false)", step, reached)
	}
	if step, reached := trafficAlertStep(83, 100, 83); !reached || step != 85 {
		t.Fatalf("83%% at 83%% threshold = (%d, %v), want (85, true)", step, reached)
	}
	if step, reached := trafficAlertStep(92, 100, 80); !reached || step != 90 {
		t.Fatalf("92%% at 80%% threshold = (%d, %v), want (90, true)", step, reached)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := time.Now().UTC().Add(24 * time.Hour)
	c := models.Client{UUID: "sync-helper-test", TrafficLimit: 100, TrafficLimitType: "sum"}
	t.Cleanup(func() { ResetTrafficAlertState(c.UUID) })
	syncTrafficAlertForClientWithThreshold(c, trafficledger.DisplayTraffic{
		Up: 45, Down: 42, Mode: trafficledger.ModeMonthly, CycleStartAt: &start, CycleEndAt: &end,
	}, 80.0)
	key := trafficAlertCacheKey(c.UUID, trafficCycleKey(trafficledger.DisplayTraffic{Mode: trafficledger.ModeMonthly, CycleStartAt: &start}))
	if value, ok := trafficCache.Get(key); !ok || value != 85 {
		t.Fatalf("synced step via helper = (%v, %v), want (85, true)", value, ok)
	}
}
