package conf

import (
	"encoding/json"
	"fmt"
	"sync"
)

// CustomFieldProvider 是自定义字段提供者需要实现的接口
type CustomFieldProvider interface {
	// Key 返回配置中的唯一键名（JSON 字段名）
	Key() string
	// Default 返回默认值
	Default() interface{}
	// Validate 验证配置值是否有效（可选，返回 nil 表示验证通过）
	Validate(value interface{}) error
}

// BaseFieldProvider 提供一个基础实现，简化自定义字段的创建
type BaseFieldProvider struct {
	key          string
	defaultValue interface{}
	validator    func(interface{}) error
}

// NewFieldProvider 创建一个基础字段提供者
func NewFieldProvider(key string, defaultValue interface{}, validator func(interface{}) error) *BaseFieldProvider {
	return &BaseFieldProvider{
		key:          key,
		defaultValue: defaultValue,
		validator:    validator,
	}
}

func (p *BaseFieldProvider) Key() string {
	return p.key
}

func (p *BaseFieldProvider) Default() interface{} {
	return p.defaultValue
}

func (p *BaseFieldProvider) Validate(value interface{}) error {
	if p.validator != nil {
		return p.validator(value)
	}
	return nil
}

// registry 存储所有注册的自定义字段提供者（只存储 provider，值存在 Conf.Extensions 中）
var (
	registryMu      sync.RWMutex
	customProviders = make(map[string]CustomFieldProvider)
)

// RegisterField 注册一个自定义字段提供者
// 应在 init() 函数中调用，以确保在配置加载前完成注册
func RegisterField(provider CustomFieldProvider) error {
	registryMu.Lock()
	defer registryMu.Unlock()

	key := provider.Key()
	if key == "" {
		return fmt.Errorf("custom field key cannot be empty")
	}

	if _, exists := customProviders[key]; exists {
		return fmt.Errorf("custom field '%s' is already registered", key)
	}

	customProviders[key] = provider
	return nil
}

// MustRegisterField 注册一个自定义字段提供者，失败时 panic
func MustRegisterField(provider CustomFieldProvider) {
	if err := RegisterField(provider); err != nil {
		panic(err)
	}
}

// RegisterSimpleField 简化的字段注册，无需实现接口
func RegisterSimpleField(key string, defaultValue interface{}) error {
	return RegisterField(NewFieldProvider(key, defaultValue, nil))
}

// MustRegisterSimpleField 简化的字段注册，失败时 panic
func MustRegisterSimpleField(key string, defaultValue interface{}) {
	MustRegisterField(NewFieldProvider(key, defaultValue, nil))
}

// GetExtension 获取扩展字段的值
func GetExtension(key string) (interface{}, bool) {
	if Conf == nil || Conf.Extensions == nil {
		return nil, false
	}
	val, ok := Conf.Extensions[key]
	return val, ok
}

// GetExtensionAs 获取扩展字段并转换为指定类型
// 使用示例: val, ok := GetExtensionAs[MyConfig]("my_module")
func GetExtensionAs[T any](key string) (T, bool) {
	val, ok := GetExtension(key)
	if !ok {
		var zero T
		return zero, false
	}

	// 尝试直接类型断言
	if typed, ok := val.(T); ok {
		return typed, true
	}

	// 尝试通过 JSON 反序列化转换
	var result T
	bytes, err := json.Marshal(val)
	if err != nil {
		var zero T
		return zero, false
	}
	if err := json.Unmarshal(bytes, &result); err != nil {
		var zero T
		return zero, false
	}

	return result, true
}

// SetExtension 设置扩展字段的值（不自动保存）
func SetExtension(key string, value interface{}) error {
	registryMu.RLock()
	provider, exists := customProviders[key]
	registryMu.RUnlock()

	if !exists {
		return fmt.Errorf("extension field '%s' is not registered", key)
	}

	if err := provider.Validate(value); err != nil {
		return fmt.Errorf("validation failed for field '%s': %w", key, err)
	}

	if Conf.Extensions == nil {
		Conf.Extensions = make(map[string]interface{})
	}
	Conf.Extensions[key] = value
	return nil
}

// SetExtensionAndSave 设置扩展字段的值并保存到文件
func SetExtensionAndSave(key string, value interface{}) error {
	if err := SetExtension(key, value); err != nil {
		return err
	}
	return Override(*Conf)
}

// GetAllExtensions 获取所有扩展字段
func GetAllExtensions() map[string]interface{} {
	if Conf == nil || Conf.Extensions == nil {
		return make(map[string]interface{})
	}

	result := make(map[string]interface{}, len(Conf.Extensions))
	for k, v := range Conf.Extensions {
		result[k] = v
	}
	return result
}

// GetRegisteredKeys 获取所有已注册的字段键名
func GetRegisteredKeys() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	keys := make([]string, 0, len(customProviders))
	for k := range customProviders {
		keys = append(keys, k)
	}
	return keys
}

// IsFieldRegistered 检查字段是否已注册
func IsFieldRegistered(key string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()

	_, exists := customProviders[key]
	return exists
}

// ResetExtension 重置扩展字段为默认值
func ResetExtension(key string) error {
	registryMu.RLock()
	provider, exists := customProviders[key]
	registryMu.RUnlock()

	if !exists {
		return fmt.Errorf("extension field '%s' is not registered", key)
	}

	if Conf.Extensions == nil {
		Conf.Extensions = make(map[string]interface{})
	}
	Conf.Extensions[key] = provider.Default()
	return nil
}

// ResetAllExtensions 重置所有扩展字段为默认值
func ResetAllExtensions() {
	registryMu.RLock()
	defer registryMu.RUnlock()

	if Conf.Extensions == nil {
		Conf.Extensions = make(map[string]interface{})
	}

	for key, provider := range customProviders {
		Conf.Extensions[key] = provider.Default()
	}
}

// ensureExtensionsDefaults 确保所有已注册的扩展字段都有值
func ensureExtensionsDefaults(cfg *Config) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	if cfg.Extensions == nil {
		cfg.Extensions = make(map[string]interface{})
	}

	for key, provider := range customProviders {
		if _, exists := cfg.Extensions[key]; !exists {
			cfg.Extensions[key] = provider.Default()
		}
	}
}

// GetExtensionDefault 获取扩展字段的默认值
func GetExtensionDefault(key string) (interface{}, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	provider, exists := customProviders[key]
	if !exists {
		return nil, false
	}
	return provider.Default(), true
}

// FromEventExtension 从 ConfigUpdated 事件中获取指定扩展字段的旧值和新值
// 返回 (oldValue, newValue, exists)
func FromEventExtension(e interface{ Get(string) interface{} }, key string) (interface{}, interface{}, bool) {
	oldConf, oldOk := e.Get("old").(Config)
	newConf, newOk := e.Get("new").(Config)

	if !oldOk || !newOk {
		return nil, nil, false
	}

	var oldVal, newVal interface{}
	var oldExists, newExists bool

	if oldConf.Extensions != nil {
		oldVal, oldExists = oldConf.Extensions[key]
	}
	if newConf.Extensions != nil {
		newVal, newExists = newConf.Extensions[key]
	}

	if !oldExists && !newExists {
		return nil, nil, false
	}

	return oldVal, newVal, true
}

// FromEventExtensionAs 从 ConfigUpdated 事件中获取指定扩展字段的旧值和新值（泛型版本）
// 使用示例: old, new, ok := FromEventExtensionAs[MyConfig](e, "my_module")
func FromEventExtensionAs[T any](e interface{ Get(string) interface{} }, key string) (T, T, bool) {
	oldVal, newVal, exists := FromEventExtension(e, key)
	if !exists {
		var zero T
		return zero, zero, false
	}

	convertTo := func(val interface{}) (T, bool) {
		if val == nil {
			var zero T
			return zero, true
		}
		if typed, ok := val.(T); ok {
			return typed, true
		}
		// JSON 转换
		var result T
		bytes, err := json.Marshal(val)
		if err != nil {
			var zero T
			return zero, false
		}
		if err := json.Unmarshal(bytes, &result); err != nil {
			var zero T
			return zero, false
		}
		return result, true
	}

	oldTyped, oldOk := convertTo(oldVal)
	newTyped, newOk := convertTo(newVal)

	if !oldOk || !newOk {
		var zero T
		return zero, zero, false
	}

	return oldTyped, newTyped, true
}

// ---- 兼容旧 API（已废弃，建议使用新的 Extension 系列函数）----

// GetCustomField 已废弃，请使用 GetExtension
func GetCustomField(key string) (interface{}, bool) {
	return GetExtension(key)
}

// GetCustomFieldAs 已废弃，请使用 GetExtensionAs
func GetCustomFieldAs[T any](key string) (T, bool) {
	return GetExtensionAs[T](key)
}

// SetCustomField 已废弃，请使用 SetExtension
func SetCustomField(key string, value interface{}) error {
	return SetExtension(key, value)
}

// SetCustomFieldAndSave 已废弃，请使用 SetExtensionAndSave
func SetCustomFieldAndSave(key string, value interface{}) error {
	return SetExtensionAndSave(key, value)
}
