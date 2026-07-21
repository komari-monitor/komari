package cmd

import (
	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the server",
	Long:  `Start the server`,
	Run: func(cmd *cobra.Command, args []string) {
		RunServer()
	},
}

func init() {
	// 从环境变量获取监听地址
	listenAddr := GetEnv("KOMARI_LISTEN", "0.0.0.0:25774")
	ServerCmd.PersistentFlags().StringVarP(&flags.Listen, "listen", "l", listenAddr, "监听地址 [env: KOMARI_LISTEN]")
	RootCmd.AddCommand(ServerCmd)
}

// RunServer 按显式的生命周期阶段启动服务端。
//
// 具体各阶段的职责与顺序见 App（cmd/app.go）。这里只负责串联：
// 任一初始化阶段失败即中止启动，避免在半初始化状态下对外提供服务。
func RunServer() {
	app := NewApp()
	if err := app.Bootstrap(); err != nil {
		_ = app.Shutdown()
		logger.Fatalf("server", "server startup failed at %q: %v", "bootstrap", err)
	}

	required, summary, err := app.LegacyUpgradeRequired()
	if err != nil {
		_ = app.Shutdown()
		logger.Fatalf("server", "server startup failed at %q: %v", "detect-1.2.7-upgrade", err)
	}
	if required {
		completed, err := app.RunLegacyUpgrade(summary)
		if err != nil {
			_ = app.Shutdown()
			logger.Fatalf("server", "server startup failed at %q: %v", "run-1.2.7-upgrade", err)
		}
		if !completed {
			return
		}
	}

	// 初始化阶段：任一步失败都不应继续对外服务。
	type stage struct {
		name string
		fn   func() error
	}
	stages := []stage{
		{"init-stores", app.InitStores},
		{"init-providers", app.InitProviders},
		{"start-background", app.StartBackground},
		{"build-router", app.BuildRouter},
	}
	for _, s := range stages {
		if err := s.fn(); err != nil {
			// 已登记的资源尽力回收，再退出。
			_ = app.Shutdown()
			logger.Fatalf("server", "server startup failed at %q: %v", s.name, err)
		}
	}

	if err := app.Run(); err != nil {
		logger.Fatalf("server", "server exited with error: %v", err)
	}
}
