package cmd

import (
	"fmt"
	"os"

	"log/slog"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"

	"github.com/spf13/cobra"
)

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// 从环境变量获取默认值
var (
	configFileEnv = GetEnv("KOMARI_CONFIG_FILE", "./data/komari.json")
)

var RootCmd = &cobra.Command{
	Use:   "Komari",
	Short: "Komari is a simple server monitoring tool",
	Long: `Komari is a simple server monitoring tool. 
Made by Akizon77 with love.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.SetArgs([]string{"server"})
		cmd.Execute()
	},
}

func Execute() {
	detectDeprecatedFlags()
	// 创建目录
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		slog.Error("Failed to create theme directory", slog.Any("error", err))
	}
	err, _ := event.Trigger(eventType.ProcessStart, event.M{})
	if err != nil {
		slog.Error("Something went wrong during process start.", slog.Any("error", err))
		os.Exit(1)
	}
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// detectDeprecatedFlags 检测过时的数据库相关命令行参数
func detectDeprecatedFlags() {
	deprecatedFlags := map[string]bool{
		"db-host":  false,
		"database": false,
		"db-user":  false,
		"db-pass":  false,
		"db-port":  false,
		"db-name":  false,
		"db-type":  false,
	}

	// 检查命令行参数
	for _, arg := range os.Args[1:] {
		for flagName := range deprecatedFlags {
			if arg == "--"+flagName || arg == "-"+string(flagName[0]) {
				deprecatedFlags[flagName] = true
				break
			}
		}
	}

	// 如果检测到过时参数，记录警告
	hasDeprecated := false
	for flagName, found := range deprecatedFlags {
		if found {
			hasDeprecated = true
			slog.Warn("Deprecated command-line flag detected", slog.String("flag", "--"+flagName))
		}
	}

	if hasDeprecated {
		slog.Warn("Command-line database configuration flags are deprecated. " +
			"Please migrate to komari.json file. ")
	}
}

func init() {
	// 设置命令行参数，提供环境变量作为默认值
	RootCmd.PersistentFlags().StringVarP(&flags.ConfigFile, "config", "c", configFileEnv, "Configuration file path [env: KOMARI_CONFIG_FILE]")
	RootCmd.PersistentFlags().StringVarP(&flags.Listen, "listen", "l", GetEnv("KOMARI_LISTEN", ":8080"), "Listen address [env: KOMARI_LISTEN]")
}
