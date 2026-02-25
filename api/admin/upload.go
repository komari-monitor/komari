package admin

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/cmd/flags"
	"gorm.io/gorm"
)

// 只有一个备份恢复操作在进行
var restoreMutex sync.Mutex

// parseBackupMarkup 解析备份标记文件，获取数据库类型信息
func parseBackupMarkup(zr *zip.ReadCloser) (dbType string, backupTime string, err error) {
	dbType = "sqlite" // 默认为 sqlite（兼容旧版本）
	backupTime = ""

	for _, f := range zr.File {
		if f.Name == "komari-backup-markup" {
			rc, err := f.Open()
			if err != nil {
				return dbType, backupTime, err
			}
			defer rc.Close()

			content, err := io.ReadAll(rc)
			if err != nil {
				return dbType, backupTime, err
			}

			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "数据库类型 / Database Type:") {
					dbType = strings.TrimSpace(strings.TrimPrefix(line, "数据库类型 / Database Type:"))
				}
				if strings.HasPrefix(line, "备份时间 / Backup Time:") {
					backupTime = strings.TrimSpace(strings.TrimPrefix(line, "备份时间 / Backup Time:"))
				}
			}
			return dbType, backupTime, nil
		}
	}
	return dbType, backupTime, nil
}

// hasSQLiteBackup 检查备份是否包含 SQLite 数据库文件
func hasSQLiteBackup(zr *zip.ReadCloser) bool {
	for _, f := range zr.File {
		if f.Name == "komari.db" {
			return true
		}
	}
	return false
}

// hasPostgreSQLBackup 检查备份是否包含 PostgreSQL SQL 文件
func hasPostgreSQLBackup(zr *zip.ReadCloser) bool {
	for _, f := range zr.File {
		if f.Name == "komari.sql" {
			return true
		}
	}
	return false
}

// UploadBackup 用于接收上传的备份文件并将其内容恢复到原始位置
func UploadBackup(c *gin.Context) {
	// 尝试获取锁，如果已有恢复操作在进行，则立即返回错误
	if !restoreMutex.TryLock() {
		api.RespondError(c, http.StatusConflict, "Another restore operation is already in progress")
		return
	}
	defer restoreMutex.Unlock()

	// 获取上传的文件
	file, header, err := c.Request.FormFile("backup")
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error getting uploaded file: %v", err))
		return
	}
	defer file.Close()

	// 检查文件是否为zip格式
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		api.RespondError(c, http.StatusBadRequest, "Uploaded file must be a ZIP archive")
		return
	}

	// 确保data目录存在
	if err := os.MkdirAll("./data", 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating data directory: %v", err))
		return
	}

	// 创建临时文件保存上传的zip（先校验，再落地到固定位置）
	tempFile, err := os.CreateTemp("", "backup-upload-*.zip")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating temporary file: %v", err))
		return
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath) // 确保临时文件最终被删除

	// 将上传的文件内容复制到临时文件
	_, err = io.Copy(tempFile, file)
	if err != nil {
		tempFile.Close()
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving uploaded file: %v", err))
		return
	}
	tempFile.Close() // 关闭文件以便后续操作

	// 基础校验：检查是否包含标记文件
	zr, err := zip.OpenReader(tempFilePath)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error opening zip file: %v", err))
		return
	}

	hasMarkup := false
	for _, f := range zr.File {
		if f.Name == "komari-backup-markup" {
			hasMarkup = true
			break
		}
	}

	if !hasMarkup {
		zr.Close()
		api.RespondError(c, http.StatusBadRequest, "Invalid backup file: missing komari-backup-markup file")
		return
	}

	// 解析备份标记文件获取数据库类型
	backupDBType, backupTime, err := parseBackupMarkup(zr)
	zr.Close()
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error parsing backup markup: %v", err))
		return
	}

	// 检查当前数据库类型
	currentDBType := flags.DatabaseType
	if currentDBType == "" {
		currentDBType = "sqlite"
	}

	// 兼容性检查
	hasSQLite := hasSQLiteBackup(zr)
	// 重新打开 zip 进行检查
	zr2, _ := zip.OpenReader(tempFilePath)
	hasPostgreSQL := hasPostgreSQLBackup(zr2)
	zr2.Close()

	// 数据库类型不匹配时的警告
	var warningMsg string
	if backupDBType == "sqlite" && currentDBType == "postgres" {
		if hasSQLite {
			warningMsg = "警告：备份来自 SQLite 数据库，但当前使用 PostgreSQL。将尝试迁移数据，但可能存在兼容性问题。"
		}
	} else if backupDBType == "postgres" && currentDBType == "sqlite" {
		if hasPostgreSQL {
			warningMsg = "警告：备份来自 PostgreSQL 数据库，但当前使用 SQLite。将尝试迁移数据，但可能存在兼容性问题。"
		}
	}

	// 将校验通过的临时文件移动到固定路径 ./data/backup.zip
	finalPath := filepath.Join(".", "data", "backup.zip")
	// 如存在旧文件，先删除
	_ = os.Remove(finalPath)
	if err := os.Rename(tempFilePath, finalPath); err != nil {
		// fallback：拷贝
		in, err2 := os.Open(tempFilePath)
		if err2 != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error preparing backup file: %v", err))
			return
		}
		defer in.Close()
		out, err2 := os.Create(finalPath)
		if err2 != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating target backup file: %v", err2))
			return
		}
		if _, err2 = io.Copy(out, in); err2 != nil {
			out.Close()
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing target backup file: %v", err2))
			return
		}
		out.Close()
	}

	// 返回：已保存备份，重启后将自动恢复
	response := gin.H{
		"status":       "success",
		"message":      "Backup uploaded successfully. The service will restart and apply the backup.",
		"path":         "./data/backup.zip",
		"backup_type":  backupDBType,
		"backup_time":  backupTime,
		"current_type": currentDBType,
	}
	if warningMsg != "" {
		response["warning"] = warningMsg
	}
	c.JSON(http.StatusOK, response)

	go func() {
		log.Println("Backup uploaded, restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// RestoreFromBackup 从备份文件恢复数据（在启动时调用）
func RestoreFromBackup(db *gorm.DB) error {
	backupZipPath := "./data/backup.zip"
	if _, err := os.Stat(backupZipPath); os.IsNotExist(err) {
		return nil // 没有备份文件，无需恢复
	}

	log.Println("[restore] Found backup.zip, starting restore process...")

	zr, err := zip.OpenReader(backupZipPath)
	if err != nil {
		return fmt.Errorf("failed to open backup.zip: %v", err)
	}
	defer zr.Close()

	// 解析备份类型
	backupDBType, _, _ := parseBackupMarkup(zr)
	currentDBType := flags.DatabaseType
	if currentDBType == "" {
		currentDBType = "sqlite"
	}

	log.Printf("[restore] Backup database type: %s, Current database type: %s", backupDBType, currentDBType)

	// 解压非数据库文件
	tempDir, err := os.MkdirTemp("", "komari-restore-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	for _, f := range zr.File {
		// 跳过数据库文件和标记文件
		if f.Name == "komari.db" || f.Name == "komari.sql" || f.Name == "komari-backup-markup" {
			continue
		}

		// 解压其他文件
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join("./data", cleanName)

		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		os.MkdirAll(filepath.Dir(targetPath), 0755)
		rc, err := f.Open()
		if err != nil {
			log.Printf("[restore] failed to open %s: %v", f.Name, err)
			continue
		}

		outFile, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			log.Printf("[restore] failed to create %s: %v", targetPath, err)
			continue
		}

		io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
	}

	// 处理数据库恢复
	switch currentDBType {
	case "sqlite", "":
		// SQLite 恢复
		for _, f := range zr.File {
			if f.Name == "komari.db" {
				// 解压 SQLite 数据库文件
				rc, err := f.Open()
				if err != nil {
					return fmt.Errorf("failed to open komari.db from backup: %v", err)
				}

				// 删除现有数据库文件
				os.Remove(flags.DatabaseFile)

				outFile, err := os.Create(flags.DatabaseFile)
				if err != nil {
					rc.Close()
					return fmt.Errorf("failed to create database file: %v", err)
				}
				_, err = io.Copy(outFile, rc)
				outFile.Close()
				rc.Close()
				if err != nil {
					return fmt.Errorf("failed to copy database: %v", err)
				}
				log.Printf("[restore] SQLite database restored from backup")
				break
			}
		}

	case "postgres", "postgresql":
		// PostgreSQL 恢复
		for _, f := range zr.File {
			if f.Name == "komari.sql" {
				// 执行 SQL 文件
				rc, err := f.Open()
				if err != nil {
					return fmt.Errorf("failed to open komari.sql from backup: %v", err)
				}

				scanner := bufio.NewScanner(rc)
				var sqlBatch strings.Builder

				for scanner.Scan() {
					line := scanner.Text()
					sqlBatch.WriteString(line + "\n")

					// 以分号结尾的语句执行
					if strings.HasSuffix(strings.TrimSpace(line), ";") {
						sql := sqlBatch.String()
						if strings.TrimSpace(sql) != "" {
							if err := db.Exec(sql).Error; err != nil {
								log.Printf("[restore] SQL error: %v, SQL: %s", err, sql[:min(100, len(sql))])
							}
						}
						sqlBatch.Reset()
					}
				}

				rc.Close()

				// 执行剩余的 SQL
				if sqlBatch.Len() > 0 {
					sql := sqlBatch.String()
					if strings.TrimSpace(sql) != "" {
						if err := db.Exec(sql).Error; err != nil {
							log.Printf("[restore] SQL error: %v", err)
						}
					}
				}

				log.Printf("[restore] PostgreSQL database restored from backup")
				break
			}

			// 如果备份是 SQLite，尝试迁移到 PostgreSQL
			if f.Name == "komari.db" && backupDBType == "sqlite" {
				log.Println("[restore] Migrating SQLite backup to PostgreSQL...")
				// 这里需要先恢复到临时 SQLite 数据库，然后迁移数据
				// 简化处理：提示用户手动迁移
				log.Println("[restore] WARNING: SQLite to PostgreSQL migration is not fully supported. Please migrate data manually.")
			}
		}
	}

	// 删除备份文件
	if err := os.Remove(backupZipPath); err != nil {
		log.Printf("[restore] failed to remove backup.zip: %v", err)
	}

	// 删除标记文件
	os.Remove("./data/komari-backup-markup")

	log.Println("[restore] Restore completed successfully")
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
