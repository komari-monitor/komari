package admin

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/ws"
	"github.com/komari-monitor/komari/pkg/utils"
)

// 接受数据类型：
// - command: string
// - clients: []string (客户端 UUID 列表)
func Exec(c *gin.Context) {
	var req struct {
		Command string   `json:"command" binding:"required"`
		Clients []string `json:"clients" binding:"required"`
	}
	var onlineClients []string
	var offlineClients []string
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	// for uuid := range ws.GetConnectedClients() {
	// 	if contain(req.Clients, uuid) {
	// 		onlineClients = append(onlineClients, uuid)
	// 	}
	// 	// else {
	// 	// 	resp.RespondError(c, 400, "Client not connected: "+uuid)
	// 	// 	return
	// 	// }
	// }
	for _, uuid := range req.Clients {
		if client := ws.GetConnectedClients()[uuid]; client != nil {
			onlineClients = append(onlineClients, uuid)
		} else {
			offlineClients = append(offlineClients, uuid)
		}
	}
	if len(onlineClients) == 0 {
		resp.RespondError(c, 400, "No clients connected")
		return
	}
	taskId := utils.GenerateRandomString(16)
	if err := tasks.CreateTask(taskId, append(onlineClients, offlineClients...), req.Command); err != nil {
		resp.RespondError(c, 500, "Failed to create task: "+err.Error())
		return
	}
	for _, uuid := range onlineClients {
		var send struct {
			Message string `json:"message"`
			Command string `json:"command"`
			TaskId  string `json:"task_id"`
		}
		send.Message = "exec"
		send.Command = req.Command
		send.TaskId = taskId

		payload, _ := json.Marshal(send)
		client := ws.GetConnectedClients()[uuid]
		if client != nil {
			if err := client.WriteMessage(websocket.TextMessage, payload); err != nil {
				resp.RespondError(c, 400, "Client connection is broke: "+uuid)
				return
			}
		} else {
			resp.RespondError(c, 400, "Client connection is null: "+uuid)
			return
		}
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "REC, task id: "+taskId, "warn")
	resp.RespondSuccess(c, gin.H{
		"task_id": taskId,
		"clients": onlineClients,
	})
	if len(offlineClients) > 0 {
		for _, uuid := range offlineClients {
			tasks.SaveTaskResult(taskId, uuid, "Client offline!", -1, models.FromTime(time.Now()))
		}
	}
}

// func contain(clients []string, uuid string) bool {
// 	for _, client := range clients {
// 		if client == uuid {
// 			return true
// 		}
// 	}
// 	return false
// }
