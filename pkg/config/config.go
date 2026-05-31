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

// GetAs 获取并转换为指定类型 (泛型)，支持数值类型自动转换
func GetAs[T any](key string, defaul ...any) (T, error) {
	var t T
	var item ConfigItem

	err := db.First(&item, "key = ?", key).Error
	if err != nil {
		if len(defaul) > 0 {
			// 尝试直接类型断言
			if v, ok := defaul[0].(T); ok {
				err = Set(key, v)
				return v, err
			}
			// 尝试类型转换
			val := reflect.ValueOf(&t).Elem()
			if err := convertAndSet(defaul[0], val); err != nil {
				return t, fmt.Errorf("default value type mismatch: expected %T, got %T", t, defaul[0])
			}
			err = Set(key, t)
			return t, err
		}
		return t, err
	}

	// 先尝试直接反序列化
	if err = json.Unmarshal([]byte(item.Value), &t); err != nil {
		// 尝试通用解析后转换
		var generic any
		if err := json.Unmarshal([]byte(item.Value), &generic); err != nil {
			return t, err
		}
		val := reflect.ValueOf(&t).Elem()
		if err := convertAndSet(generic, val); err != nil {
			return t, err
		}
	}
	return t, nil
}

// GetMany 获取多个配置项，keys 为 map[key]defaultValue
// 如果 defaultValue 为 nil，则数据库不存在时不写入
// 如果 defaultValue 不为 nil，则数据库不存在时写入默认值
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

	// 收集需要写入数据库的默认值
	var toInsert []ConfigItem
	for k, def := range keys {
		if _, found := foundKeys[k]; !found {
			if def != nil {
				result[k] = def
				// 序列化后加入待写入列表
				jsonBytes, err := json.Marshal(def)
				if err != nil {
					slog.Warn("marshal default value failed", "key", k, "error", err)
					continue
				}
				toInsert = append(toInsert, ConfigItem{
					Key:   k,
					Value: string(jsonBytes),
				})
			}
		}
	}

	// 批量写入默认值到数据库
	if len(toInsert) > 0 {
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&toInsert).Error; err != nil {
			slog.Warn("batch insert default config failed", "error", err)
		}
	}

	return result, nil
}

// GetManyAs 将多个配置项映射到一个结构体中，json tag 作为 Key
// 支持 default tag 作为默认值，如果数据库中不存在且有 default tag 则写入数据库
// 没有 default tag 的字段使用零值，不写入数据库
func GetManyAs[T any]() (*T, error) {
	var t T
	val := reflect.ValueOf(&t).Elem()
	typ := val.Type()

	type fieldInfo struct {
		index      int
		key        string
		hasDefault bool
		defaultVal string
	}

	fields := make([]fieldInfo, 0)
	keys := make([]string, 0)

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		// 解析 json tag，处理 "key,omitempty" 格式
		key := strings.Split(jsonTag, ",")[0]
		if key == "" || key == "-" {
			continue
		}

		defaultTag := field.Tag.Get("default")
		hasDefault := defaultTag != "" || field.Tag.Get("default") == ""
		// 检查是否显式定义了 default tag (即使值为空)
		_, hasDefault = field.Tag.Lookup("default")

		fields = append(fields, fieldInfo{
			index:      i,
			key:        key,
			hasDefault: hasDefault,
			defaultVal: defaultTag,
		})
		keys = append(keys, key)
	}

	if len(keys) == 0 {
		return &t, nil
	}

	var items []ConfigItem
	if err := db.Where("key IN ?", keys).Find(&items).Error; err != nil {
		return nil, err
	}

	// 建立数据库中存在的 key 映射
	foundItems := make(map[string]string) // key -> value
	for _, item := range items {
		foundItems[item.Key] = item.Value
	}

	// 需要写入数据库的新配置项
	var toInsert []ConfigItem

	for _, fi := range fields {
		fieldVal := val.Field(fi.index)
		if !fieldVal.CanSet() {
			continue
		}

		if dbValue, found := foundItems[fi.key]; found {
			// 数据库中存在，使用数据库值
			if err := unmarshalToField(dbValue, fieldVal); err != nil {
				slog.Warn("unmarshal config failed", "key", fi.key, "error", err)
			}
		} else if fi.hasDefault {
			// 数据库中不存在，但有 default tag，解析默认值并写入数据库
			if err := parseDefaultToField(fi.defaultVal, fieldVal); err != nil {
				slog.Warn("parse default value failed", "key", fi.key, "error", err)
				continue
			}
			// 序列化后写入数据库
			jsonBytes, err := json.Marshal(fieldVal.Interface())
			if err != nil {
				slog.Warn("marshal default value failed", "key", fi.key, "error", err)
				continue
			}
			toInsert = append(toInsert, ConfigItem{
				Key:   fi.key,
				Value: string(jsonBytes),
			})
		}
		// 没有 default tag 且数据库中不存在，保持零值，不写入数据库
	}

	// 批量写入默认值到数据库
	if len(toInsert) > 0 {
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&toInsert).Error; err != nil {
			slog.Warn("batch insert default config failed", "error", err)
		}
	}

	return &t, nil
}

// unmarshalToField 将 JSON 字符串反序列化到字段，支持数值类型转换
func unmarshalToField(jsonStr string, fieldVal reflect.Value) error {
	target := reflect.New(fieldVal.Type()).Interface()
	if err := json.Unmarshal([]byte(jsonStr), target); err != nil {
		// 尝试通用解析后转换
		var generic any
		if err := json.Unmarshal([]byte(jsonStr), &generic); err != nil {
			return err
		}
		return convertAndSet(generic, fieldVal)
	}
	fieldVal.Set(reflect.ValueOf(target).Elem())
	return nil
}

// parseDefaultToField 解析 default tag 值到字段
func parseDefaultToField(defaultVal string, fieldVal reflect.Value) error {
	kind := fieldVal.Kind()

	switch kind {
	case reflect.String:
		fieldVal.SetString(defaultVal)
	case reflect.Bool:
		fieldVal.SetBool(defaultVal == "true" || defaultVal == "1")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var v int64
		if defaultVal != "" {
			if _, err := fmt.Sscanf(defaultVal, "%d", &v); err != nil {
				// 尝试解析浮点数后转换
				var f float64
				if _, err := fmt.Sscanf(defaultVal, "%f", &f); err != nil {
					return err
				}
				v = int64(f)
			}
		}
		fieldVal.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var v uint64
		if defaultVal != "" {
			if _, err := fmt.Sscanf(defaultVal, "%d", &v); err != nil {
				var f float64
				if _, err := fmt.Sscanf(defaultVal, "%f", &f); err != nil {
					return err
				}
				v = uint64(f)
			}
		}
		fieldVal.SetUint(v)
	case reflect.Float32, reflect.Float64:
		var v float64
		if defaultVal != "" {
			if _, err := fmt.Sscanf(defaultVal, "%f", &v); err != nil {
				return err
			}
		}
		fieldVal.SetFloat(v)
	default:
		// 对于复杂类型，尝试 JSON 解析
		if defaultVal == "" {
			return nil // 保持零值
		}
		target := reflect.New(fieldVal.Type()).Interface()
		if err := json.Unmarshal([]byte(defaultVal), target); err != nil {
			return err
		}
		fieldVal.Set(reflect.ValueOf(target).Elem())
	}
	return nil
}

// convertAndSet 通用类型转换并设置字段值
func convertAndSet(val any, fieldVal reflect.Value) error {
	if val == nil {
		return nil
	}

	targetType := fieldVal.Type()
	v := reflect.ValueOf(val)

	// 直接类型匹配
	if v.Type().AssignableTo(targetType) {
		fieldVal.Set(v)
		return nil
	}

	// 类型可转换
	if v.Type().ConvertibleTo(targetType) {
		fieldVal.Set(v.Convert(targetType))
		return nil
	}

	// 数值类型特殊处理 (JSON 数字默认解析为 float64)
	if f, ok := val.(float64); ok {
		switch fieldVal.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fieldVal.SetInt(int64(f))
			return nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fieldVal.SetUint(uint64(f))
			return nil
		case reflect.Float32, reflect.Float64:
			fieldVal.SetFloat(f)
			return nil
		}
	}

	// JSON 回环转换
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	target := reflect.New(targetType).Interface()
	if err := json.Unmarshal(b, target); err != nil {
		return err
	}
	fieldVal.Set(reflect.ValueOf(target).Elem())
	return nil
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
