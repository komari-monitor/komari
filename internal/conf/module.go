package conf

import (
	"context"
	"encoding/json"
	"os"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"
	"go.uber.org/fx"
)

// FxModule provides the configuration loader and associated startup side-effects.
//
// It loads configuration and also keeps the legacy global variable Conf updated
// to minimize downstream changes.
func FxModule() fx.Option {
	return fx.Options(
		fx.Provide(loadConfig),
		fx.Invoke(registerConfigEvents),
	)
}

func loadConfig() (*Config, error) {
	var cst *Config
	if _, err := os.Stat(flags.ConfigFile); os.IsNotExist(err) {
		// --- 初始化流程 ---
		installGuide()
		t := Default()
		cst = &t

		// 序列化默认配置
		b, err := json.MarshalIndent(cst, "", "  ")
		if err != nil {
			return nil, err
		}

		// 写入文件
		if err := os.WriteFile(flags.ConfigFile, b, 0644); err != nil {
			return nil, err
		}
	} else {
		// --- 读取流程 ---
		b, err := os.ReadFile(flags.ConfigFile)
		if err != nil {
			return nil, err
		}

		cst = &Config{}
		if err := json.Unmarshal(b, cst); err != nil {
			return nil, err
		}
	}

	ensureExtensionsDefaults(cst)
	Conf = cst
	return Conf, nil
}

func registerConfigEvents(lc fx.Lifecycle, cfg *Config) {
	// Keep legacy behavior: once the HTTP stack is initialized,
	// broadcast a ConfigUpdated to populate consumers (e.g. CORS, public index).
	lc.Append(fx.Hook{OnStart: func(_ctx context.Context) error {
		event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
			event.Trigger(eventType.ConfigUpdated, event.M{
				"old": Config{},
				"new": *cfg,
			})
			return nil
		}), event.Low)
		return nil
	}})
}
