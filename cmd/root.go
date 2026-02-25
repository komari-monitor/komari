package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/komari-monitor/komari/cmd/flags"

	"github.com/spf13/cobra"
)

func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func GetEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i
	}
	return defaultValue
}

// 从环境变量获取默认值
var (
	dbTypeEnv = GetEnv("KOMARI_DB_TYPE", "sqlite")
	dbFileEnv = GetEnv("KOMARI_DB_FILE", "./data/komari.db")
	dbHostEnv = GetEnv("KOMARI_DB_HOST", "localhost")
	dbPortEnv = GetEnv("KOMARI_DB_PORT", "")
	dbUserEnv = GetEnv("KOMARI_DB_USER", "")
	dbPassEnv = GetEnv("KOMARI_DB_PASS", "")
	dbNameEnv = GetEnv("KOMARI_DB_NAME", "komari")

	// 资源限制默认值
	maxTerminalSessionsEnv = GetEnvInt("KOMARI_MAX_TERMINAL_SESSIONS", 100)
	maxWebSocketConnsEnv   = GetEnvInt("KOMARI_MAX_WEBSOCKET_CONNS", 500)
	maxRecordsCacheEnv     = GetEnvInt("KOMARI_MAX_RECORDS_CACHE", 10000)
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
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// 设置命令行参数，提供环境变量作为默认值
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseType, "db-type", "t", dbTypeEnv, "Database type [env: KOMARI_DB_TYPE]")
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseFile, "database", "d", dbFileEnv, "Database file path [env: KOMARI_DB_FILE]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseHost, "db-host", dbHostEnv, "Database host address [env: KOMARI_DB_HOST]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabasePort, "db-port", dbPortEnv, "Database port [env: KOMARI_DB_PORT]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseUser, "db-user", dbUserEnv, "Database username [env: KOMARI_DB_USER]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabasePass, "db-pass", dbPassEnv, "Database password [env: KOMARI_DB_PASS]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseName, "db-name", dbNameEnv, "Database name [env: KOMARI_DB_NAME]")

	// 资源限制参数
	RootCmd.PersistentFlags().IntVar(&flags.MaxTerminalSessions, "max-terminal-sessions", maxTerminalSessionsEnv, "Maximum terminal sessions [env: KOMARI_MAX_TERMINAL_SESSIONS]")
	RootCmd.PersistentFlags().IntVar(&flags.MaxWebSocketConns, "max-websocket-conns", maxWebSocketConnsEnv, "Maximum WebSocket connections [env: KOMARI_MAX_WEBSOCKET_CONNS]")
	RootCmd.PersistentFlags().IntVar(&flags.MaxRecordsCacheSize, "max-records-cache", maxRecordsCacheEnv, "Maximum records cache size [env: KOMARI_MAX_RECORDS_CACHE]")
}
