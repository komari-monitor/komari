package cmd

import (
	"context"
	"os"
	"time"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

var PermitPasswordLoginCmd = &cobra.Command{
	Use:   "permit-login",
	Short: "Force permit password login",
	Long:  `Force permit password login`,
	Run: func(cmd *cobra.Command, args []string) {
		fxApp := fx.New(
			conf.FxModule(),
			dbcore.FxModule(),
			fx.NopLogger,
		)
		err := runFxWith(context.Background(), fxApp, 5*time.Second, func(ctx context.Context) error {
			db := dbcore.GetDBInstance()
			return db.Transaction(func(tx *gorm.DB) error {
				return tx.Model(&conf.V1Struct{}).Where("id = ?", 1).
					Update("disable_password_login", false).Error
			})
		})
		if err != nil {
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
