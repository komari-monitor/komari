package jsonrpc

import (
	"context"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func testAuditContext(ip, userUUID string) context.Context {
	return rpc.NewContextWithMeta(context.Background(), &rpc.ContextMeta{
		RemoteIP: ip,
		UserUUID: userUUID,
	})
}

func latestAuditLog(t *testing.T) models.Log {
	t.Helper()
	var entry models.Log
	if err := dbcore.GetDBInstance().Order("id desc").First(&entry).Error; err != nil {
		t.Fatalf("load latest audit log: %v", err)
	}
	return entry
}

func TestClientLifecycleAuditLogsIncludeNameAndChanges(t *testing.T) {
	db := dbcore.GetDBInstance()
	ctx := testAuditContext("192.0.2.10", "actor-admin-1")

	res, rpcErr := adminAddClient(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:addClient",
		Params:  map[string]any{"name": "SG-Node-01"},
	})
	if rpcErr != nil {
		t.Fatalf("adminAddClient failed: %v", rpcErr)
	}
	createdUUID := res.(map[string]any)["uuid"].(string)
	t.Cleanup(func() {
		db.Where("uuid = ?", createdUUID).Delete(&models.Client{})
	})

	createLog := latestAuditLog(t)
	wantCreate := "Create client: SG-Node-01(" + createdUUID + ")"
	if createLog.Message != wantCreate {
		t.Fatalf("create log = %q, want %q", createLog.Message, wantCreate)
	}

	if _, rpcErr := adminEditClient(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editClient",
		Params: map[string]any{
			"uuid":   createdUUID,
			"name":   "SG-Node-Renamed",
			"group":  "production",
			"region": "SG",
		},
	}); rpcErr != nil {
		t.Fatalf("adminEditClient failed: %v", rpcErr)
	}
	editLog := latestAuditLog(t)
	for _, want := range []string{
		"Edit client: SG-Node-Renamed(" + createdUUID + ")",
		`name: "SG-Node-01" -> "SG-Node-Renamed"`,
		`group: "" -> "production"`,
		`region: "" -> "SG"`,
	} {
		if !strings.Contains(editLog.Message, want) {
			t.Fatalf("edit log missing %q:\n%s", want, editLog.Message)
		}
	}

	if _, rpcErr := adminOrderClients(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:orderClients",
		Params:  map[string]any{createdUUID: 7},
	}); rpcErr != nil {
		t.Fatalf("adminOrderClients failed: %v", rpcErr)
	}
	orderLog := latestAuditLog(t)
	if !strings.Contains(orderLog.Message, "SG-Node-Renamed("+createdUUID+")=7") {
		t.Fatalf("order log missing client weight:\n%s", orderLog.Message)
	}

	if _, rpcErr := adminRemoveClient(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:removeClient",
		Params:  map[string]any{"uuid": createdUUID},
	}); rpcErr != nil {
		t.Fatalf("adminRemoveClient failed: %v", rpcErr)
	}
	deleteLog := latestAuditLog(t)
	for _, want := range []string{
		"Delete client: SG-Node-Renamed(" + createdUUID + ")",
		"group=production",
		"region=SG",
	} {
		if !strings.Contains(deleteLog.Message, want) {
			t.Fatalf("delete log missing %q:\n%s", want, deleteLog.Message)
		}
	}
}

func TestSettingsAndDBExecAuditLogsRetainContextAndRedactSecrets(t *testing.T) {
	ctx := testAuditContext("192.0.2.11", "actor-admin-2")

	if _, rpcErr := adminEditSettings(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editSettings",
		Params: map[string]any{
			"sitename": "Komari Audit Test",
			"api_key":  "secret-api-key-123456",
		},
	}); rpcErr != nil {
		t.Fatalf("adminEditSettings failed: %v", rpcErr)
	}
	settingsLog := latestAuditLog(t)
	if strings.Contains(settingsLog.Message, "secret-api-key-123456") {
		t.Fatalf("settings audit log leaked api_key:\n%s", settingsLog.Message)
	}
	for _, want := range []string{
		"Update settings:",
		"api_key: [REDACTED] (updated)",
		`"Komari Audit Test"`,
	} {
		if !strings.Contains(settingsLog.Message, want) {
			t.Fatalf("settings audit log missing %q:\n%s", want, settingsLog.Message)
		}
	}

	if _, rpcErr := adminDBExec(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:dbExec",
		Params: map[string]any{
			"database": "main",
			"sql":      "UPDATE clients SET weight = weight WHERE uuid = ?",
			"args":     []any{"non-existent-uuid"},
		},
	}); rpcErr != nil {
		t.Fatalf("adminDBExec failed: %v", rpcErr)
	}
	sqlLog := latestAuditLog(t)
	for _, want := range []string{
		"Execute database SQL: target=main, driver=sqlite, rows_affected=0, args=1",
		"SQL: UPDATE clients SET weight = weight WHERE uuid = ?",
	} {
		if !strings.Contains(sqlLog.Message, want) {
			t.Fatalf("dbExec audit log missing %q:\n%s", want, sqlLog.Message)
		}
	}
	if strings.Contains(sqlLog.Message, "non-existent-uuid") {
		t.Fatalf("dbExec audit log should not record raw positional args:\n%s", sqlLog.Message)
	}
}

func TestPingAndOfflineNotificationAuditLogsResolveClientNames(t *testing.T) {
	db := dbcore.GetDBInstance()
	ctx := testAuditContext("192.0.2.12", "actor-admin-3")

	const nodeUUID = "audit-ping-node-1"
	t.Cleanup(func() {
		db.Where("uuid = ?", nodeUUID).Delete(&models.Client{})
		db.Where("client = ?", nodeUUID).Delete(&models.OfflineNotification{})
	})
	db.Where("uuid = ?", nodeUUID).Delete(&models.Client{})
	if err := db.Create(&models.Client{
		UUID:  nodeUUID,
		Token: "tok-" + nodeUUID,
		Name:  "JP-Osaka",
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}

	res, rpcErr := adminAddPingTask(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:addPingTask",
		Params: map[string]any{
			"name":     "Cloudflare DNS",
			"target":   "1.1.1.1",
			"type":     "icmp",
			"interval": 60,
			"clients":  []string{nodeUUID},
		},
	})
	if rpcErr != nil {
		t.Fatalf("adminAddPingTask failed: %v", rpcErr)
	}
	taskID := res.(map[string]any)["task_id"].(uint)
	t.Cleanup(func() {
		db.Where("id = ?", taskID).Delete(&models.PingTask{})
	})

	addPingLog := latestAuditLog(t)
	for _, want := range []string{
		"Create ping task:",
		"Cloudflare DNS",
		"target=1.1.1.1",
		"JP-Osaka(" + nodeUUID + ")",
	} {
		if !strings.Contains(addPingLog.Message, want) {
			t.Fatalf("addPingTask log missing %q:\n%s", want, addPingLog.Message)
		}
	}

	if _, rpcErr := adminEditPingTask(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editPingTask",
		Params: map[string]any{
			"tasks": []map[string]any{
				{
					"id":       taskID,
					"name":     "Cloudflare DNS Updated",
					"target":   "1.0.0.1",
					"type":     "icmp",
					"interval": 30,
					"clients":  []string{nodeUUID},
				},
			},
		},
	}); rpcErr != nil {
		t.Fatalf("adminEditPingTask failed: %v", rpcErr)
	}
	editPingLog := latestAuditLog(t)
	for _, want := range []string{
		"Edit ping tasks: count=1",
		"Cloudflare DNS Updated",
		"target=1.0.0.1",
		"JP-Osaka(" + nodeUUID + ")",
	} {
		if !strings.Contains(editPingLog.Message, want) {
			t.Fatalf("editPingTask log missing %q:\n%s", want, editPingLog.Message)
		}
	}

	if _, rpcErr := adminEditOfflineNotification(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:editOfflineNotification",
		Params: []map[string]any{
			{"client": nodeUUID, "enable": true, "grace_period": 180},
		},
	}); rpcErr != nil {
		t.Fatalf("adminEditOfflineNotification failed: %v", rpcErr)
	}
	offlineLog := latestAuditLog(t)
	if !strings.Contains(offlineLog.Message, "JP-Osaka("+nodeUUID+") (enabled=true, grace=180s)") {
		t.Fatalf("editOfflineNotification log missing details:\n%s", offlineLog.Message)
	}

	if _, rpcErr := adminDisableOfflineNotification(ctx, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:disableOfflineNotification",
		Params:  []string{nodeUUID},
	}); rpcErr != nil {
		t.Fatalf("adminDisableOfflineNotification failed: %v", rpcErr)
	}
	disableLog := latestAuditLog(t)
	if !strings.Contains(disableLog.Message, "Disable offline notifications: JP-Osaka("+nodeUUID+")") {
		t.Fatalf("disableOfflineNotification log missing client label:\n%s", disableLog.Message)
	}
}
