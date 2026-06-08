package admin

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"github.com/komari-monitor/komari/web/api"
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
	var queuedClients []string
	var offlineClients []string
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		api.RespondError(c, 400, "Command cannot be empty")
		return
	}
	// for uuid := range agent_runtime.GetConnectedClients() {
	// 	if contain(req.Clients, uuid) {
	// 		onlineClients = append(onlineClients, uuid)
	// 	}
	// 	// else {
	// 	// 	api.RespondError(c, 400, "Client not connected: "+uuid)
	// 	// 	return
	// 	// }
	// }
	for _, uuid := range req.Clients {
		if client := agent_runtime.GetConnectedClients()[uuid]; client != nil {
			onlineClients = append(onlineClients, uuid)
		} else if agent_runtime.IsAgentOnline(uuid) {
			queuedClients = append(queuedClients, uuid)
		} else {
			offlineClients = append(offlineClients, uuid)
		}
	}
	if len(onlineClients) == 0 && len(queuedClients) == 0 {
		api.RespondError(c, 400, "No clients connected")
		return
	}
	taskId := utils.GenerateRandomString(16)
	taskClients := append(append([]string{}, onlineClients...), queuedClients...)
	taskClients = append(taskClients, offlineClients...)
	if err := tasks.CreateTask(taskId, taskClients, req.Command); err != nil {
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
		if agent_runtime.IsV2Client(uuid) {
			payload, _ = json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentExec, Params: v2.ExecParams{TaskID: taskId, Command: req.Command}})
		}
		client := agent_runtime.GetConnectedClients()[uuid]
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
	for _, uuid := range queuedClients {
		agent_runtime.DispatchV2Event(uuid, v2.MethodAgentExec, v2.ExecParams{TaskID: taskId, Command: req.Command})
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "REC, task id: "+taskId, "warn")
	api.RespondSuccess(c, gin.H{
		"task_id":        taskId,
		"clients":        onlineClients,
		"queued_clients": queuedClients,
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
