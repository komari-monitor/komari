package messageSender

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

type testChannel struct {
	mu       sync.Mutex
	sends    int
	config   map[string]any
	unloaded bool
	err      error
}

func (c *testChannel) Send(_ context.Context, _ Notification, config map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends++
	c.config = config
	return c.err
}

func (c *testChannel) Unload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unloaded = true
	return nil
}

func TestRegistryRegisterListCallAndUnregister(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)

	config := models.Configuration{
		Type: models.ThemeConfigurationManaged,
		Name: "Test",
		Data: []models.ManagedThemeConfigurationItem{
			{Key: "endpoint", Type: "string", Default: "default-endpoint"},
		},
	}
	sender := &testChannel{}
	if err := RegisterNotificationChannel("test", config, sender); err != nil {
		t.Fatal(err)
	}
	if err := RegisterNotificationChannel("", config, sender); err == nil {
		t.Fatal("empty id was accepted")
	}
	if err := RegisterNotificationChannel("test", config, sender); err == nil {
		t.Fatal("duplicate id was accepted")
	}
	list := ListNotificationChannels()
	if len(list) != 1 || list[0].ID != "test" || list[0].Configuration.Name != "Test" {
		t.Fatalf("ListNotificationChannels() = %#v", list)
	}
	if err := CallNotificationChannel(context.Background(), "test", Notification{Title: "hello"}); err != nil {
		t.Fatal(err)
	}
	if sender.config["endpoint"] != "default-endpoint" {
		t.Fatalf("config = %#v", sender.config)
	}
	if err := UnregisterNotificationChannel("test"); err != nil {
		t.Fatal(err)
	}
	if !sender.unloaded {
		t.Fatal("Unload was not called")
	}
	if err := CallNotificationChannel(context.Background(), "test", Notification{}); err == nil {
		t.Fatal("unregistered channel was callable")
	}
}

func TestRegistryUnregisterWaitsForInFlightCall(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)

	blocking := &blockingChannel{started: make(chan struct{}), release: make(chan struct{})}
	if err := RegisterNotificationChannel("blocking", models.Configuration{Type: "managed"}, blocking); err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() { callDone <- CallNotificationChannel(context.Background(), "blocking", Notification{}) }()
	<-blocking.started

	unloadDone := make(chan error, 1)
	go func() { unloadDone <- UnregisterNotificationChannel("blocking") }()
	select {
	case <-unloadDone:
		t.Fatal("UnregisterNotificationChannel returned before the in-flight call finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(blocking.release)
	if err := <-callDone; err != nil {
		t.Fatal(err)
	}
	if err := <-unloadDone; err != nil {
		t.Fatal(err)
	}
}

type blockingChannel struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingChannel) Send(context.Context, Notification, map[string]any) error {
	close(c.started)
	<-c.release
	return nil
}

func (c *blockingChannel) Unload() error { return nil }

func TestRegistryUsesSavedConfigurationAndResolvesSelectors(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)
	db := dbcore.GetDBInstance()
	if err := db.AutoMigrate(&models.MessageSenderProvider{}, &models.Client{}, &models.PingTask{}); err != nil {
		t.Fatal(err)
	}
	const nodeID = "notification-registry-node"
	const taskID = uint(43)
	if err := db.Where("uuid = ?", nodeID).Delete(&models.Client{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Client{UUID: nodeID, Token: nodeID, Name: "node"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", taskID).Delete(&models.PingTask{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PingTask{Id: taskID, Name: "task"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("name = ?", "configured").Delete(&models.MessageSenderProvider{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.MessageSenderProvider{
		Name:     "configured",
		Addition: `{"nodes":"[\"notification-registry-node\",\"missing\"]","tasks":"[43,7]","token":"saved"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sender := &testChannel{}
	config := models.Configuration{
		Type: "managed",
		Data: []models.ManagedThemeConfigurationItem{
			{Key: "token", Type: "string", Default: "default"},
			{Key: "nodes", Type: "nodes"},
			{Key: "tasks", Type: "pingtasks"},
			{Key: "missing", Type: "number"},
		},
	}
	if err := RegisterNotificationChannel("configured", config, sender); err != nil {
		t.Fatal(err)
	}
	if err := CallNotificationChannel(context.Background(), "configured", Notification{}); err != nil {
		t.Fatal(err)
	}
	if sender.config["token"] != "saved" {
		t.Fatalf("token = %#v", sender.config["token"])
	}
	if sender.config["missing"] != float64(0) {
		t.Fatalf("missing default = %#v", sender.config["missing"])
	}
	nodes, ok := sender.config["nodes"].([]string)
	if !ok || len(nodes) != 1 || nodes[0] != nodeID {
		t.Fatalf("nodes = %#v", sender.config["nodes"])
	}
	tasks, ok := sender.config["tasks"].([]uint)
	if !ok || len(tasks) != 1 || tasks[0] != taskID {
		t.Fatalf("tasks = %#v", sender.config["tasks"])
	}
}

func TestRegistryUsesLatestSavedConfiguration(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)
	db := dbcore.GetDBInstance()
	if err := db.AutoMigrate(&models.MessageSenderProvider{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("name = ?", "latest").Delete(&models.MessageSenderProvider{}).Error; err != nil {
		t.Fatal(err)
	}
	sender := &testChannel{}
	if err := RegisterNotificationChannel("latest", models.Configuration{Type: "managed"}, sender); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.MessageSenderProvider{
		Name:     "latest",
		Addition: `{"token":"first"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CallNotificationChannel(context.Background(), "latest", Notification{}); err != nil {
		t.Fatal(err)
	}
	if sender.config["token"] != "first" {
		t.Fatalf("first config = %#v", sender.config)
	}
	if err := db.Model(&models.MessageSenderProvider{}).
		Where("name = ?", "latest").
		Update("addition", `{"token":"second"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := CallNotificationChannel(context.Background(), "latest", Notification{}); err != nil {
		t.Fatal(err)
	}
	if sender.config["token"] != "second" {
		t.Fatalf("second config = %#v", sender.config)
	}
}

func TestRegistryDoesNotRetryFailedCall(t *testing.T) {
	clearNotificationChannels()
	t.Cleanup(clearNotificationChannels)
	sender := &testChannel{err: errors.New("send failed")}
	if err := RegisterNotificationChannel("failed", models.Configuration{Type: "managed"}, sender); err != nil {
		t.Fatal(err)
	}
	if err := CallNotificationChannel(context.Background(), "failed", Notification{}); err == nil {
		t.Fatal("failed call returned nil")
	}
	if sender.sends != 1 {
		t.Fatalf("sends = %d, want 1", sender.sends)
	}
}

func clearNotificationChannels() {
	notificationChannels.Lock()
	notificationChannels.entries = make(map[string]*notificationChannelEntry)
	notificationChannels.Unlock()
}

var _ NotificationChannel = (*testChannel)(nil)
var _ NotificationChannel = (*blockingChannel)(nil)
