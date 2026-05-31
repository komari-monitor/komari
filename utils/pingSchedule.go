package utils

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/corn"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/web/ws"
)

// PingTaskManager 管理定时器和任务
type PingTaskManager struct {
	mu    sync.Mutex
	tasks map[int][]models.PingTask
}

var manager = &PingTaskManager{
	tasks: make(map[int][]models.PingTask),
}

// Reload 重载时间表
func (m *PingTaskManager) Reload(pingTasks []models.PingTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	corn.RemovePrefix("ping:")
	m.tasks = make(map[int][]models.PingTask)

	// 按Interval分组任务
	taskGroups := make(map[int][]models.PingTask)
	for _, task := range pingTasks {
		if task.Interval <= 0 {
			continue
		}
		taskGroups[task.Interval] = append(taskGroups[task.Interval], task)
	}

	// 为每个唯一的Interval创建协程
	for interval, tasks := range taskGroups {
		interval := interval
		tasks := append([]models.PingTask(nil), tasks...)
		m.tasks[interval] = tasks
		if err := corn.AddContextFunc(fmt.Sprintf("ping:%d", interval), corn.Every(time.Duration(interval)*time.Second), false, func(ctx context.Context) {
			onlineClients := ws.GetConnectedClients()
			for _, task := range tasks {
				go executePingTask(ctx, task, onlineClients)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

// executePingTask 执行单个PingTask
func executePingTask(ctx context.Context, task models.PingTask, onlineClients map[string]*ws.SafeConn) {
	var message struct {
		TaskID  uint   `json:"ping_task_id"`
		Message string `json:"message"`
		Type    string `json:"ping_type"`
		Target  string `json:"ping_target"`
	}

	message.Message = "ping"
	message.TaskID = task.Id
	message.Type = task.Type
	message.Target = task.Target

	for _, clientUUID := range targetPingClientUUIDs(task, onlineClients) {
		select {
		case <-ctx.Done():
			// Context was canceled, stop sending pings.
			return
		default:
			// Context is still active, continue.
		}

		if conn, exists := onlineClients[clientUUID]; exists && conn != nil {
			payload := any(message)
			if ws.IsV2Client(clientUUID) {
				payload = v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentPing, Params: v2.PingParams{TaskID: task.Id, Type: task.Type, Target: task.Target}}
			}
			if err := conn.WriteJSON(payload); err != nil {
				continue
			}
		}
	}
}

// targetPingClientUUIDs 根据任务配置计算本次调度需要下发的在线服务器列表。
func targetPingClientUUIDs(task models.PingTask, onlineClients map[string]*ws.SafeConn) []string {
	_ = onlineClients
	return task.Clients
}

// ReloadPingSchedule 加载或重载时间表
func ReloadPingSchedule(pingTasks []models.PingTask) error {
	return manager.Reload(pingTasks)
}
