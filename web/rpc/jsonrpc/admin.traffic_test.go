package jsonrpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/trafficledger"
	"github.com/komari-monitor/komari/pkg/rpc"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

func TestTrafficAdminRPCConfigCalibrationAndReset(t *testing.T) {
	ctx := testAuditContext("192.0.2.45", "traffic-admin")
	if err := trafficledger.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize traffic ledger: %v", err)
	}
	uuid, _, err := clients.CreateClientWithName("Traffic Test Node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		agent_runtime.DeleteLatestReport(uuid)
		_ = trafficledger.DeleteClient(uuid)
		_ = clients.DeleteClient(uuid)
	})

	if _, rpcErr := adminUpdateClientTrafficConfig(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:updateClientTrafficConfig",
		Params: map[string]any{
			"uuid":               uuid,
			"mode":               "monthly",
			"reset_day":          31,
			"interval_days":      30,
			"timezone":           "Asia/Tokyo",
			"traffic_limit":      int64(8 << 30),
			"traffic_limit_type": "sum",
		},
	}); rpcErr != nil {
		t.Fatalf("update traffic config: %v", rpcErr)
	}

	if _, rpcErr := adminCalibrateClientTraffic(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:calibrateClientTraffic",
		Params: map[string]any{
			"uuid":        uuid,
			"target_up":   int64(3 << 30),
			"target_down": int64(2 << 30),
			"note":        "vendor panel correction",
		},
	}); rpcErr != nil {
		t.Fatalf("calibrate traffic: %v", rpcErr)
	}
	snapshot, ok := trafficledger.Get(uuid)
	if !ok || snapshot.Mode != trafficledger.ModeMonthly {
		t.Fatalf("ledger snapshot missing or wrong mode: %#v", snapshot)
	}
	if snapshot.EffectiveUp != 3<<30 || snapshot.EffectiveDown != 2<<30 {
		t.Fatalf("effective usage = %d/%d, want %d/%d", snapshot.EffectiveUp, snapshot.EffectiveDown, 3<<30, 2<<30)
	}

	auditBeforeGet := latestAuditLog(t)
	result, rpcErr := adminGetClientTraffic(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientTraffic",
		Params:  map[string]any{"uuid": uuid},
	})
	if rpcErr != nil {
		t.Fatalf("get traffic config: %v", rpcErr)
	}
	response, ok := result.(clientTrafficResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", result)
	}
	if response.Mode != "monthly" || response.TrafficLimitType != "sum" || response.EffectiveUp != 3<<30 {
		t.Fatalf("unexpected traffic response: %+v", response)
	}
	if auditAfterGet := latestAuditLog(t); auditAfterGet.ID != auditBeforeGet.ID {
		t.Fatalf("GET traffic unexpectedly created an audit log entry: %+v", auditAfterGet)
	}

	if _, rpcErr := adminResetClientTrafficCycle(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:resetClientTrafficCycle",
		Params:  map[string]any{"uuid": uuid, "note": "test reset"},
	}); rpcErr != nil {
		t.Fatalf("reset traffic cycle: %v", rpcErr)
	}
	snapshot, ok = trafficledger.Get(uuid)
	if !ok || snapshot.EffectiveUp != 0 || snapshot.EffectiveDown != 0 || snapshot.OffsetUp != 0 || snapshot.OffsetDown != 0 {
		t.Fatalf("traffic cycle was not cleared: %#v", snapshot)
	}
	if entry := latestAuditLog(t); !strings.Contains(entry.Message, "Reset client traffic cycle") || entry.UUID != "traffic-admin" || entry.IP != "192.0.2.45" {
		t.Fatalf("reset audit log lacks action or actor metadata: %#v", entry)
	}
}

func TestTrafficAdminRPCAnchorDateUsesConfiguredTimezone(t *testing.T) {
	ctx := testAuditContext("192.0.2.48", "traffic-admin")
	if err := trafficledger.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize traffic ledger: %v", err)
	}
	uuid, _, err := clients.CreateClientWithName("Traffic Anchor Date Node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		_ = trafficledger.DeleteClient(uuid)
		_ = clients.DeleteClient(uuid)
	})

	if _, rpcErr := adminUpdateClientTrafficConfig(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:updateClientTrafficConfig",
		Params: map[string]any{
			"uuid":          uuid,
			"mode":          "fixed_days",
			"reset_day":     1,
			"interval_days": 30,
			"anchor_date":   "2026-11-15",
			"timezone":      "Asia/Shanghai",
		},
	}); rpcErr != nil {
		t.Fatalf("update fixed_days with anchor_date: %v", rpcErr)
	}

	result, rpcErr := adminGetClientTraffic(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientTraffic",
		Params:  map[string]any{"uuid": uuid},
	})
	if rpcErr != nil {
		t.Fatalf("get traffic: %v", rpcErr)
	}
	resp := result.(clientTrafficResponse)
	wantAnchor := time.Date(2026, 11, 14, 16, 0, 0, 0, time.UTC) // 2026-11-15 00:00 +08:00
	if resp.AnchorAt == nil || !resp.AnchorAt.Equal(wantAnchor) || resp.AnchorDate != "2026-11-15" {
		t.Fatalf("anchor = %v (date=%q), want %v / 2026-11-15", resp.AnchorAt, resp.AnchorDate, wantAnchor)
	}
}

func TestTrafficLedgerGETUsesLiveAgentCounterInCompatibilityMode(t *testing.T) {
	if err := trafficledger.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize traffic ledger: %v", err)
	}
	uuid, _, err := clients.CreateClientWithName("Traffic Compatibility Node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		agent_runtime.DeleteLatestReport(uuid)
		_ = trafficledger.DeleteClient(uuid)
		_ = clients.DeleteClient(uuid)
	})
	agent_runtime.RecordReport(v2.Report{
		UUID:      uuid,
		UpdatedAt: time.Now().UTC(),
		Network:   v2.NetworkReport{TotalUp: 123456, TotalDown: 654321},
	})
	result, rpcErr := adminGetClientTraffic(testAuditContext("192.0.2.47", "traffic-admin"), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientTraffic",
		Params:  map[string]any{"uuid": uuid},
	})
	if rpcErr != nil {
		t.Fatalf("get traffic: %v", rpcErr)
	}
	resp := result.(clientTrafficResponse)
	if resp.EffectiveUp != 123456 || resp.EffectiveDown != 654321 {
		t.Fatalf("effective traffic did not use the live agent report: %+v", resp)
	}
}

func TestTrafficLedgerGETPreservesPersistedRawWhenOffline(t *testing.T) {
	ctx := testAuditContext("192.0.2.49", "traffic-admin")
	if err := trafficledger.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize traffic ledger: %v", err)
	}
	uuid, _, err := clients.CreateClientWithName("Traffic Offline Node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		agent_runtime.DeleteLatestReport(uuid)
		_ = trafficledger.DeleteClient(uuid)
		_ = clients.DeleteClient(uuid)
	})
	agent_runtime.RecordReport(v2.Report{
		UUID:      uuid,
		UpdatedAt: time.Now().UTC(),
		Uptime:    1000,
		Network:   v2.NetworkReport{TotalUp: 9000, TotalDown: 8000},
	})
	if _, rpcErr := adminUpdateClientTrafficConfig(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:updateClientTrafficConfig",
		Params:  map[string]any{"uuid": uuid, "mode": "monthly", "reset_day": 1, "interval_days": 30},
	}); rpcErr != nil {
		t.Fatalf("enable monthly: %v", rpcErr)
	}
	// Simulate node going offline / no live report in memory.
	agent_runtime.DeleteLatestReport(uuid)
	result, rpcErr := adminGetClientTraffic(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientTraffic",
		Params:  map[string]any{"uuid": uuid},
	})
	if rpcErr != nil {
		t.Fatalf("get offline traffic: %v", rpcErr)
	}
	resp := result.(clientTrafficResponse)
	if resp.RawLastUp != 9000 || resp.RawLastDown != 8000 {
		t.Fatalf("offline GET overwrote persisted raw counters: %+v", resp)
	}
}

func TestTrafficDefaultTimezoneRejectsInvalidIANAZone(t *testing.T) {
	_, rpcErr := adminEditSettings(testAuditContext("192.0.2.46", "traffic-admin"), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editSettings",
		Params: map[string]any{
			"traffic_default_timezone": "Mars/Olympus_Mons",
		},
	})
	if rpcErr == nil || rpcErr.Code != rpc.InvalidParams {
		t.Fatalf("invalid timezone error = %#v, want InvalidParams", rpcErr)
	}
}

func TestTrafficAdminRPCRequiresAdmin(t *testing.T) {
	response := Dispatch(context.Background(), &rpc.ContextMeta{Permission: rpc.RoleGuest}, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:getClientTraffic",
		Params:  map[string]any{"uuid": "missing"},
	})
	if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
		t.Fatalf("guest traffic RPC error = %#v, want permission denied", response.Error)
	}
}

func TestTrafficLedgerModelPersistsUTCInstants(t *testing.T) {
	if err := dbcore.GetDBInstance().AutoMigrate(&models.ClientTrafficLedger{}); err != nil {
		t.Fatalf("migrate ledger model: %v", err)
	}
	stamp := time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC)
	row := models.ClientTrafficLedger{UUID: "traffic-ledger-model-test", Mode: trafficledger.ModeManual, CycleStartAt: &stamp}
	db := dbcore.GetDBInstance()
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create ledger row: %v", err)
	}
	t.Cleanup(func() { db.Delete(&models.ClientTrafficLedger{}, "uuid = ?", row.UUID) })
	var loaded models.ClientTrafficLedger
	if err := db.First(&loaded, "uuid = ?", row.UUID).Error; err != nil {
		t.Fatalf("load ledger row: %v", err)
	}
	if loaded.CycleStartAt == nil || !loaded.CycleStartAt.Equal(stamp) {
		t.Fatalf("cycle start = %v, want %v", loaded.CycleStartAt, stamp)
	}
}

func TestParseMonthRotate(t *testing.T) {
	cases := []struct {
		input   any
		enabled bool
		day     int
	}{
		{0, false, 0},
		{float64(15), true, 15},
		{31, true, 31},
		{32, true, 0},
		{" 7 ", true, 7},
		{json.Number("20"), true, 20},
		{-1, false, 0},
		{"bad", false, 0},
		{nil, false, 0},
	}
	for _, tc := range cases {
		enabled, day := parseMonthRotate(tc.input)
		if enabled != tc.enabled || day != tc.day {
			t.Fatalf("parseMonthRotate(%#v) = (%v, %d), want (%v, %d)", tc.input, enabled, day, tc.enabled, tc.day)
		}
	}
}

func TestTrafficAdminRPCPreserveCurrentUsage(t *testing.T) {
	ctx := testAuditContext("192.0.2.50", "traffic-admin")
	if err := trafficledger.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize traffic ledger: %v", err)
	}
	uuid, _, err := clients.CreateClientWithName("Traffic Preserve Node")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	t.Cleanup(func() {
		agent_runtime.DeleteLatestReport(uuid)
		_ = trafficledger.DeleteClient(uuid)
		_ = clients.DeleteClient(uuid)
	})
	agent_runtime.RecordReport(v2.Report{
		UUID:      uuid,
		UpdatedAt: time.Now().UTC(),
		Uptime:    3600,
		Network:   v2.NetworkReport{TotalUp: 2 << 30, TotalDown: 5 << 30},
	})

	if _, rpcErr := adminUpdateClientTrafficConfig(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:updateClientTrafficConfig",
		Params: map[string]any{
			"uuid":                   uuid,
			"mode":                   "monthly",
			"reset_day":              15,
			"interval_days":          30,
			"preserve_current_usage": true,
		},
	}); rpcErr != nil {
		t.Fatalf("update with preserve_current_usage: %v", rpcErr)
	}

	snap, ok := trafficledger.Get(uuid)
	if !ok || snap.OffsetUp != 2<<30 || snap.OffsetDown != 5<<30 || snap.EffectiveUp != 2<<30 || snap.EffectiveDown != 5<<30 {
		t.Fatalf("preserved ledger state = %+v", snap)
	}
	if entry := latestAuditLog(t); !strings.Contains(entry.Message, "initial_offset=") {
		t.Fatalf("audit log missing initial_offset: %#v", entry)
	}
}
