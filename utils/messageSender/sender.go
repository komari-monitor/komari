package messageSender

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/config"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/messageSender/factory"
)

var (
	currentProvider factory.IMessageSender
	mu              = sync.Mutex{}
	once            = sync.Once{}
)

func CurrentProvider() factory.IMessageSender {
	mu.Lock()
	defer mu.Unlock()
	return currentProvider
}

func Initialize() {
	go func() {
		once.Do(func() {
			all := factory.GetAllMessageSenders()
			for _, provider := range all {
				if _, err := database.GetMessageSenderConfigByName(provider.GetName()); err == nil {
					continue
				}
				// 如果数据库中没有该提供者的配置，则保存默认配置
				config := provider.GetConfiguration()
				configBytes, err := json.Marshal(config)
				if err != nil {
					log.Printf("Failed to marshal config for provider %s: %v", provider.GetName(), err)
					return
				}
				if err := database.SaveMessageSenderConfig(&models.MessageSenderProvider{
					Name:     provider.GetName(),
					Addition: string(configBytes),
				}); err != nil {
					log.Printf("Failed to save default config for provider %s: %v", provider.GetName(), err)
					return
				}
			}
		})
	}()
	cfg, _ := config.Get()

	if cfg.NotificationMethod == "" || cfg.NotificationMethod == "none" {
		LoadProvider("empty", "{}")
		return
	}

	// 尝试从数据库加载配置
	senderConfig, err := database.GetMessageSenderConfigByName(cfg.NotificationMethod)
	if err != nil {
		// 如果没有找到配置，使用empty provider
		LoadProvider("empty", "{}")
		return
	}
	LoadProvider(cfg.NotificationMethod, senderConfig.Addition)
}

func SendTextMessage(message string, title string) error {
	if CurrentProvider() == nil {
		return fmt.Errorf("message sender provider is not initialized")
	}
	var err error
	cfg, err := config.Get()
	if err != nil {
		return err
	}
	if !cfg.NotificationEnabled {
		return nil
	}
	for i := 0; i < 3; i++ {
		err = CurrentProvider().SendTextMessage(message, title)
		if err == nil {
			auditlog.Log("", "", "Message sent: "+title, "info")
			return nil
		}
	}
	auditlog.Log("", "", "Failed to send message after 3 attempts: "+err.Error()+","+title, "error")
	return err
}
func SendEvent(event models.EventMessage) error {
	if CurrentProvider() == nil {
		return fmt.Errorf("message sender provider is not initialized")
	}
	var err error
	cfg, err := config.Get()
	if err != nil {
		return err
	}
	if !cfg.NotificationEnabled {
		return nil
	}

	// 检查提供者是否实现了 IEventMessageSender 接口
	if eventSender, ok := CurrentProvider().(factory.IEventMessageSender); ok {
		// 如果实现了,直接调用 SendEvent
		for i := 0; i < 3; i++ {
			err = eventSender.SendEvent(event)
			if err == nil || err.Error() == "short response: \x00\x00\x00\x1a\x00\x00\x00" {
				auditlog.Log("", "", "Event message sent: "+event.Event, "info")
				return nil
			}
		}
		auditlog.Log("", "", "Failed to send event message after 3 attempts: "+err.Error()+","+event.Event, "error")
		return err
	}

	// 如果没有实现,使用模板格式化为文本消息
	messageTemplate := cfg.NotificationTemplate
	if messageTemplate == "" {
		messageTemplate = "{{emoji}}{{emoji}}{{emoji}}\nEvent: {{event}}\nClients: {{client}}\nMessage: {{message}}\nTime: {{time}}"
	}
	messageTemplate = parseTemplate(messageTemplate, event)

	for i := 0; i < 3; i++ {
		err = CurrentProvider().SendTextMessage(messageTemplate, event.Event)
		if err == nil || err.Error() == "short response: \x00\x00\x00\x1a\x00\x00\x00" { // QQ 会返回这个错误，但实际上消息是发送成功的
			auditlog.Log("", "", "Event message sent: "+event.Event, "info")
			return nil
		}
	}
	auditlog.Log("", "", "Failed to send event message after 3 attempts: "+err.Error()+","+event.Event, "error")
	return err
}

func parseTemplate(messageTemplate string, event models.EventMessage) string {
	// Aggregate client names. If Name is empty, fall back to UUID.
	clientNames := make([]string, 0, len(event.Clients))
	for _, c := range event.Clients {
		name := c.Name
		if strings.TrimSpace(name) == "" {
			// fallback to UUID when name is not set
			name = c.UUID
		}
		clientNames = append(clientNames, name)
	}
	joinedClients := strings.Join(clientNames, ", ")

	replaceMap := map[string]string{
		"{{event}}":   event.Event,
		"{{client}}":  joinedClients,
		"{{time}}":    event.Time.Format(time.RFC3339),
		"{{message}}": event.Message,
		"{{emoji}}":   event.Emoji,
	}
	result := messageTemplate
	for placeholder, value := range replaceMap {
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}
