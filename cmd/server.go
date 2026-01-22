package cmd

import (
	"context"
	"os"
	"time"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/internal/scheduler"
	"github.com/komari-monitor/komari/internal/server"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
)

var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the server",
	Long:  `Start the server`,
	Run: func(cmd *cobra.Command, args []string) {
		fxApp := fx.New(
			conf.FxModule(),
			dbcore.FxModule(),
			server.FxModule(),
			scheduler.FxModule(),
			fx.NopLogger,
		)
		if err := runFxUntilSignal(context.Background(), fxApp, 5*time.Second); err != nil {
			cmd.PrintErrln(err)
			os.Exit(1)
		}
	},
}

func init() {
	RootCmd.AddCommand(ServerCmd)
}
