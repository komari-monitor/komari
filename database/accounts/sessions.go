package accounts

import (
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	messageevent "github.com/komari-monitor/komari/database/models/messageEvent"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/geoip"
	"github.com/komari-monitor/komari/utils/messageSender"
)

// GetAllSessions 获取所有会话
func GetAllSessions() ([]models.Session, error) {
	db := dbcore.GetDBInstance()
	var sessions []models.Session
	return sessions, db.Find(&sessions).Error
}

// CreateSession 创建新会话
func CreateSession(uuid string, expires int, userAgent, ip, login_method string) (string, error) {
	db := dbcore.GetDBInstance()
	session := utils.GenerateRandomString(32)

	sessionRecord := models.Session{
		UUID:         uuid,
		Session:      session,
		Expires:      time.Now().UTC().Add(time.Duration(expires) * time.Second),
		UserAgent:    userAgent,
		Ip:           ip,
		LoginMethod:  login_method,
		LatestOnline: time.Now().UTC(),
	}
	go func() {
		LoginNotification, _ := config.GetAs[bool](config.LoginNotificationKey, false)
		if LoginNotification {
			ipAddr := net.ParseIP(ip)
			ipinfo, _ := geoip.GetGeoInfo(ipAddr)
			loc := "unknown"
			if ipinfo != nil && ipinfo.Name != "" {
				loc = ipinfo.Name
			}
			_ = messageSender.SendNotification(models.EventMessage{
				Event:   messageevent.Login,
				Time:    time.Now().UTC(),
				Message: fmt.Sprintf("%s: %s (%s)\n%s", login_method, ip, loc, userAgent),
				Emoji:   "🔑",
			})
		}
	}()

	return session, db.Create(&sessionRecord).Error
}

// GetSession 根据会话 ID 获取 UUID
func GetSession(session string) (string, error) {
	db := dbcore.GetDBInstance()
	var sessionRecord models.Session
	if err := db.Where("session = ?", session).First(&sessionRecord).Error; err != nil {
		return "", err
	}

	if time.Now().UTC().After(sessionRecord.Expires) {
		// 会话已过期，删除它
		_ = DeleteSession(session)
		return "", errors.New("session expired")
	}

	return sessionRecord.UUID, nil
}

func GetUserBySession(session string) (models.User, error) {
	var sessionRecord models.Session
	if err := dbcore.GetDBInstance().Where("session = ?", session).First(&sessionRecord).Error; err != nil {
		return models.User{}, err
	}
	return GetUserByUUID(sessionRecord.UUID)
}

// DeleteSession 删除指定会话
func DeleteSession(session string) error {
	db := dbcore.GetDBInstance()
	return db.Where("session = ?", session).Delete(&models.Session{}).Error
}

func DeleteAllSessions() error {
	db := dbcore.GetDBInstance()
	return db.Where("1 = 1").Delete(&models.Session{}).Error
}

func UpdateLatest(session, useragent, ip string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.Session{}).Where("session = ?", session).Updates(map[string]interface{}{
		"latest_online":     time.Now().UTC(),
		"latest_user_agent": useragent,
		"latest_ip":         ip,
	}).Error
}

func RemoveExpiredSessions() error {
	db := dbcore.GetDBInstance()
	return db.Where("expires < ?", time.Now().UTC()).Delete(&models.Session{}).Error
}
