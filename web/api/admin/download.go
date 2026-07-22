package admin

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
// 短时去重：一分钟内重复请求直接返回最近备份，避免网络不佳或下载管理器重复调用
// TODO: 后续需前端配合改为自动获取备份列表并展示的方式 弃用旧版本函数
func DownloadBackup(c *gin.Context) {
	backupDir := filepath.Join(".", "data", "backup")

	// 0) 短时去重：检查最近一分钟 backup-*.zip 是否在窗口内
	const dedupWindow = 60 * time.Second
	if entries, readErr := os.ReadDir(backupDir); readErr == nil {
		var latest os.DirEntry
		var latestTime time.Time
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			if e.IsDir() || !strings.HasPrefix(name, "backup-") || !strings.HasSuffix(name, ".zip") {
				continue
			}
			info, _ := e.Info()
			if info != nil && info.ModTime().After(latestTime) {
				latestTime = info.ModTime()
				latest = e
			}
		}
		if latest != nil && time.Since(latestTime) < dedupWindow {
			cachedPath := filepath.Join(backupDir, latest.Name())
			f, err := os.Open(cachedPath)
			if err == nil {
				defer f.Close()
				c.Writer.Header().Set("Content-Type", "application/zip")
				c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", latest.Name()))
				http.ServeContent(c.Writer, c.Request, latest.Name(), latestTime, f)
				return
			}
		}
	}

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

	// 5) 归档到 data/backup/backup-YYYYMMDD-HHmmSS.zip
	ts := time.Now().UTC().Format("20060102-150405")
	archiveName := fmt.Sprintf("backup-%s.zip", ts)

	archivePath := filepath.Join(backupDir, archiveName)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating backup directory: %v", err))
		return
	}

	if err := copyFile(tempZipPath, archivePath); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error archiving backup: %v", err))
		return
	}

	// 裁剪旧备份：仅保留最近 3 份 backup-* 类型
	pruneOldZipByPrefix(backupDir, "backup-", 3)

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

// pruneOldZipByPrefix 裁剪指定前缀的 .zip 文件，仅保留最近 keep 份。
// 文件名格式 {prefix}YYYYMMDD-HHmmSS.zip，按名称字典序即为时间序。
func pruneOldZipByPrefix(backupDir, prefix string, keep int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}

	lowerPrefix := strings.ToLower(prefix)
	var files []os.DirEntry
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if e.IsDir() || !strings.HasPrefix(name, lowerPrefix) || !strings.HasSuffix(name, ".zip") {
			continue
		}
		files = append(files, e)
	}

	if len(files) <= keep {
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for i := 0; i < len(files)-keep; i++ {
		path := filepath.Join(backupDir, files[i].Name())
		if err := os.Remove(path); err != nil {
			log.Printf("[prune] failed to remove old %s %s: %v", prefix, path, err)
		}
	}
}
