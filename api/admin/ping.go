package admin

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
)

// POST body: clients []string, target, task_type string, interval int
func AddPingTask(c *gin.Context) {
	var req struct {
		Clients  []string `json:"clients" binding:"required"`
		Name     string   `json:"name" binding:"required"`
		Target   string   `json:"target" binding:"required"`
		TaskType string   `json:"type" binding:"required"`     // icmp, tcp, http
		Interval int      `json:"interval" binding:"required"` // 间隔时间，单位秒
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if taskID, err := tasks.AddPingTask(req.Clients, req.Name, req.Target, req.TaskType, req.Interval); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		api.RespondSuccess(c, gin.H{"task_id": taskID})
	}
}

// POST body: id []uint
func DeletePingTask(c *gin.Context) {
	var req struct {
		ID []uint `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := tasks.DeletePingTask(req.ID); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		api.RespondSuccess(c, nil)
	}
}

// POST body: id []uint, updates map[string]interface{}
func EditPingTask(c *gin.Context) {
	var req struct {
		Tasks []*models.PingTask `json:"tasks" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request data")
		return
	}

	if err := tasks.EditPingTask(req.Tasks); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		// for _, task := range req.Tasks {
		// 	tasks.DeletePingRecords([]uint{task.Id})
		// }
		api.RespondSuccess(c, nil)
	}
}

func GetAllPingTasks(c *gin.Context) {
	tasks, err := tasks.GetAllPingTasks()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	api.RespondSuccess(c, tasks)
}

func OrderPingTask(c *gin.Context) {
	var req map[string]int
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid or missing request body: "+err.Error())
		return
	}

	order := make(map[uint]int, len(req))
	for idStr, weight := range req {
		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			api.RespondError(c, http.StatusBadRequest, "Invalid task id: "+idStr)
			return
		}
		order[uint(id)] = weight
	}

	if err := tasks.UpdatePingTaskOrder(order); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	api.RespondSuccess(c, nil)
}
