package cmd

import (
	"fmt"
	"os"

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

// 从环境变量获取默认值
var (
	dbTypeEnv = GetEnv("KOMARI_DB_TYPE", "sqlite")
	dbFileEnv = GetEnv("KOMARI_DB_FILE", "./data/komari.db")
	dbDSNEnv  = GetEnv("KOMARI_DB_DSN", "")
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
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseType, "db-type", "t", dbTypeEnv, "Database type: sqlite, postgres [env: KOMARI_DB_TYPE]")
	RootCmd.PersistentFlags().StringVarP(&flags.DatabaseFile, "database", "d", dbFileEnv, "SQLite database file path [env: KOMARI_DB_FILE]")
	RootCmd.PersistentFlags().StringVar(&flags.DatabaseDSN, "db-dsn", dbDSNEnv, `PostgreSQL DSN, e.g. "host=... port=... user=... password=... dbname=... sslmode=disable" [env: KOMARI_DB_DSN]`)
}
