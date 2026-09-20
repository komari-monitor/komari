package messageSender

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
)

// SendNotification 是通知发送的统一实现：解析事件中的客户端 UUID（外部传入可只含
// UUID 字段）后委托 SendEvent。内部调用与 admin:sendNotification RPC 共用此实现。
func SendNotification(event models.EventMessage) error {
	if len(event.Clients) > 0 {
		eventClients := make([]models.Client, 0, len(event.Clients))
		for _, c := range event.Clients {
			if c.UUID == "" {
				continue
			}
			if full, err := clients.GetClientByUUID(c.UUID); err == nil {
				eventClients = append(eventClients, full)
			}
		}
		if len(eventClients) == 0 {
			return fmt.Errorf("none of the specified clients exist")
		}
		event.Clients = eventClients
	}
	return SendEvent(event)
}

func SendEvent(event models.EventMessage) error {
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	} else {
		event.Time = event.Time.UTC()
	}
	var err error
	cfg, err := config.GetMany(map[string]any{
		config.NotificationEnabledKey:  false,
		config.NotificationTemplateKey: "{{emoji}}{{emoji}}{{emoji}}\nEvent: {{event}}\nClients: {{client}}\nMessage: {{message}}\nTime: {{time}}",
	})
	if err != nil {
		return err
	}
	if !cfg[config.NotificationEnabledKey].(bool) {
		return nil
	}

	method, err := config.GetAs[string](config.NotificationMethodKey, "none")
	if err != nil {
		return err
	}
	messageTemplate := cfg[config.NotificationTemplateKey].(string)
	messageTemplate = parseTemplate(messageTemplate, event)

	if method == "" || method == "none" {
		return nil
	}
	err = CallNotificationChannel(context.Background(), method, Notification{
		Event:   event,
		Title:   fmt.Sprint(event.Event),
		Message: messageTemplate,
	})
	if err == nil {
		auditlog.Log("", "", "Event message sent: "+fmt.Sprint(event.Event), "info")
		return nil
	}
	auditlog.Log("", "", "Failed to send event message: "+err.Error()+","+fmt.Sprint(event.Event), "error")
	return err
}

// parseTemplate 通过反射解析 event 持有的结构体的所有字段，将模板中的 {{fieldName}}
// 占位符替换为对应值（字段名小写，如 {{event}}、{{time}}、{{message}}、{{emoji}}）。
// 为兼容旧模板，Clients 字段同时支持 {{client}}（旧）与 {{clients}}（新）两种占位符。
// event 为非结构体（如 map 等）时原样返回模板。
func parseTemplate(messageTemplate string, event any) string {
	eventValue := reflect.ValueOf(event)
	if eventValue.Kind() == reflect.Pointer {
		eventValue = eventValue.Elem()
	}
	if eventValue.Kind() != reflect.Struct {
		return messageTemplate
	}
	eventType := eventValue.Type()
	result := messageTemplate
	for i := 0; i < eventType.NumField(); i++ {
		field := eventType.Field(i)
		placeholder := "{{" + strings.ToLower(field.Name) + "}}"
		value := formatTemplateField(field.Name, eventValue.Field(i))
		result = strings.ReplaceAll(result, placeholder, value)
		if field.Name == "Clients" {
			result = strings.ReplaceAll(result, "{{client}}", value)
		}
	}
	return result
}

// formatTemplateField 将事件字段格式化为模板占位符的值：
// 字符串字段直接取值；any 字段取其实际值格式化；Clients 字段拼接客户端名称
// （无名称时回退为 UUID）；Time 字段按服务器本地时区格式化为 RFC3339；其余类型返回空串。
func formatTemplateField(fieldName string, v reflect.Value) string {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		return fmt.Sprint(v.Interface())
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Slice:
		if fieldName == "Clients" {
			clientNames := make([]string, 0, v.Len())
			for i := 0; i < v.Len(); i++ {
				clientValue := v.Index(i)
				name := clientValue.FieldByName("Name").String()
				if strings.TrimSpace(name) == "" {
					name = clientValue.FieldByName("UUID").String()
				}
				clientNames = append(clientNames, name)
			}
			return strings.Join(clientNames, ", ")
		}
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			return t.In(time.Local).Format("2006-01-02 15:04:05")
		}
	}
	return ""
}
