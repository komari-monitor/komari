package accounts

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12 // bcrypt cost 参数，12 是推荐的安全值

// 旧版 SHA256 的固定盐（用于向后兼容验证）
const legacySalt = "06Wm4Jv1Hkxx"

// CheckPassword 检查密码是否正确
// 返回: uuid, success, needsMigration
// needsMigration 表示密码使用旧版哈希，需要前端提示用户修改密码
func CheckPassword(username, passwd string) (uuid string, success bool, needsMigration bool) {
	db := dbcore.GetDBInstance()
	var user models.User
	result := db.Where("username = ?", username).First(&user)
	if result.Error != nil {
		// 静默处理错误，不显示日志
		return "", false, false
	}

	// 检查密码并判断是否需要迁移
	valid, migrationNeeded := checkPasswordHashWithMigration(passwd, user.Passwd)
	if !valid {
		return "", false, false
	}

	// 如果需要迁移，在后台静默升级密码哈希
	if migrationNeeded {
		go migratePasswordHash(user.UUID, passwd)
	}

	return user.UUID, true, migrationNeeded
}

// migratePasswordHash 静默迁移密码哈希到 bcrypt
func migratePasswordHash(uuid, passwd string) {
	db := dbcore.GetDBInstance()
	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return
	}
	db.Model(&models.User{}).Where("uuid = ?", uuid).Update("passwd", hashedPassword)
}

// ForceResetPassword 强制重置用户密码
func ForceResetPassword(username, passwd string) (err error) {
	db := dbcore.GetDBInstance()
	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return fmt.Errorf("密码哈希失败: %v", err)
	}
	result := db.Model(&models.User{}).Where("username = ?", username).Update("passwd", hashedPassword)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("无法找到用户名")
	}
	return nil
}

// hashPasswd 使用 bcrypt 对密码进行哈希
func hashPasswd(passwd string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(passwd), bcryptCost)
	return string(bytes), err
}

// checkPasswordHashWithMigration 验证密码是否匹配哈希值
// 返回: valid, needsMigration
// 支持旧版 SHA256 哈希（向后兼容）和新的 bcrypt 哈希
func checkPasswordHashWithMigration(passwd, hash string) (bool, bool) {
	// 检查是否是 bcrypt 哈希（bcrypt 哈希以 $2a$ 或 $2b$ 开头）
	if len(hash) > 4 && (hash[:4] == "$2a$" || hash[:4] == "$2b$") {
		err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(passwd))
		return err == nil, false
	}

	// 向后兼容：旧版 SHA256 哈希验证
	if validateLegacyHash(passwd, hash) {
		return true, true // 验证成功，但需要迁移
	}

	return false, false
}

// validateLegacyHash 使用旧版 SHA256 方式验证密码
func validateLegacyHash(passwd, hash string) bool {
	// 旧版哈希计算方式: base64(sha256(passwd + salt))
	saltedPassword := passwd + legacySalt
	h := sha256.New()
	h.Write([]byte(saltedPassword))
	return base64.StdEncoding.EncodeToString(h.Sum(nil)) == hash
}

func CreateAccount(username, passwd string) (user models.User, err error) {
	db := dbcore.GetDBInstance()
	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return models.User{}, fmt.Errorf("密码哈希失败: %v", err)
	}
	user = models.User{
		UUID:     uuid.New().String(),
		Username: username,
		Passwd:   hashedPassword,
	}
	err = db.Create(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func DeleteAccountByUsername(username string) (err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("username = ?", username).Delete(&models.User{}).Error
	if err != nil {
		return err
	}
	return nil
}

// 创建默认管理员账户，使用环境变量 ADMIN_USERNAME 作为用户名，环境变量 ADMIN_PASSWORD 作为密码
func CreateDefaultAdminAccount() (username, passwd string, err error) {
	db := dbcore.GetDBInstance()

	username = os.Getenv("ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}

	passwd = os.Getenv("ADMIN_PASSWORD")
	if passwd == "" {
		passwd = utils.GeneratePassword()
	}

	hashedPassword, err := hashPasswd(passwd)
	if err != nil {
		return "", "", fmt.Errorf("密码哈希失败: %v", err)
	}

	user := models.User{
		UUID:      uuid.New().String(),
		Username:  username,
		Passwd:    hashedPassword,
		SSOID:     "",
		CreatedAt: models.FromTime(time.Now()),
		UpdatedAt: models.FromTime(time.Now()),
	}

	err = db.Create(&user).Error
	if err != nil {
		return "", "", err
	}

	return username, passwd, nil
}

func GetUserByUUID(uuid string) (user models.User, err error) {
	db := dbcore.GetDBInstance()
	err = db.Where("uuid = ?", uuid).First(&user).Error
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

// 通过 SSO 信息获取用户
func GetUserBySSO(ssoID string) (user models.User, err error) {
	db := dbcore.GetDBInstance()

	// 首先尝试查找已存在的用户
	err = db.Where("sso_id = ?", ssoID).First(&user).Error
	if err == nil {
		return user, nil
	}

	// 如果找不到用户，返回明确的错误信息
	return models.User{}, fmt.Errorf("用户不存在：%s", ssoID)
}

func BindingExternalAccount(uuid string, sso_id string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", sso_id).Error
	if err != nil {
		return err
	}
	return nil
}

func UnbindExternalAccount(uuid string) error {
	db := dbcore.GetDBInstance()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Update("sso_id", "").Error
	if err != nil {
		return err
	}
	return nil
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
		hashedPassword, err := hashPasswd(*password)
		if err != nil {
			return fmt.Errorf("密码哈希失败: %v", err)
		}
		updates["passwd"] = hashedPassword
	}
	if sso_type != nil {
		updates["sso_type"] = *sso_type
	}
	updates["updated_at"] = time.Now()
	err := db.Model(&models.User{}).Where("uuid = ?", uuid).Updates(updates).Error
	if err != nil {
		return err
	}
	if password != nil {
		DeleteAllSessions()
	}
	return nil
}
