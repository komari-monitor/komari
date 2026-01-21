package cmd

import (
	"context"
	"os"
	"time"

	"github.com/komari-monitor/komari/internal/app"
	"github.com/komari-monitor/komari/internal/scheduler"
	"github.com/komari-monitor/komari/internal/server"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the server",
	Long:  `Start the server`,
	Run: func(cmd *cobra.Command, args []string) {
		a := app.New().
			WithShutdownTimeout(5 * time.Second).
			With(server.NewHTTPModule()).
			With(scheduler.NewModule())
		if err := a.RunUntilSignal(context.Background()); err != nil {
			cmd.PrintErrln(err)
			os.Exit(1)
		}
	},
}

func init() {
	RootCmd.AddCommand(ServerCmd)
}
