package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	clients "github.com/komari-monitor/komari/internal/client"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/ws"
)

func AddClient(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		uuid, token, err := clients.CreateClient()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "success", "uuid": uuid, "token": token})
		return
	}
	uuid, token, err := clients.CreateClientWithName(req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	user_uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), user_uuid.(string), "create client:"+uuid, "info")
	event.Trigger(eventType.ClientCreated, event.M{
		"client": uuid,
		"token":  token,
	})
	c.JSON(http.StatusOK, gin.H{"status": "success", "uuid": uuid, "token": token, "message": ""})
}

func EditClient(c *gin.Context) {
	var req = make(map[string]interface{})
	uuid := c.Param("uuid")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Invalid or missing UUID"})
		return
	}
	req["uuid"] = uuid
	err := clients.PartialUpdate(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	user_uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), user_uuid.(string), "edit client:"+uuid, "info")
	event.Trigger(eventType.ClientUpdated, event.M{
		"client": uuid,
		"data":   req,
	})
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func RemoveClient(c *gin.Context) {
	uuid := c.Param("uuid")
	err := clients.DeleteClient(uuid)
	if err != nil {
		c.JSON(500, gin.H{
			"status": "error",
			"error":  "Failed to delete client" + err.Error(),
		})
		return
	}
	user_uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), user_uuid.(string), "delete client:"+uuid, "warn")
	c.JSON(200, gin.H{"status": "success"})
	ws.DeleteConnectedClients(uuid)
	ws.DeleteLatestReport(uuid)
	event.Trigger(eventType.ClientDeleted, event.M{
		"client": uuid,
	})
}

func ClearRecord(c *gin.Context) {
	if err := records.DeleteAll(); err != nil {
		c.JSON(500, gin.H{
			"status":  "error",
			"message": "Failed to delete Record" + err.Error(),
		})
		return
	}
	user_uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), user_uuid.(string), "clear records", "warn")
	c.JSON(200, gin.H{"status": "success"})
}

func GetClient(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(400, gin.H{
			"status":  "error",
			"message": "Invalid or missing UUID",
		})
		return
	}

	result, err := clients.GetClientByUUID(uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, result)
}

func ListClients(c *gin.Context) {
	cls, err := clients.GetAllClientBasicInfo()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, cls)
}

func GetClientToken(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(400, gin.H{
			"status":  "error",
			"message": "Invalid or missing UUID",
		})
		return
	}

	token, err := clients.GetClientTokenByUUID(uuid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "token": token, "message:": ""})
}
