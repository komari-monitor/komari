package auditlog

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:komari_auditlog_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

func TestFormatClientAndResolution(t *testing.T) {
	if got := FormatClient("Tokyo-01", "uuid-1"); got != "Tokyo-01(uuid-1)" {
		t.Fatalf("FormatClient = %q, want %q", got, "Tokyo-01(uuid-1)")
	}
	if got := FormatClient("", "uuid-1"); got != "uuid-1" {
		t.Fatalf("FormatClient without name = %q, want %q", got, "uuid-1")
	}
	if got := FormatClient("Tokyo-01", ""); got != "Tokyo-01" {
		t.Fatalf("FormatClient without uuid = %q, want %q", got, "Tokyo-01")
	}

	db := dbcore.GetDBInstance()
	t.Cleanup(func() {
		db.Where("uuid IN ?", []string{"audit-c1", "audit-c2"}).Delete(&models.Client{})
	})
	if err := db.Create(&models.Client{UUID: "audit-c1", Token: "tok-c1", Name: "东京节点"}).Error; err != nil {
		t.Fatalf("seed client 1: %v", err)
	}
	if err := db.Create(&models.Client{UUID: "audit-c2", Token: "tok-c2", Name: ""}).Error; err != nil {
		t.Fatalf("seed client 2: %v", err)
	}

	if got := FormatClientUUID("audit-c1"); got != "东京节点(audit-c1)" {
		t.Fatalf("FormatClientUUID = %q, want %q", got, "东京节点(audit-c1)")
	}
	if got := FormatClientUUID("audit-c2"); got != "audit-c2" {
		t.Fatalf("FormatClientUUID unnamed = %q, want %q", got, "audit-c2")
	}
	if got := FormatClientUUID("missing-uuid"); got != "missing-uuid" {
		t.Fatalf("FormatClientUUID missing = %q, want %q", got, "missing-uuid")
	}

	gotBatch := FormatClientUUIDs([]string{"audit-c1", "missing-uuid", "audit-c2", " "})
	wantBatch := []string{"东京节点(audit-c1)", "missing-uuid", "audit-c2"}
	if len(gotBatch) != len(wantBatch) {
		t.Fatalf("FormatClientUUIDs len = %d (%#v), want %d", len(gotBatch), gotBatch, len(wantBatch))
	}
	for i := range wantBatch {
		if gotBatch[i] != wantBatch[i] {
			t.Fatalf("FormatClientUUIDs[%d] = %q, want %q", i, gotBatch[i], wantBatch[i])
		}
	}

	gotModels := FormatClients([]models.Client{
		{UUID: "audit-c1"},
		{UUID: "inline-uuid", Name: "HK-Edge"},
	})
	if len(gotModels) != 2 || gotModels[0] != "东京节点(audit-c1)" || gotModels[1] != "HK-Edge(inline-uuid)" {
		t.Fatalf("FormatClients = %#v", gotModels)
	}
}

func TestRedactionAndFormattingHelpers(t *testing.T) {
	if got := MaskToken("1234567890abcdef"); got != "1234...cdef" {
		t.Fatalf("MaskToken = %q, want %q", got, "1234...cdef")
	}
	if got := MaskToken("short"); got != "***" {
		t.Fatalf("MaskToken short = %q, want ***", got)
	}

	if got := RedactDSN("mysql://admin:supersecret@tcp(127.0.0.1:3306)/komari"); strings.Contains(got, "supersecret") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("RedactDSN url = %q", got)
	}
	if got := RedactDSN("host=localhost user=komari password=topsecret dbname=metrics"); strings.Contains(got, "topsecret") || !strings.Contains(got, "password=[REDACTED]") {
		t.Fatalf("RedactDSN kv = %q", got)
	}

	redactedURL := RedactURL("https://user:pass@hooks.example.com/notify?token=abc123&chat_id=42")
	if strings.Contains(redactedURL, "pass") || strings.Contains(redactedURL, "abc123") || !strings.Contains(redactedURL, "chat_id=42") {
		t.Fatalf("RedactURL = %q", redactedURL)
	}

	if got := Truncate("你好世界测试", 3); got != "你好世... (truncated, total 6 chars)" {
		t.Fatalf("Truncate utf8 = %q", got)
	}

	if got := FormatBytes(1536); got != "1.50 KiB" {
		t.Fatalf("FormatBytes = %q, want 1.50 KiB", got)
	}

	var nilTime *time.Time
	before := map[string]any{
		"sitename":      "Komari",
		"api_key":       "old-secret-value",
		"metric_db_dsn": "mysql://u:oldpass@host/db",
		"weight":        5,
		"expired_at":    nilTime,
	}
	submitted := map[string]any{
		"sitename":      "Komari Pro",
		"api_key":       "new-secret-value",
		"metric_db_dsn": "mysql://u:newpass@host/db",
		"weight":        float64(5),
		"expired_at":    nil,
	}
	keys, lines := FormatDiff(before, submitted)
	if len(keys) != 3 {
		t.Fatalf("FormatDiff changed keys = %#v, want 3 changed keys", keys)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "old-secret-value") || strings.Contains(joined, "new-secret-value") ||
		strings.Contains(joined, "oldpass") || strings.Contains(joined, "newpass") {
		t.Fatalf("FormatDiff leaked secret material:\n%s", joined)
	}
	if !strings.Contains(joined, `sitename: "Komari" -> "Komari Pro"`) {
		t.Fatalf("FormatDiff missing sitename diff:\n%s", joined)
	}
	if !strings.Contains(joined, "api_key: [REDACTED] (updated)") {
		t.Fatalf("FormatDiff missing redacted api_key:\n%s", joined)
	}
}

func TestLogWithIDAndUpdateMessage(t *testing.T) {
	db := dbcore.GetDBInstance()
	t.Cleanup(func() {
		db.Where("msg_type = ?", "terminal").Delete(&models.Log{})
	})

	id := LogWithID("192.0.2.5", "user-1", "Terminal session: status=active", "terminal")
	if id == 0 {
		t.Fatal("LogWithID returned 0")
	}
	UpdateMessage(id, "Terminal session: status=closed, duration=5s")

	var logs []models.Log
	if err := db.Where("id = ?", id).Find(&logs).Error; err != nil {
		t.Fatalf("query log: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logs))
	}
	if logs[0].Message != "Terminal session: status=closed, duration=5s" {
		t.Fatalf("updated message = %q", logs[0].Message)
	}
}
