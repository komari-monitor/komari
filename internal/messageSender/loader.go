package messageSender

import (
	"encoding/json"
	"fmt"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

func LoadProvider(name string, addition string) error {
	mu.Lock()
	defer mu.Unlock()
	constructor, exists := factory.GetConstructor(name)
	if !exists {
		return fmt.Errorf("message sender provider not found: %s", name)
	}

	provider := constructor()
	err := json.Unmarshal([]byte(addition), provider.GetConfiguration())
	if err != nil {
		return fmt.Errorf("failed to load config for provider %s: %w", name, err)
	}
	provider.Init()
	if currentProvider != nil {
		currentProvider.Destroy()
	}
	currentProvider = provider
	return nil
}

func GetProviderConfiguration(name string) (map[string]interface{}, error) {
	constructor, exists := factory.GetConstructor(name)
	if !exists {
		return nil, fmt.Errorf("message sender provider not found: %s", name)
	}

	provider := constructor()
	config := provider.GetConfiguration()

	// 将配置转换为map
	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal configuration: %w", err)
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(configBytes, &configMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal configuration: %w", err)
	}

	return configMap, nil
}
