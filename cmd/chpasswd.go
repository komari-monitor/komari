package cmd

import (
	"os"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/spf13/cobra"
)

var (
	//Username    string
	NewPassword string
)

var ChpasswdCmd = &cobra.Command{
	Use:     "chpasswd",
	Short:   "Force change password",
	Long:    `Force change password`,
	Example: `komari chpasswd -p <password>`,
	Run: func(cmd *cobra.Command, args []string) {
		if NewPassword == "" {
			cmd.Help()
			return
		}
		if _, err := os.Stat(conf.Conf.Database.DatabaseFile); os.IsNotExist(err) {
			cmd.Println("Database file does not exist.")
			return
		}
		user := &models.User{}
		dbcore.GetDBInstance().Model(&models.User{}).First(user)
		cmd.Println("Changing password for user:", user.Username)
		if err := accounts.ForceResetPassword(user.Username, NewPassword); err != nil {
			cmd.Println("Error:", err)
			return
		}
		cmd.Println("Password changed successfully, new password:", NewPassword)

		if err := accounts.DeleteAllSessions(); err != nil {
			cmd.Println("Unable to force logout of other devices:", err)
			return
		}

		cmd.Println("Please restart the server to apply the changes.")
	},
}

func init() {
	//ChpasswdCmd.PersistentFlags().StringVarP(&Username, "user", "u", "admin", "The username of the account to change password")
	ChpasswdCmd.PersistentFlags().StringVarP(&NewPassword, "password", "p", "", "New password")
	RootCmd.AddCommand(ChpasswdCmd)
}
