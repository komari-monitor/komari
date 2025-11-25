package accounts

import (
	"image"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/pquerna/otp/totp"
)

var (
	TwoFactorIssuer = "Komari Monitor"
)

func Generate2Fa() (string, image.Image, error) {
	otp, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TwoFactorIssuer,
		AccountName: "komari",
	})
	if err != nil {
		return "", nil, err
	}
	img, err := otp.Image(250, 250)
	if err != nil {
		return "", nil, err
	}
	return otp.Secret(), img, nil
}

func Enable2Fa(uuid, secret string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", secret).Error
	if err != nil {
		event.Trigger(eventType.UserTwoFaAdded, event.M{
			"user": uuid,
		})
		return err
	}
	return nil
}

func Verify2Fa(uuid, code string) (bool, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	err := db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return false, err
	}

	if user.TwoFactor == "" {
		return false, nil // 用户未启用2FA
	}

	valid := totp.Validate(code, user.TwoFactor)
	if !valid {
		return false, nil
	}

	return true, nil
}

func Disable2Fa(uuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", "").Error
	if err != nil {
		return err
	}
	event.Trigger(eventType.UserTwoFaRemoved, event.M{
		"user": uuid,
	})
	return nil
}
