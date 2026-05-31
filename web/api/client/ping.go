package client

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
)

func GetPingTasks(c *gin.Context) {
	uuid, _ := c.Get("client_uuid")
	clientUUID, _ := uuid.(string)
	if clientUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_uuid not found"})
		return
	}

	result := tasks.GetPingTasksByClient(clientUUID)
	c.JSON(http.StatusOK, result)
}

func UploadPingResult(c *gin.Context) {
	uuid, _ := c.Get("client_uuid")
	clientUUID, _ := uuid.(string)
	if clientUUID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "client_uuid not found"})
		return
	}

	var req struct {
		TaskID     uint      `json:"task_id" binding:"required"`
		Value      int       `json:"value"`
		PingType   string    `json:"ping_type"`
		FinishedAt time.Time `json:"finished_at" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	record := models.PingRecord{
		Client: clientUUID,
		TaskId: req.TaskID,
		Value:  req.Value,
		Time:   models.FromTime(req.FinishedAt),
	}
	if err := tasks.SavePingRecord(record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save ping result: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
