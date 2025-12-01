package pingSchedule

import (
	"context"
	"sync"
	"time"

	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/ws"
)

// PingTaskManager 管理定时器和任务
type PingTaskManager struct {
	mu         sync.Mutex
	cancelFunc context.CancelFunc
	tasks      map[int][]models.PingTask
}

var manager = &PingTaskManager{
	tasks: make(map[int][]models.PingTask),
}

// Reload 重载时间表
func (m *PingTaskManager) Reload(pingTasks []models.PingTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancelFunc != nil {
		m.cancelFunc()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel

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
		m.tasks[interval] = tasks
		go m.runPreciseLoop(ctx, time.Duration(interval)*time.Second, tasks)
	}
	return nil
}

func (m *PingTaskManager) runPreciseLoop(ctx context.Context, interval time.Duration, tasks []models.PingTask) {
	// Start the first timer.
	timer := time.NewTimer(interval)

	// This will be the reference point for all future ticks.
	// By adding the interval to this time, we avoid accumulating execution delays.
	nextTick := time.Now().Add(interval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			onlineClients := ws.GetConnectedClients()
			for _, task := range tasks {
				go executePingTask(ctx, task, onlineClients)
			}

			nextTick = nextTick.Add(interval)
			timer.Reset(time.Until(nextTick))

		case <-ctx.Done():
			return
		}
	}
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

	for _, clientUUID := range task.Clients {
		select {
		case <-ctx.Done():
			// Context was canceled, stop sending pings.
			return
		default:
			// Context is still active, continue.
		}

		if conn, exists := onlineClients[clientUUID]; exists && conn != nil {
			if err := conn.WriteJSON(message); err != nil {
				continue
			}
		}
	}
}

// ReloadPingScheduleWithTasks 加载或重载时间表
func ReloadPingScheduleWithTasks(pingTasks []models.PingTask) error {
	return manager.Reload(pingTasks)
}

func ReloadPingSchedule() error {
	db := dbcore.GetDBInstance()
	var pingTasks []models.PingTask
	if err := db.Find(&pingTasks).Error; err != nil {
		return err
	}
	return manager.Reload(pingTasks)
}
