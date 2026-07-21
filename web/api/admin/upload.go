package admin

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/backup"
)

// UploadBackup receives a backup and applies it after the service restarts.
func UploadBackup(c *gin.Context) {
	file, header, err := c.Request.FormFile("backup")
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("get uploaded backup: %v", err))
		return
	}
	defer file.Close()
	if err := backup.SaveUploadedBackup(file, header.Filename); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Backup uploaded successfully. The service will restart and apply the backup.",
		"path":    "./data/backup.zip",
	})
	go func() {
		logger.InfoArgs("admin-api", "Backup uploaded, restarting service in 2 seconds to apply on startup...")
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}
