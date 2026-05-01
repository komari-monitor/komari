package public

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/tasks"
)

// PublicPingTask 是对外暴露的延迟监测任务信息。
type PublicPingTask struct {
	Id        uint     `json:"id"`
	Name      string   `json:"name"`
	Clients   []string `json:"clients"`
	DefaultOn bool     `json:"default_on"`
	Type      string   `json:"type"`
	Interval  int      `json:"interval"`
}

func GetPublicPingTasks(c *gin.Context) {
	tasks, err := tasks.GetAllPingTasks()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	publicTasks := make([]PublicPingTask, len(tasks))
	for i, task := range tasks {
		publicTasks[i] = PublicPingTask{
			Id:        task.Id,
			Name:      task.Name,
			Clients:   task.Clients,
			DefaultOn: task.DefaultOn,
			Type:      task.Type,
			Interval:  task.Interval,
		}
	}

	api.RespondSuccess(c, publicTasks)
}
