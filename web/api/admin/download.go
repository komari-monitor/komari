package admin

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/web/api"
)

// copyFile 复制单个文件到目标路径（会确保父目录存在）
func copyFile(srcPath, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %v", err)
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %v", err)
	}
	defer src.Close()

	dest, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %v", err)
	}
	defer dest.Close()

	if _, err = io.Copy(dest, src); err != nil {
		return fmt.Errorf("failed to copy file: %v", err)
	}
	return nil
}

// walkDirToZip 将 contentDir 的内容写入 zip writer。
// 注意：contentDir 不应包含被 walk 的 zip 文件本身。
func walkDirToZip(zipWriter *zip.Writer, contentDir string) error {
	return filepath.Walk(contentDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(contentDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		zipPath := filepath.ToSlash(rel)
		if info.IsDir() {
			_, err := zipWriter.CreateHeader(&zip.FileHeader{
				Name:     zipPath + "/",
				Method:   zip.Deflate,
				Modified: info.ModTime(),
			})
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		w, err := zipWriter.CreateHeader(&zip.FileHeader{
			Name:     zipPath,
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		})
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		return err
	})
}

// writeBackupMarkup 追加备份标记文件到 zip。
func writeBackupMarkup(zipWriter *zip.Writer) error {
	now := time.Now().UTC()
	markupContent := "此文件为 Komari 备份标记文件，请勿删除。\nThis is a Komari backup markup file, please do not delete.\n\n备份时间 / Backup Time: " + now.Format(time.RFC3339Nano)
	markupWriter, err := zipWriter.CreateHeader(&zip.FileHeader{
		Name:     "komari-backup-markup",
		Method:   zip.Deflate,
		Modified: now,
	})
	if err != nil {
		return err
	}
	_, err = markupWriter.Write([]byte(markupContent))
	return err
}

// backupSQLiteTo 使用 SQLite VACUUM INTO 将当前数据库一致性备份到指定路径。
// destDBPath 会先删除以防 SQLite 报 "output file already exists"。
func backupSQLiteTo(destDBPath string) error {
	if err := os.MkdirAll(filepath.Dir(destDBPath), 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory for db: %v", err)
	}

	// 确保目标文件不存在（VACUUM INTO 要求目标文件不存在）
	_ = os.Remove(destDBPath)

	db := dbcore.GetDBInstance()
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database connection: %v", err)
	}

	// Windows 下 VACUUM INTO 传绝对路径时，统一使用正斜杠避免路径解析歧义
	safePath := filepath.ToSlash(destDBPath)
	safePath = strings.ReplaceAll(safePath, "'", "''")
	vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", safePath)
	if _, err = sqlDB.Exec(vacuumSQL); err != nil {
		return fmt.Errorf("sqlite VACUUM INTO failed: %v", err)
	}
	return nil
}

// DownloadBackup 使用白名单打包 ./data 及数据库文件为 zip 并下载，
// 同时归档到 ./data/backup/ 确保 Docker 挂载后备份文件可持久化。
//
// 归档文件由前端后续统一管理，服务端只负责生成并保存。
func DownloadBackup(c *gin.Context) {
	backupDir := filepath.Join(".", "data", "backup")

	// 1) 创建临时目录，内容隔离到 content/ 子目录
	tempDir, err := os.MkdirTemp("", "komari-backup-*")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating temporary directory: %v", err))
		return
	}
	defer os.RemoveAll(tempDir)

	contentDir := filepath.Join(tempDir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating content directory: %v", err))
		return
	}

	// 2) 复制白名单文件到 content 目录
	if err := copyWhitelistedFiles(contentDir); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error copying data to temp: %v", err))
		return
	}

	// 3) 处理数据库备份 -> content/komari.db
	destDB := filepath.Join(contentDir, "komari.db")
	dbFilePath := flags.DatabaseFile

	if flags.IsSQLite() {
		if err := backupSQLiteTo(destDB); err != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error backing up sqlite database: %v", err))
			return
		}
	} else if dbFilePath != "" {
		if _, err := os.Stat(dbFilePath); err == nil {
			if err := copyFile(dbFilePath, destDB); err != nil {
				api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error copying database file: %v", err))
				return
			}
		} else if !os.IsNotExist(err) {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error stating database file: %v", err))
			return
		}
	}

	// 4) 打包到临时 ZIP（放在 tempDir 下，与 content 平级）
	tempZipPath := filepath.Join(tempDir, "output.zip")
	tempZip, err := os.Create(tempZipPath)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating temp zip: %v", err))
		return
	}
	zipWriter := zip.NewWriter(tempZip)

	// 只 walk content 目录，避免 output.zip 被打包进去
	if err := walkDirToZip(zipWriter, contentDir); err != nil {
		zipWriter.Close()
		tempZip.Close()
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error archiving temp folder: %v", err))
		return
	}

	if err := writeBackupMarkup(zipWriter); err != nil {
		zipWriter.Close()
		tempZip.Close()
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing backup markup: %v", err))
		return
	}

	if err := zipWriter.Close(); err != nil {
		tempZip.Close()
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error finalizing zip: %v", err))
		return
	}
	tempZip.Close()

	// 5) 归档到 data/backup/。先写临时文件再原子发布。
	ts := time.Now().UTC().Format("20060102-150405.000000")
	archiveName := fmt.Sprintf("backup-%s.zip", ts)

	archivePath := filepath.Join(backupDir, archiveName)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating backup directory: %v", err))
		return
	}

	archiveTemp, err := os.CreateTemp(backupDir, ".backup-*.tmp")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating archive temp file: %v", err))
		return
	}
	archiveTempPath := archiveTemp.Name()
	if err := archiveTemp.Close(); err != nil {
		os.Remove(archiveTempPath)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error closing archive temp file: %v", err))
		return
	}
	defer os.Remove(archiveTempPath)
	if err := copyFile(tempZipPath, archiveTempPath); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error archiving backup: %v", err))
		return
	}
	if err := os.Rename(archiveTempPath, archivePath); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error publishing backup archive: %v", err))
		return
	}

	// 6) 发送给客户端
	c.Writer.Header().Set("Content-Type", "application/zip")
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", archiveName))

	zipReader, err := os.Open(tempZipPath)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error reading temp zip: %v", err))
		return
	}
	defer zipReader.Close()

	http.ServeContent(c.Writer, c.Request, archiveName, time.Now(), zipReader)
}
