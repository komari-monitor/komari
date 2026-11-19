package messageSender

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
)

func TestParseTemplateFormatsEventTimeInLocalTimezone(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocal })

	eventTime := time.Date(2026, 7, 17, 1, 30, 0, 123456789, time.UTC)
	got := parseTemplate("{{time}}", models.EventMessage{Time: eventTime})
	want := "2026-07-17 09:30:00"
	if got != want {
		t.Fatalf("formatted event time = %q, want %q", got, want)
	}
}

func TestParseTemplateFormatsAnyTypedEventField(t *testing.T) {
	for _, tc := range []struct {
		event any
		want  string
	}{
		{event: models.EventMessage{Event: "Offline"}, want: "Offline"},
		{event: models.EventMessage{Event: 42}, want: "42"},
		{event: models.EventMessage{Event: nil}, want: ""},
	} {
		if got := parseTemplate("{{event}}", tc.event); got != tc.want {
			t.Fatalf("parseTemplate with Event=%v = %q, want %q", tc.event, got, tc.want)
		}
	}
}

func TestSendNotificationWithActorRecordsDetailedAuditLog(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)

	db := dbcore.GetDBInstance()
	t.Cleanup(func() {
		db.Where("uuid = ?", "sender-audit-node").Delete(&models.Client{})
		db.Where("1 = 1").Delete(&models.Log{})
	})
	if err := db.Create(&models.Client{
		UUID:  "sender-audit-node",
		Token: "tok-sender-audit-node",
		Name:  "Tokyo-Prod",
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}

	ch := &testChannel{}
	if err := RegisterNotificationChannel("mock-audit", models.Configuration{Type: "managed"}, ch); err != nil {
		t.Fatalf("register channel: %v", err)
	}
	if err := config.SetMany(map[string]any{
		config.NotificationEnabledKey:  true,
		config.NotificationMethodKey:   "mock-audit",
		config.NotificationTemplateKey: "Event={{event}} Clients={{client}} Msg={{message}}",
	}); err != nil {
		t.Fatalf("set config: %v", err)
	}

	db.Where("1 = 1").Delete(&models.Log{})
	if err := SendNotificationWithActor("10.0.0.1", "admin-uuid-1", models.EventMessage{
		Event:   "Offline",
		Clients: []models.Client{{UUID: "sender-audit-node"}},
		Message: "heartbeat timeout",
	}); err != nil {
		t.Fatalf("SendNotificationWithActor failed: %v", err)
	}

	var sentLog models.Log
	if err := db.Order("id desc").First(&sentLog).Error; err != nil {
		t.Fatalf("fetch sent audit log: %v", err)
	}
	if sentLog.MsgType != "info" || sentLog.IP != "10.0.0.1" || sentLog.UUID != "admin-uuid-1" {
		t.Fatalf("unexpected log metadata: %+v", sentLog)
	}
	for _, want := range []string{
		"Notification sent: event=Offline, channel=mock-audit",
		"clients=Tokyo-Prod(sender-audit-node)",
		"Event=Offline Clients=Tokyo-Prod Msg=heartbeat timeout",
	} {
		if !strings.Contains(sentLog.Message, want) {
			t.Fatalf("sent audit log missing %q:\n%s", want, sentLog.Message)
		}
	}

	ch.err = errors.New("upstream 502")
	if err := SendNotificationWithActor("10.0.0.2", "admin-uuid-2", models.EventMessage{
		Event:   "Traffic",
		Clients: []models.Client{{UUID: "sender-audit-node"}},
		Message: "used 90%",
	}); err == nil {
		t.Fatal("expected channel error")
	}

	var errLog models.Log
	if err := db.Order("id desc").First(&errLog).Error; err != nil {
		t.Fatalf("fetch error audit log: %v", err)
	}
	if errLog.MsgType != "error" {
		t.Fatalf("expected error msg_type, got %q", errLog.MsgType)
	}
	for _, want := range []string{
		"Failed to send notification: event=Traffic, channel=mock-audit",
		"clients=Tokyo-Prod(sender-audit-node)",
		"Error: upstream 502",
		"used 90%",
	} {
		if !strings.Contains(errLog.Message, want) {
			t.Fatalf("failed audit log missing %q:\n%s", want, errLog.Message)
		}
	}
}
