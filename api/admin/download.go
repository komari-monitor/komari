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
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/dbcore"
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

// copyDataToTempExcludingDB 将 ./data 下除了 .db/.db-wal/.db-shm 之外的所有文件复制到临时目录
func copyDataToTempExcludingDB(tempDir string) error {
	dataRoot := "./data"

	// 如果 data 目录不存在，视为无文件可复制
	if stat, err := os.Stat(dataRoot); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat data dir: %v", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("%s is not a directory", dataRoot)
	}

	return filepath.Walk(dataRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dataRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// 跳过数据库相关文件
		name := info.Name()
		if strings.HasSuffix(strings.ToLower(name), ".db") ||
			strings.HasSuffix(strings.ToLower(name), ".db-wal") ||
			strings.HasSuffix(strings.ToLower(name), ".db-shm") {
			return nil
		}

		dst := filepath.Join(tempDir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return copyFile(p, dst)
	})
}

// backupSQLiteTo 使用 SQLite VACUUM INTO 将当前数据库一致性备份到指定路径
func backupSQLiteTo(destDBPath string) error {
	if err := os.MkdirAll(filepath.Dir(destDBPath), 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory for db: %v", err)
	}

	db := dbcore.GetDBInstance()
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database connection: %v", err)
	}

	safePath := strings.ReplaceAll(destDBPath, "'", "''")
	vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", safePath)
	if _, err = sqlDB.Exec(vacuumSQL); err != nil {
		return fmt.Errorf("sqlite VACUUM INTO failed: %v", err)
	}
	return nil
}

// DownloadBackup 用于打包 ./data 目录及数据库文件为 zip 并通过 HTTP 下载
func DownloadBackup(c *gin.Context) {
	// 1) 创建临时目录
	tempDir, err := os.MkdirTemp("", "komari-backup-*")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating temporary directory: %v", err))
		return
	}
	defer os.RemoveAll(tempDir)

	// 2) 复制 ./data 下除 .db/.db-wal/.db-shm 外的所有文件到临时目录
	if err := copyDataToTempExcludingDB(tempDir); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error copying data to temp: %v", err))
		return
	}

	// 3) 导出 SQLite 数据库
	destDB := filepath.Join(tempDir, "komari.db")
	if err := backupSQLiteTo(destDB); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error backing up database: %v", err))
		return
	}

	// 4) 开始写出 ZIP（以临时目录为根）
	backupFileName := fmt.Sprintf("backup-%d.zip", time.Now().UnixMicro())
	c.Writer.Header().Set("Content-Type", "application/zip")
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", backupFileName))

	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	// 写入临时目录里的内容
	err = filepath.Walk(tempDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tempDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// zip 内路径统一正斜杠
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
		w, err := zipWriter.CreateHeader(&zip.FileHeader{
			Name:     zipPath,
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		})
		if err != nil {
			f.Close()
			return err
		}
		_, err = io.Copy(w, f)
		f.Close()
		return err
	})
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error archiving temp folder: %v", err))
		return
	}

	// 5) 追加备份标记文件（放在 zip 根目录）
	markupContent := fmt.Sprintf("This is a Komari backup markup file, please do not delete.\n\nBackup Time: %s\nDatabase Type: sqlite\n", time.Now().Format(time.RFC3339))
	markupWriter, err := zipWriter.CreateHeader(&zip.FileHeader{
		Name:     "komari-backup-markup",
		Method:   zip.Deflate,
		Modified: time.Now(),
	})
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating backup markup file: %v", err))
		return
	}
	if _, err = markupWriter.Write([]byte(markupContent)); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing backup markup file: %v", err))
		return
	}
}