package accounts

import (
	"image"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
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
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", secret).Error
}

func Verify2Fa(uuid, code string) (bool, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	if err := db.Where("uuid = ?", uuid).First(&user).Error; err != nil {
		return false, err
	}
	// 用户未启用2FA时视为校验失败
	return user.TwoFactor != "" && totp.Validate(code, user.TwoFactor), nil
}

func Disable2Fa(uuid string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", "").Error
}
