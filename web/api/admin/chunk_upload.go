package admin

import (
	"encoding/json"
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
	chunkUploadID       = "backup"
	maxChunkIndex       = int(backup.MaxArchiveSize / defaultChunkSize)
)

var chunkUploadRoot = filepath.Join(".", "data", "backup", ".uploading")

const uploadMetadataName = "upload.json"

type chunkUploadMetadata struct {
	Size int64 `json:"size"`
}

func isValidUploadID(uploadID string) bool {
	return uploadID == chunkUploadID
}

func chunkUploadDir(uploadID string) (string, error) {
	if !isValidUploadID(uploadID) {
		return "", fmt.Errorf("invalid upload_id")
	}
	return filepath.Join(chunkUploadRoot, uploadID), nil
}

func readChunkUploadMetadata(chunkDir string) (chunkUploadMetadata, error) {
	var metadata chunkUploadMetadata
	data, err := os.ReadFile(filepath.Join(chunkDir, uploadMetadataName))
	if err != nil {
		return metadata, err
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, err
	}
	if metadata.Size <= 0 || metadata.Size > backup.MaxArchiveSize {
		return metadata, fmt.Errorf("invalid upload size")
	}
	return metadata, nil
}

func chunkUploadSize(chunkDir string) (int64, error) {
	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".part") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if total > backup.MaxArchiveSize-info.Size() {
			return 0, fmt.Errorf("uploaded chunks exceed the archive limit")
		}
		total += info.Size()
	}
	return total, nil
}

func clearChunkUploadCache(root string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return os.MkdirAll(root, 0755)
}

// InitChunkUpload 初始化分块上传，返回 upload_id 和 chunk_size。
// Komari 使用单一上传槽位，初始化时会清除上次中断留下的全部缓存。
func InitChunkUpload(c *gin.Context) {
	var req struct {
		Size int64 `json:"size"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Size <= 0 || req.Size > backup.MaxArchiveSize {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("size must be between 1 and %d bytes", backup.MaxArchiveSize))
		return
	}

	if err := clearChunkUploadCache(chunkUploadRoot); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error clearing upload cache: %v", err))
		return
	}
	chunkDir, err := chunkUploadDir(chunkUploadID)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Error preparing upload directory")
		return
	}
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating upload directory: %v", err))
		return
	}
	metadata, err := json.Marshal(chunkUploadMetadata{Size: req.Size})
	if err != nil {
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error encoding upload metadata: %v", err))
		return
	}
	if err := os.WriteFile(filepath.Join(chunkDir, uploadMetadataName), metadata, 0600); err != nil {
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing upload metadata: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_id":  chunkUploadID,
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

	metadata, err := readChunkUploadMetadata(chunkDir)
	if err != nil {
		api.RespondError(c, http.StatusNotFound, "upload_id not found or expired")
		return
	}

	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("%d.part", chunkIndex))
	tempChunk, err := os.CreateTemp(chunkDir, fmt.Sprintf(".%d-*.part", chunkIndex))
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving chunk: %v", err))
		return
	}
	tempChunkPath := tempChunk.Name()
	defer os.Remove(tempChunkPath)

	written, err := io.Copy(tempChunk, io.LimitReader(file, defaultChunkSize+1))
	if closeErr := tempChunk.Close(); err == nil {
		err = closeErr
	}
	if err != nil || written > defaultChunkSize {
		if err != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error writing chunk: %v", err))
		} else {
			api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Chunk too large: %d bytes (max %d)", written, defaultChunkSize))
		}
		return
	}
	currentSize, err := chunkUploadSize(chunkDir)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error checking uploaded chunks: %v", err))
		return
	}
	oldSize := int64(0)
	if info, err := os.Stat(chunkPath); err == nil {
		oldSize = info.Size()
	} else if !os.IsNotExist(err) {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error checking existing chunk: %v", err))
		return
	}
	if currentSize-oldSize+written > metadata.Size {
		api.RespondError(c, http.StatusBadRequest, "uploaded chunks exceed the declared backup size")
		return
	}
	if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error replacing chunk: %v", err))
		return
	}
	if err := os.Rename(tempChunkPath, chunkPath); err != nil {
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error publishing chunk: %v", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"received":    true,
		"chunk_index": chunkIndex,
	})
}

// MergeChunkUpload 合并分块 → 校验 ZIP → 保存到 data/backup/ → 触发恢复。
func MergeChunkUpload(c *gin.Context) {
	restoreLock, err := backup.AcquireRestoreLock()
	if err != nil {
		api.RespondError(c, http.StatusConflict, err.Error())
		return
	}
	keepRestoreLock := false
	defer func() {
		if !keepRestoreLock {
			restoreLock.Release()
		}
	}()

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
	metadata, err := readChunkUploadMetadata(chunkDir)
	if err != nil {
		api.RespondError(c, http.StatusNotFound, "upload_id not found or expired")
		return
	}
	if uploadedSize, err := chunkUploadSize(chunkDir); err != nil || uploadedSize != metadata.Size {
		if err != nil {
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error checking uploaded chunks: %v", err))
		} else {
			api.RespondError(c, http.StatusBadRequest, "uploaded chunk size does not match the declared backup size")
		}
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
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error merging chunks: %v", err))
		return
	}

	if err := validateBackupZip(mergedPath); err != nil {
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Invalid backup: %v", err))
		return
	}

	// 保存归档副本到 data/backup/，文件名由服务端生成。
	archivePath := filepath.Join(backupDir, archiveName)
	if err := os.Rename(mergedPath, archivePath); err != nil {
		if cpErr := copyFile(mergedPath, archivePath); cpErr != nil {
			clearChunkUploadCache(chunkUploadRoot)
			api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error saving merged file: %v", cpErr))
			return
		}
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusInternalServerError, fmt.Sprintf("Error opening archived backup: %v", err))
		return
	}
	defer archive.Close()
	if err := restoreLock.SaveUploadedBackup(archive, archiveName); err != nil {
		clearChunkUploadCache(chunkUploadRoot)
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("Error preparing restore: %v", err))
		return
	}

	clearChunkUploadCache(chunkUploadRoot)

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Backup uploaded successfully. The service will restart and apply the backup.",
		"path":    filepath.Join(".", "data", "backup.zip"),
	})

	keepRestoreLock = true
	go func() {
		log.Println("Backup uploaded (chunk), restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		restoreLock.Release()
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
	if err := backup.ValidateArchive(zipPath); err != nil {
		return fmt.Errorf("not a valid backup archive: %w", err)
	}
	return nil
}
