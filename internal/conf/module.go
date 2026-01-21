package conf

import (
	"encoding/json"
	"os"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/app"
	"github.com/komari-monitor/komari/internal/eventType"
)

type configModule struct{}

var _ app.Module = (*configModule)(nil)

// NewConfigModule provides the "config" module for the lifecycle app.
//
// It loads configuration and also keeps the legacy global variable Conf updated
// to minimize downstream changes.
func NewConfigModule() app.Module { return &configModule{} }

func (m *configModule) Name() string      { return "config" }
func (m *configModule) Depends() []string { return nil }

func (m *configModule) Provide(r app.Registry) error {
	return r.Provide(func() (*Config, error) {
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
	})
}

func (m *configModule) Hooks() app.Hooks {
	return app.Hooks{
		Init: func(_ *Config) error { return nil },
		Start: func(cfg *Config) {
			// Keep legacy behavior: once the HTTP stack is initialized,
			// broadcast a ConfigUpdated to populate consumers (e.g. CORS, public index).
			event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
				event.Trigger(eventType.ConfigUpdated, event.M{
					"old": Config{},
					"new": *cfg,
				})
				return nil
			}), event.Low)
		},
	}
}

func init() {
	app.RegisterModuleFactory("config", NewConfigModule)
}
