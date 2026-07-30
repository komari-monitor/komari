package messageSender

import (
	"encoding/json"
	"fmt"

	"github.com/komari-monitor/komari/utils/messageSender/factory"
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
