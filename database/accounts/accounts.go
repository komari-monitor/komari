package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm"
)

const constantSalt = "06Wm4Jv1Hkxx"

// CheckPassword 检查密码，正确时返回用户 UUID 和 true
func CheckPassword(username, passwd string) (uuid string, ok bool) {
	db := dbcore.GetDBInstance()
	var user models.User
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		return "", false
	}
	if hashPasswd(passwd) != user.Passwd {
		return "", false
	}
	return user.UUID, true
}

// ForceResetPassword 强制重置用户密码
func ForceResetPassword(username, passwd string) error {
	db := dbcore.GetDBInstance()
	result := db.Model(&models.User{}).Where("username = ?", username).Update("passwd", hashPasswd(passwd))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("无法找到用户名")
	}
	return nil
}

// hashPasswd 对密码进行加盐哈希
func hashPasswd(passwd string) string {
	h := sha256.Sum256([]byte(passwd + constantSalt))
	return base64.StdEncoding.EncodeToString(h[:])
}

func CreateAccount(username, passwd string) (user models.User, err error) {
	return CreateAccountWithDB(dbcore.GetDBInstance(), username, passwd)
}

func CreateAccountWithDB(db *gorm.DB, username, passwd string) (models.User, error) {
	user := models.User{
		UUID:     uuid.New().String(),
		Username: username,
		Passwd:   hashPasswd(passwd),
	}
	return user, db.Create(&user).Error
}

func DeleteAccountByUsername(username string) (err error) {
	return DeleteAccountByUsernameWithDB(dbcore.GetDBInstance(), username)
}

func DeleteAccountByUsernameWithDB(db *gorm.DB, username string) error {
	return db.Where("username = ?", username).Delete(&models.User{}).Error
}

func GetUserByUUID(uuid string) (models.User, error) {
	var user models.User
	return user, dbcore.GetDBInstance().Where("uuid = ?", uuid).First(&user).Error
}

// GetUserBySSO 通过 SSO 信息获取用户
func GetUserBySSO(ssoID string) (models.User, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	if err := db.Where("sso_id = ?", ssoID).First(&user).Error; err != nil {
		return models.User{}, fmt.Errorf("用户不存在：%s", ssoID)
	}
	return user, nil
}

func BindingExternalAccount(uuid string, sso_id string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", sso_id).Error
}

func UnbindExternalAccount(uuid string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", "").Error
}

func UpdateUser(uuid string, name, password, sso_type *string) error {
	db := dbcore.GetDBInstance()
	// Check if user exists
	var existingUser models.User
	result := db.Where("uuid = ?", uuid).First(&existingUser)
	if result.Error != nil {
		return fmt.Errorf("user not found: %s", uuid)
	}
	updates := make(map[string]interface{})
	if name != nil {
		updates["username"] = *name
	}
	if password != nil {
		updates["passwd"] = hashPasswd(*password)
	}
	if sso_type != nil {
		updates["sso_type"] = *sso_type
	}
	updates["updated_at"] = time.Now().UTC()
	if err := db.Model(&models.User{}).Where("uuid = ?", uuid).Updates(updates).Error; err != nil {
		return err
	}
	if password != nil {
		DeleteAllSessions()
	}
	return nil
}
