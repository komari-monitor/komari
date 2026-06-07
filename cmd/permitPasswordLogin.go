package cmd

import (
	"os"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/spf13/cobra"
)

var PermitPasswordLoginCmd = &cobra.Command{
	Use:   "permit-login",
	Short: "Force permit password login",
	Long:  `Force permit password login`,
	Run: func(cmd *cobra.Command, args []string) {
		dbcore.GetDBInstance()
		if err := config.Set(config.DisablePasswordLoginKey, false); err != nil {
			cmd.Println("Error:", err)
			os.Exit(1)
		}
		cmd.Println("Password login has been permitted.")
		cmd.Println("Please restart the server to apply the changes.")
	},
}

func init() {
	RootCmd.AddCommand(PermitPasswordLoginCmd)
}
