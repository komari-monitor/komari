package javascript

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

type JavaScriptSender struct {
	Addition
	runtime *jsruntime.Runtime
}

func (j *JavaScriptSender) GetName() string {
	return "Javascript"
}

func (j *JavaScriptSender) GetConfiguration() factory.Configuration {
	return &j.Addition
}

func (j *JavaScriptSender) Init() error {
	runtime, err := jsruntime.New(j.Addition.Script, jsruntime.Options{})
	if err != nil {
		return err
	}
	if !runtime.HasFunction("sendMessage") {
		runtime.Close()
		return errors.New("sendMessage function not defined or not callable in script")
	}
	if j.runtime != nil {
		j.runtime.Close()
	}
	j.runtime = runtime
	return nil
}

func (j *JavaScriptSender) Destroy() error {
	if j.runtime != nil {
		j.runtime.Close()
		j.runtime = nil
	}
	return nil
}

func (j *JavaScriptSender) SendTextMessage(message, title string) error {
	if err := j.ensureRuntime(); err != nil {
		return err
	}
	return j.runtime.Call("sendMessage", message, title)
}

func (j *JavaScriptSender) SendEvent(event models.EventMessage) error {
	if err := j.ensureRuntime(); err != nil {
		return err
	}
	if !j.runtime.HasFunction("sendEvent") {
		return j.fallbackToTextMessage(event)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}
	var eventMap map[string]interface{}
	if err := json.Unmarshal(eventJSON, &eventMap); err != nil {
		return fmt.Errorf("failed to unmarshal event: %v", err)
	}
	return j.runtime.Call("sendEvent", eventMap)
}

func (j *JavaScriptSender) ensureRuntime() error {
	if j.runtime != nil {
		return nil
	}
	return j.Init()
}

// fallbackToTextMessage formats structured events for scripts that only
// implement sendMessage.
func (j *JavaScriptSender) fallbackToTextMessage(event models.EventMessage) error {
	message := fmt.Sprintf("%v%v%v\nEvent: %v\nMessage: %v\nTime: %s",
		event.Emoji, event.Emoji, event.Emoji,
		event.Event,
		event.Message,
		event.Time.UTC().Format(time.RFC3339Nano))
	if len(event.Clients) > 0 {
		clientNames := make([]string, 0, len(event.Clients))
		for _, client := range event.Clients {
			name := client.Name
			if name == "" {
				name = client.UUID
			}
			clientNames = append(clientNames, name)
		}
		message = fmt.Sprintf("%v%v%v\nEvent: %v\nClients: %s\nMessage: %v\nTime: %s",
			event.Emoji, event.Emoji, event.Emoji,
			event.Event,
			clientNames,
			event.Message,
			event.Time.UTC().Format(time.RFC3339Nano))
	}
	return j.SendTextMessage(message, fmt.Sprint(event.Event))
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &JavaScriptSender{}
	})
}

var _ factory.IMessageSender = (*JavaScriptSender)(nil)
