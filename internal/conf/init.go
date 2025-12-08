package conf

import (
	"encoding/json"
	"os"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	Conf = &Config{}
	// 以最高优先级启动程序时加载配置文件
	// Extensions的注册已经在相应模块的init中完成
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		if _, err := os.Stat(flags.ConfigFile); os.IsNotExist(err) {
			if err := Override(Default()); err != nil {
				return err
			}
		}
		b, err := os.ReadFile(flags.ConfigFile)
		if err != nil {
			return err
		}
		cst := &Config{}
		if err := json.Unmarshal(b, cst); err != nil {
			return err
		}
		// 确保 Extensions 包含所有已注册字段的默认值
		ensureExtensionsDefaults(cst)
		Conf = cst
		return nil
	}), event.Max+2)

	event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
		event.Trigger(eventType.ConfigUpdated, event.M{
			"old": Config{},
			"new": *Conf,
		})
		return nil
	}), event.Low)
}
