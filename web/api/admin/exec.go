package admin

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/ws"
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
		api.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	// for uuid := range ws.GetConnectedClients() {
	// 	if contain(req.Clients, uuid) {
	// 		onlineClients = append(onlineClients, uuid)
	// 	}
	// 	// else {
	// 	// 	api.RespondError(c, 400, "Client not connected: "+uuid)
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
		api.RespondError(c, 400, "No clients connected")
		return
	}
	taskId := utils.GenerateRandomString(16)
	if err := tasks.CreateTask(taskId, append(onlineClients, offlineClients...), req.Command); err != nil {
		api.RespondError(c, 500, "Failed to create task: "+err.Error())
		return
	}
	for _, uuid := range onlineClients {
		legacy := struct {
			Message string `json:"message"`
			Command string `json:"command"`
			TaskId  string `json:"task_id"`
		}{Message: "exec", Command: req.Command, TaskId: taskId}
		payload, _ := json.Marshal(legacy)
		if ws.IsV2Client(uuid) {
			payload, _ = json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentExec, Params: v2.ExecParams{TaskID: taskId, Command: req.Command}})
		}
		client := ws.GetConnectedClients()[uuid]
		if client != nil {
			if err := client.WriteMessage(websocket.TextMessage, payload); err != nil {
				api.RespondError(c, 400, "Client connection is broke: "+uuid)
				return
			}
		} else {
			api.RespondError(c, 400, "Client connection is null: "+uuid)
			return
		}
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "REC, task id: "+taskId, "warn")
	api.RespondSuccess(c, gin.H{
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
