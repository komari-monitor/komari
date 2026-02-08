package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ConfigItem struct {
	Key   string `gorm:"primaryKey;column:key;type:text"`
	Value string `gorm:"column:value;type:text"` // 存 JSON 字符串
}

func (ConfigItem) TableName() string {
	return "configs"
}

var (
	db    *gorm.DB
	SetDb = func(gdb *gorm.DB) {
		db = gdb
		migrateInPlace()
	}
)

func migrateInPlace() {
	if db.Migrator().HasTable("configs") && db.Migrator().HasColumn(&Legacy{}, "Sitename") {
		slog.Info("[>1.1.4] Moving legacy config data...")

		var oldData Legacy
		if err := db.Order("id desc").First(&oldData).Error; err != nil {
			db.Migrator().DropTable("configs")
		} else {
			var newRows []ConfigItem
			val := reflect.ValueOf(oldData)
			typ := reflect.TypeOf(oldData)

			for i := 0; i < val.NumField(); i++ {
				field := typ.Field(i)
				tag := field.Tag.Get("json")
				key := strings.Split(tag, ",")[0]

				// 过滤 id 和无用字段
				if key == "" || key == "-" || key == "id" {
					continue
				}

				valInterface := val.Field(i).Interface()
				jsonBytes, _ := json.Marshal(valInterface)

				newRows = append(newRows, ConfigItem{
					Key:   key,
					Value: string(jsonBytes),
				})
			}

			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Migrator().DropTable("configs"); err != nil {
					return err
				}
				if err := tx.AutoMigrate(&ConfigItem{}); err != nil {
					return err
				}
				if len(newRows) > 0 {
					return tx.Create(&newRows).Error
				}
				return nil
			})

			if err != nil {
				panic("failed " + err.Error())
			}
			return
		}
	}

	db.AutoMigrate(&ConfigItem{})
}

// Get 获取原始值 (反序列化为 interface{})
func Get(key string, defaul ...any) (any, error) {
	var item ConfigItem
	err := db.First(&item, "key = ?", key).Error
	if err != nil {
		if len(defaul) > 0 {
			v := defaul[0]
			err = Set(key, v)
			return v, err
		}
		return nil, err
	}

	var val any
	if err := json.Unmarshal([]byte(item.Value), &val); err != nil {
		return nil, err
	}
	return val, nil
}

// GetAs 获取并转换为指定类型 (泛型)
func GetAs[T any](key string, defaul ...any) (T, error) {
	var t T
	var item ConfigItem

	err := db.First(&item, "key = ?", key).Error
	if err != nil {
		if len(defaul) > 0 {
			v, ok := defaul[0].(T)
			if !ok {
				return t, fmt.Errorf("default value type mismatch: expected %T, got %T", t, defaul[0])
			}
			err = Set(key, v)
			return v, err
		}
		return t, err
	}

	err = json.Unmarshal([]byte(item.Value), &t)
	return t, err
}

// key[defaults]
func GetMany(keys map[string]any) (map[string]any, error) {
	var items []ConfigItem
	result := make(map[string]any)
	keyList := make([]string, 0, len(keys))
	for k := range keys {
		keyList = append(keyList, k)
	}
	if len(keyList) == 0 {
		return result, nil
	}
	if err := db.Where("key IN ?", keyList).Find(&items).Error; err != nil {
		return nil, err
	}

	foundKeys := make(map[string]bool)
	for _, item := range items {
		var parsed any
		if err := json.Unmarshal([]byte(item.Value), &parsed); err == nil {
			result[item.Key] = parsed
			foundKeys[item.Key] = true
		}
	}

	// Fill in defaults for missing keys
	for k, def := range keys {
		if _, found := foundKeys[k]; !found {
			if def != nil {
				result[k] = def
				_ = Set(k, def) // Ignore error
			}
		}
	}

	return result, nil
}

// GetManyAs 将多个配置项映射到一个结构体中，json tag 作为 Key
func GetManyAs[T any]() (*T, error) {
	var t T
	val := reflect.ValueOf(&t).Elem()
	typ := val.Type()

	tagToFieldMap := make(map[string]int) // Key -> FieldIndex
	keys := make([]string, 0)

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag != "" && tag != "-" {
			keys = append(keys, tag)
			tagToFieldMap[tag] = i
		}
	}

	if len(keys) == 0 {
		return &t, nil
	}

	var items []ConfigItem
	if err := db.Where("key IN ?", keys).Find(&items).Error; err != nil {
		return nil, err
	}

	for _, item := range items {
		fieldIndex, ok := tagToFieldMap[item.Key]
		if !ok {
			continue
		}

		fieldVal := val.Field(fieldIndex)
		if !fieldVal.CanSet() {
			continue
		}

		target := reflect.New(fieldVal.Type()).Interface()

		if err := json.Unmarshal([]byte(item.Value), target); err == nil {
			fieldVal.Set(reflect.ValueOf(target).Elem())
		}
	}

	return &t, nil
}

func GetAll() (map[string]any, error) {
	var items []ConfigItem
	result := make(map[string]any)
	if err := db.Find(&items).Error; err != nil {
		return nil, err
	}

	for _, item := range items {
		var parsed any
		if err := json.Unmarshal([]byte(item.Value), &parsed); err == nil {
			result[item.Key] = parsed
		}
	}
	return result, nil
}

// Set 设置单个配置
func Set(key string, value any) error {
	oldVal := map[string]any{}
	{
		var oldItem ConfigItem
		if err := db.First(&oldItem, "key = ?", key).Error; err == nil {
			var parsed any
			if err := json.Unmarshal([]byte(oldItem.Value), &parsed); err == nil {
				oldVal[key] = parsed
			}
		}
	}

	bytes, err := json.Marshal(value)
	if err != nil {
		return err
	}

	item := ConfigItem{
		Key:   key,
		Value: string(bytes),
	}

	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&item).Error
	if err != nil {
		return err
	}

	newVal := map[string]any{key: value}
	publishEvent(oldVal, newVal)
	return nil
}

// SetMany 将结构体保存为多个配置项
func SetManyAs[T any](config T) error {
	val := reflect.ValueOf(config)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	typ := val.Type()
	var items []ConfigItem

	for i := 0; i < val.NumField(); i++ {
		fieldType := typ.Field(i)
		tag := fieldType.Tag.Get("json")

		if tag == "" || tag == "-" {
			continue
		}

		fieldValue := val.Field(i).Interface()

		bytes, err := json.Marshal(fieldValue)
		if err != nil {
			return fmt.Errorf("marshal field %s failed: %w", fieldType.Name, err)
		}

		items = append(items, ConfigItem{
			Key:   tag,
			Value: string(bytes),
		})
	}

	if len(items) == 0 {
		return nil
	}

	keys := make([]string, 0, len(items))
	newVal := make(map[string]any, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
		var parsed any
		if err := json.Unmarshal([]byte(it.Value), &parsed); err == nil {
			newVal[it.Key] = parsed
		}
	}

	oldVal := map[string]any{}
	if len(keys) > 0 {
		var oldItems []ConfigItem
		if err := db.Where("key IN ?", keys).Find(&oldItems).Error; err == nil {
			for _, oi := range oldItems {
				var parsed any
				if err := json.Unmarshal([]byte(oi.Value), &parsed); err == nil {
					oldVal[oi.Key] = parsed
				}
			}
		}
	}

	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&items).Error
	if err != nil {
		return err
	}

	publishEvent(oldVal, newVal)
	return nil
}

func SetMany(cst map[string]any) error {
	var items []ConfigItem
	for k, v := range cst {
		bytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal key %s failed: %w", k, err)
		}
		items = append(items, ConfigItem{
			Key:   k,
			Value: string(bytes),
		})
	}
	if len(items) == 0 {
		return nil
	}

	keys := make([]string, 0, len(items))
	newVal := make(map[string]any, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
		var parsed any
		if err := json.Unmarshal([]byte(it.Value), &parsed); err == nil {
			newVal[it.Key] = parsed
		}
	}

	oldVal := map[string]any{}
	if len(keys) > 0 {
		var oldItems []ConfigItem
		if err := db.Where("key IN ?", keys).Find(&oldItems).Error; err == nil {
			for _, oi := range oldItems {
				var parsed any
				if err := json.Unmarshal([]byte(oi.Value), &parsed); err == nil {
					oldVal[oi.Key] = parsed
				}
			}
		}
	}

	err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&items).Error
	if err != nil {
		return err
	}

	publishEvent(oldVal, newVal)
	return nil
}

type ConfigEvent struct {
	Old map[string]any // Old models.Config
	New map[string]any // New models.Config
}

func (e ConfigEvent) IsChanged(key string) bool {
	oldVal, oldOk := e.Old[key]
	newVal, newOk := e.New[key]
	if !oldOk && !newOk {
		return false
	}
	if oldOk != newOk {
		return true
	}
	return !reflect.DeepEqual(oldVal, newVal)
}

func IsChangedT[T any](e ConfigEvent, key string) (bool, T) {
	changed := e.IsChanged(key)
	var zero T

	val, ok := e.New[key]
	if !ok {
		val, ok = e.Old[key]
		if !ok {
			return changed, zero
		}
	}
	if val == nil {
		return changed, zero
	}

	// Fast path: direct assertion.
	if cast, ok := val.(T); ok {
		return changed, cast
	}

	// Try reflection-based conversion (covers numeric conversions, etc.).
	targetType := reflect.TypeOf((*T)(nil)).Elem()
	v := reflect.ValueOf(val)
	if v.IsValid() {
		if v.Type().AssignableTo(targetType) {
			return changed, v.Interface().(T)
		}
		if v.Type().ConvertibleTo(targetType) {
			converted := v.Convert(targetType)
			return changed, converted.Interface().(T)
		}
	}

	// Fallback: JSON roundtrip for map/struct and other loosely typed values.
	if b, err := json.Marshal(val); err == nil {
		var out T
		if err := json.Unmarshal(b, &out); err == nil {
			return changed, out
		}
	}

	return changed, zero
}

// ConfigSubscriber handles config events
type ConfigSubscriber func(event ConfigEvent)

var (
	subscribersMu sync.RWMutex
	subscribers   []ConfigSubscriber
)

// Subscribe registers a subscriber for all config events.
func Subscribe(subscriber ConfigSubscriber) {
	subscribersMu.Lock()
	defer subscribersMu.Unlock()
	subscribers = append(subscribers, subscriber)
}

// publishEvent notifies all subscribers of a config change.
func publishEvent(oldVal, newVal map[string]any) {
	subscribersMu.RLock()
	defer subscribersMu.RUnlock()
	for _, sub := range subscribers {
		event := ConfigEvent{Old: oldVal, New: newVal}
		go sub(event)
	}
}
