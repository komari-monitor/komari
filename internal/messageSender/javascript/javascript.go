package javascript

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/jsruntime"
	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type JavaScriptSender struct {
	Addition
	js          *jsruntime.JsRuntime
	noopProgram *goja.Program
}

func (j *JavaScriptSender) GetName() string {
	return "Javascript"
}

func (j *JavaScriptSender) GetConfiguration() factory.Configuration {
	return &j.Addition
}

func (j *JavaScriptSender) Init() error {
	var err error
	j.js, err = jsruntime.NewBuilder().WithNodejs().
		WithFetch().
		WithXHR().
		Build()
	if err != nil {
		return fmt.Errorf("failed to build js runtime: %v", err)
	}

	if strings.TrimSpace(j.Addition.Script) == "" {
		return errors.New("script is empty")
	}
	if _, err := j.js.RunScript(j.Addition.Script); err != nil {
		return fmt.Errorf("failed to execute script: %v", err)
	}

	if !j.js.HasFunction("sendMessage") {
		return errors.New("sendMessage function not defined in script")
	}

	return nil
}

func (j *JavaScriptSender) Destroy() error {
	if j.js != nil {
		j.js.Stop()
		j.js = nil
	}
	return nil
}

func (j *JavaScriptSender) SendTextMessage(message, title string) error {
	if j.js == nil {
		return errors.New("JavaScript runtime not initialized")
	}
	result, err := j.js.Call("sendMessage", message, title)
	if err != nil {
		return fmt.Errorf("JavaScript error: %v, result: %v", err, result)
	}
	if result.ToBoolean() == false {
		return errors.New("sendMessage returned false")
	}
	return nil
}

func (j *JavaScriptSender) SendEvent(event models.EventMessage) error {
	if j.js == nil {
		return errors.New("JavaScript runtime not initialized")
	}

	// 如果没有定义 sendEvent,则回退到使用 SendTextMessage
	if !j.js.HasFunction("sendEvent") {
		return j.fallbackToTextMessage(event)
	}

	// 将 EventMessage 转换为 JavaScript 对象
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %v", err)
	}

	var eventMap map[string]interface{}
	if err := json.Unmarshal(eventJSON, &eventMap); err != nil {
		return fmt.Errorf("failed to unmarshal event: %v", err)
	}

	// 调用 sendEvent 函数
	result, err := j.js.Call("sendEvent", eventMap)
	if err != nil {
		return fmt.Errorf("JavaScript error in sendEvent: %v, result: %v", err, result)
	}
	if result.ToBoolean() == false {
		return errors.New("sendEvent returned false")
	}
	return nil
}

// fallbackToTextMessage 当没有定义 sendEvent 时,回退到使用文本消息格式
func (j *JavaScriptSender) fallbackToTextMessage(event models.EventMessage) error {
	// 构建简单的文本消息
	message := fmt.Sprintf("%s%s%s\nEvent: %s\nMessage: %s\nTime: %s",
		event.Emoji, event.Emoji, event.Emoji,
		event.Event,
		event.Message,
		event.Time.Format(time.RFC3339))

	// 添加客户端信息
	if len(event.Clients) > 0 {
		clientNames := make([]string, 0, len(event.Clients))
		for _, c := range event.Clients {
			name := c.Name
			if name == "" {
				name = c.UUID
			}
			clientNames = append(clientNames, name)
		}
		message = fmt.Sprintf("%s%s%s\nEvent: %s\nClients: %s\nMessage: %s\nTime: %s",
			event.Emoji, event.Emoji, event.Emoji,
			event.Event,
			clientNames,
			event.Message,
			event.Time.Format(time.RFC3339))
	}

	return j.SendTextMessage(message, event.Event)
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &JavaScriptSender{}
	})
}

// 确保实现了 IMessageSender 接口
var _ factory.IMessageSender = (*JavaScriptSender)(nil)
