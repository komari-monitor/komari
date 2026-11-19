package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/plugin"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/web/backup"
	"github.com/komari-monitor/komari/web/upload"
)

func NewArchiveUploadHandler() *upload.Handler {
	h := upload.NewHandler(upload.DefaultStore, map[upload.Purpose]upload.Finalizer{
		upload.PurposeBackup: finalizeBackupUpload,
		upload.PurposePlugin: finalizePluginUpload,
		upload.PurposeTheme:  finalizeThemeUpload,
	})
	h.OnMerged = auditArchiveUpload
	return h
}

func auditArchiveUpload(c *gin.Context, session upload.Session, result upload.Result) {
	ip := c.ClientIP()
	actor := c.GetString("uuid")
	filename := session.Metadata.Filename
	size := auditlog.FormatBytes(session.Metadata.Size)
	switch session.Metadata.Purpose {
	case upload.PurposeBackup:
		auditlog.Log(ip, actor, fmt.Sprintf("Upload backup archive for restore: file=%s, size=%s", filename, size), "warn")
	case upload.PurposePlugin:
		if info, ok := result.Data.(models.Plugin); ok {
			auditlog.Log(ip, actor, fmt.Sprintf("Install plugin from upload: short=%s, version=%s, file=%s, size=%s", info.Short, info.Version, filename, size), "warn")
		} else {
			auditlog.Log(ip, actor, fmt.Sprintf("Install plugin from upload: file=%s, size=%s", filename, size), "warn")
		}
	case upload.PurposeTheme:
		if info, ok := result.Data.(models.Theme); ok {
			auditlog.Log(ip, actor, fmt.Sprintf("Install theme from upload: short=%s, version=%s, file=%s, size=%s", info.Short, info.Version, filename, size), "info")
		} else {
			auditlog.Log(ip, actor, fmt.Sprintf("Install theme from upload: file=%s, size=%s", filename, size), "info")
		}
	}
}

func finalizeBackupUpload(session upload.Session) (upload.Result, error) {
	restoreLock, err := backup.AcquireRestoreLock()
	if err != nil {
		return upload.Result{}, err
	}
	archive, err := os.Open(session.ArchivePath)
	if err != nil {
		restoreLock.Release()
		return upload.Result{}, fmt.Errorf("open merged backup: %w", err)
	}
	if err := restoreLock.SaveUploadedBackup(archive, session.Metadata.Filename); err != nil {
		_ = archive.Close()
		restoreLock.Release()
		return upload.Result{}, err
	}
	if err := archive.Close(); err != nil {
		restoreLock.Release()
		return upload.Result{}, fmt.Errorf("close merged backup: %w", err)
	}

	go func() {
		logger.InfoArgs("admin-api", "Backup uploaded, restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		restoreLock.Release()
		os.Exit(0)
	}()

	return upload.Result{
		Message: "Backup uploaded successfully. The service will restart and apply the backup.",
		Data:    map[string]string{"path": filepath.Join(".", "data", "backup.zip")},
	}, nil
}

func finalizePluginUpload(session upload.Session) (upload.Result, error) {
	info, err := plugin.InstallZip(session.ArchivePath)
	if err != nil {
		return upload.Result{}, err
	}
	return upload.Result{Message: "插件上传成功", Data: info}, nil
}

func finalizeThemeUpload(session upload.Session) (upload.Result, error) {
	info, err := extractAndValidateTheme(session.ArchivePath)
	if err != nil {
		return upload.Result{}, err
	}
	return upload.Result{Message: "主题上传成功", Data: info}, nil
}
