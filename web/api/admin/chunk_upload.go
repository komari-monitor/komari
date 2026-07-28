package admin

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/backup"
)

const (
	defaultChunkSize    = 5 * 1024 * 1024 // 5 MiB
	maxChunkRequestSize = defaultChunkSize + 1*1024*1024
	uploadIDLength      = 32
	uploadExpiration    = 24 * time.Hour
	maxChunkIndex       = 1_000_000
)

var chunkUploadRoot = filepath.Join(".", "data", "backup", ".uploading")

// generateUploadID 生成随机上传 ID，仅包含 hex 字符防止路径穿越。
func generateUploadID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isValidUploadID(uploadID string) bool {
	if len(uploadID) != uploadIDLength {
		return false
	}
	_, err := hex.DecodeString(uploadID)
	return err == nil
}

func chunkUploadDir(uploadID string) (string, error) {
	if !isValidUploadID(uploadID) {
		return "", fmt.Errorf("invalid upload_id")
	}
	return filepath.Join(chunkUploadRoot, uploadID), nil
}

func cleanupExpiredChunkUploads(root string, now time.Time) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < uploadExpiration {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// InitChunkUpload 初始化分块上传，返回 upload_id 和 chunk_size。
// 仅清理过期上传，避免影响仍在进行的其他上传。
func InitChunkUpload(c *gin.Context) {
	if err := cleanupExpiredChunkUploads(chunkUploadRoot, time.Now()); err != nil {
		log.Printf("[chunk_upload] failed to clean expired uploads: %v", err)
	}

	uploadID, err := generateUploadID()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error generating upload ID: %v", err))
		return
	}

	chunkDir, err := chunkUploadDir(uploadID)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Error preparing upload directory")
		return
	}
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating upload directory: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_id":  uploadID,
		"chunk_size": defaultChunkSize,
	})
}

// UploadChunk 接收单个分块，保存到临时目录。
func UploadChunk(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxChunkRequestSize)
	uploadID := c.PostForm("upload_id")
	chunkDir, err := chunkUploadDir(uploadID)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "invalid upload_id")
		return
	}

	chunkIndexStr := c.PostForm("chunk_index")
	if chunkIndexStr == "" {
		api.RespondError(c, http.StatusBadRequest, "chunk_index is required")
		return
	}
	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil || chunkIndex < 0 || chunkIndex > maxChunkIndex {
		api.RespondError(c, http.StatusBadRequest, "chunk_index must be a non-negative integer")
		return
	}

	if info, err := os.Stat(chunkDir); err != nil || !info.IsDir() {
		api.RespondError(c, http.StatusNotFound, "upload_id not found or expired")
		return
	}

	file, _, err := c.Request.FormFile("chunk_data")
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error getting chunk data: %v", err))
		return
	}
	defer file.Close()

	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.part", chunkIndex))
	out, err := os.Create(chunkPath)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving chunk: %v", err))
		return
	}

	written, err := io.Copy(out, io.LimitReader(file, defaultChunkSize+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil || written > defaultChunkSize {
		os.Remove(chunkPath)
		if err != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing chunk: %v", err))
		} else {
			api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Chunk too large: %d bytes (max %d)", written, defaultChunkSize))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"received":    true,
		"chunk_index": chunkIndex,
	})
}

// MergeChunkUpload 合并分块 → 校验 ZIP → 保存到 data/backup/ → 触发恢复。
func MergeChunkUpload(c *gin.Context) {
	var req struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request: %v", err))
		return
	}

	chunkDir, err := chunkUploadDir(req.UploadID)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "invalid upload_id")
		return
	}
	if info, err := os.Stat(chunkDir); err != nil || !info.IsDir() {
		api.RespondError(c, http.StatusNotFound, "upload_id not found or expired")
		return
	}

	backupDir := filepath.Join(".", "data", "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating backup directory: %v", err))
		return
	}

	archiveName := fmt.Sprintf("backup-%s.zip", time.Now().UTC().Format("20060102-150405.000000"))

	mergedPath := filepath.Join(chunkDir, "merged.zip")
	if err := mergeChunks(chunkDir, mergedPath); err != nil {
		cleanupChunkDir(chunkDir)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error merging chunks: %v", err))
		return
	}

	if err := validateBackupZip(mergedPath); err != nil {
		cleanupChunkDir(chunkDir)
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Invalid backup: %v", err))
		return
	}

	// 保存归档副本到 data/backup/，文件名由服务端生成。
	archivePath := filepath.Join(backupDir, archiveName)
	if err := os.Rename(mergedPath, archivePath); err != nil {
		if cpErr := copyFile(mergedPath, archivePath); cpErr != nil {
			cleanupChunkDir(chunkDir)
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving merged file: %v", cpErr))
			return
		}
	}

	// 裁剪旧备份，仅保留最近 3 份
	pruneOldZipByPrefix(backupDir, "backup-", 3)

	archive, err := os.Open(archivePath)
	if err != nil {
		cleanupChunkDir(chunkDir)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error opening archived backup: %v", err))
		return
	}
	defer archive.Close()
	if err := backup.SaveUploadedBackup(archive, archiveName); err != nil {
		cleanupChunkDir(chunkDir)
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error preparing restore: %v", err))
		return
	}

	cleanupChunkDir(chunkDir)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Backup uploaded successfully. The service will restart and apply the backup.",
		"path":    filepath.Join(".", "data", "backup.zip"),
	})

	go func() {
		log.Println("Backup uploaded (chunk), restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// mergeChunks 按分块索引数值排序后顺序合并所有 .part 文件。
func mergeChunks(chunkDir, destPath string) error {
	parts, err := filepath.Glob(filepath.Join(chunkDir, "*.part"))
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("no chunks found")
	}

	// 按分块索引数值排序（非字典序），避免 10.part 排在 2.part 前面
	sort.Slice(parts, func(i, j int) bool {
		idxI, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(parts[i]), ".part"))
		idxJ, _ := strconv.Atoi(strings.TrimSuffix(filepath.Base(parts[j]), ".part"))
		return idxI < idxJ
	})

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for _, partPath := range parts {
		f, err := os.Open(partPath)
		if err != nil {
			return fmt.Errorf("error opening chunk %s: %v", filepath.Base(partPath), err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return fmt.Errorf("error reading chunk %s: %v", filepath.Base(partPath), err)
		}
		f.Close()
	}
	return nil
}

// validateBackupZip 校验 ZIP 结构完整性及 komari-backup-markup 标记文件。
func validateBackupZip(zipPath string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("not a valid zip file: %v", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name == "komari-backup-markup" {
			return nil
		}
	}
	return fmt.Errorf("missing komari-backup-markup file")
}

// cleanupChunkDir 清理临时上传目录。
func cleanupChunkDir(chunkDir string) {
	if err := os.RemoveAll(chunkDir); err != nil {
		log.Printf("[chunk_upload] failed to cleanup %s: %v", chunkDir, err)
	}
}
