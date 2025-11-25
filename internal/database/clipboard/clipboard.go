package clipboard

import (
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
)

// CreateClipboard 创建剪贴板记录
func CreateClipboard(cb *models.Clipboard) error {
	db := dbcore.GetDBInstance()
	return db.Create(cb).Error
}

// GetClipboardByID 根据ID获取剪贴板记录
func GetClipboardByID(id int) (*models.Clipboard, error) {
	var cb models.Clipboard
	db := dbcore.GetDBInstance()
	if err := db.First(&cb, id).Error; err != nil {
		return nil, err
	}
	return &cb, nil
}

// UpdateClipboardFields 更新剪贴板记录
func UpdateClipboardFields(id int, fields map[string]interface{}) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.Clipboard{}).Where("id = ?", id).Updates(fields).Error
}

// DeleteClipboard 删除剪贴板记录
func DeleteClipboard(id int) error {
	db := dbcore.GetDBInstance()
	// Check if record exists first
	var cb models.Clipboard
	if err := db.First(&cb, id).Error; err != nil {
		return err // Record not found or other error
	}
	return db.Delete(&cb).Error
}

// DeleteClipboardBatch 批量删除剪贴板记录
func DeleteClipboardBatch(ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	db := dbcore.GetDBInstance()
	return db.Where("id IN ?", ids).Delete(&models.Clipboard{}).Error
}

// ListClipboard 列出所有剪贴板记录
func ListClipboard() ([]models.Clipboard, error) {
	var list []models.Clipboard
	db := dbcore.GetDBInstance()
	if err := db.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
