package javascript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/messageSender/outboundhttp"
)

// DataDir confines notification scripts' filesystem and local module access.
var DataDir = "./data/notification/javascript"

const executionTimeout = 30 * time.Second

type JavaScriptSender struct {
	mu      sync.Mutex
	script  string
	runtime *jsruntime.Runtime
}

func (j *JavaScriptSender) Send(ctx context.Context, notification messageSender.Notification, config map[string]any) error {
	var addition Addition
	if err := messageSender.DecodeConfiguration(config, &addition); err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := j.ensureRuntime(addition.Script); err != nil {
		return err
	}
	if !j.runtime.HasFunction("sendEvent") {
		return j.runtime.Call("sendMessage", notification.Message, notification.Title)
	}

	// Preserve the event's JSON field names and nested payloads for legacy scripts.
	raw, err := json.Marshal(notification.Event)
	if err != nil {
		return fmt.Errorf("marshal notification event: %w", err)
	}
	var event map[string]any
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("decode notification event: %w", err)
	}
	return j.runtime.Call("sendEvent", event)
}

func (j *JavaScriptSender) ensureRuntime(script string) error {
	if j.runtime != nil && j.script == script {
		return nil
	}
	j.closeRuntime()
	if strings.TrimSpace(script) == "" {
		return errors.New("JavaScript notification script is not configured")
	}
	if err := os.MkdirAll(DataDir, 0755); err != nil {
		return fmt.Errorf("create JavaScript notification directory: %w", err)
	}
	runtime, err := jsruntime.New(script, jsruntime.Options{
		BaseDir:    DataDir,
		NodeJS:     true,
		Timeout:    executionTimeout,
		HTTPClient: outboundhttp.NewClient(executionTimeout),
	})
	if err != nil {
		return err
	}
	if !runtime.HasFunction("sendMessage") {
		runtime.Close()
		return errors.New("sendMessage function not defined or not callable in script")
	}
	j.script = script
	j.runtime = runtime
	return nil
}

func (j *JavaScriptSender) closeRuntime() {
	if j.runtime != nil {
		j.runtime.Close()
		j.runtime = nil
	}
	j.script = ""
}

func (j *JavaScriptSender) Unload() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closeRuntime()
	return nil
}

func Register() error {
	return messageSender.RegisterNotificationChannel("javascript", messageSender.ManagedConfiguration("JavaScript", Addition{}), &JavaScriptSender{})
}

var _ messageSender.NotificationChannel = (*JavaScriptSender)(nil)
