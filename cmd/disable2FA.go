package cmd

import (
	"os"

	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

var Disable2FA = &cobra.Command{
	Use:   "disable-2fa",
	Short: "Force disable 2FA",
	Long:  `Force disable 2FA`,
	Run: func(cmd *cobra.Command, args []string) {
		db := dbcore.GetDBInstance()
		err := db.Transaction(func(tx *gorm.DB) error {
			return tx.Model(&models.User{}).Where("two_factor != ?", "").
				Update("two_factor", "").Error
		})
		if err != nil {
			cmd.Println("Error:", err)
			os.Exit(1)
		}
		cmd.Println("2FA has been disabled.")
		cmd.Println("Please restart the server to apply the changes.")
	},
}

func init() {
	RootCmd.AddCommand(Disable2FA)
}
