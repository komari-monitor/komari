package admin

import (
	"archive/zip"
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
	"github.com/komari-monitor/komari/internal/api_v1/resp"
)

// 只有一个备份恢复操作在进行
var restoreMutex sync.Mutex

// UploadBackup 用于接收上传的备份文件并将其内容恢复到原始位置
func UploadBackup(c *gin.Context) {
	// 尝试获取锁，如果已有恢复操作在进行，则立即返回错误
	if !restoreMutex.TryLock() {
		resp.RespondError(c, http.StatusConflict, "Another restore operation is already in progress")
		return
	}
	defer restoreMutex.Unlock()

	// 获取上传的文件
	file, header, err := c.Request.FormFile("backup")
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error getting uploaded file: %v", err))
		return
	}
	defer file.Close()

	// 检查文件是否为zip格式
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		resp.RespondError(c, http.StatusBadRequest, "Uploaded file must be a ZIP archive")
		return
	}

	// 确保data目录存在
	if err := os.MkdirAll("./data", 0755); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating data directory: %v", err))
		return
	}

	// 创建临时文件保存上传的zip（先校验，再落地到固定位置）
	tempFile, err := os.CreateTemp("", "backup-upload-*.zip")
	if err != nil {
		resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating temporary file: %v", err))
		return
	}
	tempFilePath := tempFile.Name()
	defer os.Remove(tempFilePath) // 确保临时文件最终被删除

	// 将上传的文件内容复制到临时文件
	_, err = io.Copy(tempFile, file)
	if err != nil {
		tempFile.Close()
		resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving uploaded file: %v", err))
		return
	}
	tempFile.Close() // 关闭文件以便后续操作

	// 基础校验：检查是否包含标记文件
	if zr, err := zip.OpenReader(tempFilePath); err == nil {
		hasMarkup := false
		for _, f := range zr.File {
			if f.Name == "komari-backup-markup" {
				hasMarkup = true
				break
			}
		}
		zr.Close()
		if !hasMarkup {
			resp.RespondError(c, http.StatusBadRequest, "Invalid backup file: missing komari-backup-markup file")
			return
		}
	} else {
		resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error opening zip file: %v", err))
		return
	}

	// 将校验通过的临时文件移动到固定路径 ./data/backup.zip
	finalPath := filepath.Join(".", "data", "backup.zip")
	// 如存在旧文件，先删除
	_ = os.Remove(finalPath)
	if err := os.Rename(tempFilePath, finalPath); err != nil {
		// fallback：拷贝
		in, err2 := os.Open(tempFilePath)
		if err2 != nil {
			resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error preparing backup file: %v", err))
			return
		}
		defer in.Close()
		out, err2 := os.Create(finalPath)
		if err2 != nil {
			resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating target backup file: %v", err2))
			return
		}
		if _, err2 = io.Copy(out, in); err2 != nil {
			out.Close()
			resp.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing target backup file: %v", err2))
			return
		}
		out.Close()
	}

	// 返回：已保存备份，重启后将自动恢复
	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Backup uploaded successfully. The service will restart and apply the backup.",
		"path":    "./data/backup.zip",
	})

	go func() {
		log.Println("Backup uploaded, restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}
