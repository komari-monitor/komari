package messageSender

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/managedconfig"
	"gorm.io/gorm"
)

// Notification is the message passed to a notification channel.
type Notification struct {
	Event   models.EventMessage `json:"event"`
	Title   string              `json:"title"`
	Message string              `json:"message"`
}

// NotificationChannel is implemented by built-in and plugin notification
// channels.
type NotificationChannel interface {
	Send(context.Context, Notification, map[string]any) error
	Unload() error
}

// NotificationChannelInfo describes a registered notification channel.
type NotificationChannelInfo struct {
	ID            string               `json:"id"`
	Configuration models.Configuration `json:"configuration"`
}

type notificationChannelEntry struct {
	configuration models.Configuration
	channel       NotificationChannel
}

var notificationChannels = struct {
	sync.RWMutex
	entries map[string]*notificationChannelEntry
}{
	entries: make(map[string]*notificationChannelEntry),
}

// RegisterNotificationChannel registers a channel. Registration fails for an
// empty or already-used ID.
func RegisterNotificationChannel(id string, configuration models.Configuration, notificationChannel NotificationChannel) error {
	return registerNotificationChannel(id, configuration, notificationChannel, nil)
}

func registerNotificationChannel(
	id string,
	configuration models.Configuration,
	notificationChannel NotificationChannel,
	afterRegister func() error,
) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("notification channel id is required")
	}
	if notificationChannel == nil {
		return fmt.Errorf("notification channel %q is nil", id)
	}
	if configuration.Type == "" {
		configuration.Type = models.ThemeConfigurationManaged
	}

	notificationChannels.Lock()
	defer notificationChannels.Unlock()
	if _, exists := notificationChannels.entries[id]; exists {
		return fmt.Errorf("notification channel %q is already registered", id)
	}
	notificationChannels.entries[id] = &notificationChannelEntry{
		configuration: configuration,
		channel:       notificationChannel,
	}
	if afterRegister != nil {
		if err := afterRegister(); err != nil {
			delete(notificationChannels.entries, id)
			return err
		}
	}
	return nil
}

// UnregisterNotificationChannel removes a channel after all calls using it
// have completed, then unloads it.
func UnregisterNotificationChannel(id string) error {
	id = strings.TrimSpace(id)
	notificationChannels.Lock()
	found, exists := notificationChannels.entries[id]
	if exists {
		delete(notificationChannels.entries, id)
	}
	notificationChannels.Unlock()
	if !exists {
		return nil
	}
	return found.channel.Unload()
}

// CallNotificationChannel invokes one registered channel with the latest
// saved, defaulted, and resolved configuration.
func CallNotificationChannel(ctx context.Context, id string, notification Notification) error {
	id = strings.TrimSpace(id)
	notificationChannels.RLock()
	found := notificationChannels.entries[id]
	if found == nil {
		notificationChannels.RUnlock()
		return fmt.Errorf("notification channel %q is not registered", id)
	}
	configuration := found.configuration
	notificationChannel := found.channel
	defer notificationChannels.RUnlock()

	values, err := resolveNotificationChannelConfiguration(id, configuration)
	if err != nil {
		return err
	}
	return notificationChannel.Send(ctx, notification, values)
}

// ListNotificationChannels returns a snapshot sorted by channel ID.
func ListNotificationChannels() []NotificationChannelInfo {
	notificationChannels.RLock()
	defer notificationChannels.RUnlock()
	result := make([]NotificationChannelInfo, 0, len(notificationChannels.entries))
	for id, found := range notificationChannels.entries {
		result = append(result, NotificationChannelInfo{
			ID:            id,
			Configuration: found.configuration,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func NotificationChannelRegistered(id string) bool {
	return notificationChannelRegistered(id)
}

func GetNotificationChannelConfiguration(id string) (models.Configuration, map[string]any, bool, error) {
	configuration, exists := notificationChannelConfiguration(id)
	if !exists {
		return models.Configuration{}, nil, false, nil
	}
	values, err := resolveNotificationChannelConfiguration(id, configuration)
	if err != nil {
		return models.Configuration{}, nil, true, err
	}
	return configuration, values, true, nil
}

func notificationChannelRegistered(id string) bool {
	notificationChannels.RLock()
	defer notificationChannels.RUnlock()
	return notificationChannels.entries[strings.TrimSpace(id)] != nil
}

func notificationChannelConfiguration(id string) (models.Configuration, bool) {
	notificationChannels.RLock()
	defer notificationChannels.RUnlock()
	found := notificationChannels.entries[strings.TrimSpace(id)]
	if found == nil {
		return models.Configuration{}, false
	}
	return found.configuration, true
}

func resolveNotificationChannelConfiguration(id string, configuration models.Configuration) (map[string]any, error) {
	values := map[string]any{}
	cfg, err := database.GetMessageSenderConfigByName(id)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("read notification channel %q configuration: %w", id, err)
	}
	if err == nil {
		if err := json.Unmarshal([]byte(cfg.Addition), &values); err != nil {
			return nil, fmt.Errorf("parse notification channel %q configuration: %w", id, err)
		}
	}

	items := managedconfig.Items(configuration)
	for _, item := range items {
		if item.Key == "" {
			continue
		}
		if _, exists := values[item.Key]; !exists {
			values[item.Key] = managedconfig.DefaultValue(item)
		}
	}
	if err := managedconfig.ResolveForOutput(values, items); err != nil {
		return nil, fmt.Errorf("resolve notification channel %q configuration: %w", id, err)
	}
	return values, nil
}

// ManagedConfiguration builds a managed configuration declaration from a
// built-in channel's tagged configuration struct.
func ManagedConfiguration(name any, configuration any) models.Configuration {
	value := reflect.ValueOf(configuration)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	items := make([]models.ManagedThemeConfigurationItem, 0, value.NumField())
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		itemType := field.Tag.Get("type")
		switch {
		case itemType == "option":
			itemType = "select"
		case itemType == "richtext":
			itemType = "richtext"
		case field.Type.Kind() == reflect.Bool:
			itemType = "switch"
		case field.Type.Kind() >= reflect.Int && field.Type.Kind() <= reflect.Int64:
			itemType = "number"
		case field.Type.Kind() >= reflect.Uint && field.Type.Kind() <= reflect.Uint64:
			itemType = "number"
		case field.Type.Kind() == reflect.Float32 || field.Type.Kind() == reflect.Float64:
			itemType = "number"
		default:
			itemType = "string"
		}
		items = append(items, models.ManagedThemeConfigurationItem{
			Key:      key,
			Name:     field.Tag.Get("name"),
			Required: field.Tag.Get("required") == "true",
			Type:     itemType,
			Options:  field.Tag.Get("options"),
			Default:  parseConfigurationDefault(field, field.Tag.Get("default")),
			Help:     field.Tag.Get("help"),
		})
	}
	return models.Configuration{
		Type: models.ThemeConfigurationManaged,
		Name: name,
		Data: items,
	}
}

// DecodeConfiguration decodes resolved configuration values into a built-in
// channel's typed configuration struct.
func DecodeConfiguration(values map[string]any, target any) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func parseConfigurationDefault(field reflect.StructField, raw string) any {
	if raw == "" {
		return nil
	}
	switch field.Type.Kind() {
	case reflect.Bool:
		value, err := strconv.ParseBool(raw)
		if err == nil {
			return value
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := strconv.ParseInt(raw, 10, field.Type.Bits())
		if err == nil {
			return value
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value, err := strconv.ParseUint(raw, 10, field.Type.Bits())
		if err == nil {
			return value
		}
	case reflect.Float32, reflect.Float64:
		value, err := strconv.ParseFloat(raw, field.Type.Bits())
		if err == nil {
			return value
		}
	}
	return raw
}
