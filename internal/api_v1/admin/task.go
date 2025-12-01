package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/tasks"
)

func GetTasks(c *gin.Context) {
	dbTasks, err := tasks.GetAllTasks()
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve tasks: "+err.Error())
		return
	}
	var responseTasks []gin.H
	for _, t := range dbTasks {
		results, err := tasks.GetTaskResultsByTaskId(t.TaskId)
		if err != nil {
			resp.RespondError(c, 500, "Failed to retrieve task results: "+err.Error())
			return
		}

		var filteredResults []gin.H
		for _, r := range results {
			filteredResults = append(filteredResults, gin.H{
				"client":      r.Client,
				"result":      r.Result,
				"exit_code":   r.ExitCode,
				"finished_at": r.FinishedAt,
				"created_at":  r.CreatedAt,
			})
		}

		responseTasks = append(responseTasks, gin.H{
			"task_id": t.TaskId,
			"clients": t.Clients,
			"command": t.Command,
			"results": filteredResults,
		})
	}
	resp.RespondSuccess(c, responseTasks)
}

func GetTaskById(c *gin.Context) {
	taskId := c.Param("task_id")
	if taskId == "" {
		resp.RespondError(c, 400, "Task ID is required")
		return
	}
	task, err := tasks.GetTaskByTaskId(taskId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve task: "+err.Error())
		return
	}
	if task == nil {
		resp.RespondError(c, 404, "Task not found")
		return
	}
	results, err := tasks.GetTaskResultsByTaskId(taskId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve task results: "+err.Error())
		return
	}
	var filteredResults []gin.H
	for _, r := range results {
		filteredResults = append(filteredResults, gin.H{
			"client":      r.Client,
			"result":      r.Result,
			"exit_code":   r.ExitCode,
			"finished_at": r.FinishedAt,
			"created_at":  r.CreatedAt,
		})
	}
	resp.RespondSuccess(c, gin.H{
		"task_id": task.TaskId,
		"clients": task.Clients,
		"command": task.Command,
		"results": filteredResults,
	})
}

func GetTasksByClientId(c *gin.Context) {
	clientId := c.Param("uuid")
	if clientId == "" {
		resp.RespondError(c, 400, "Client ID is required")
		return
	}
	tasks, err := tasks.GetTasksByClientId(clientId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve tasks: "+err.Error())
		return
	}
	if len(tasks) == 0 {
		resp.RespondError(c, 404, "No tasks found for this client")
		return
	}
	resp.RespondSuccess(c, tasks)
}

func GetSpecificTaskResult(c *gin.Context) {
	taskId := c.Param("task_id")
	clientId := c.Param("uuid")
	if taskId == "" || clientId == "" {
		resp.RespondError(c, 400, "Task ID and Client ID are required")
		return
	}
	result, err := tasks.GetSpecificTaskResult(taskId, clientId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve task result: "+err.Error())
		return
	}
	if result == nil {
		resp.RespondError(c, 404, "No result found for this task and client")
		return
	}
	resp.RespondSuccess(c, result)
}

// Param: task_id
func GetTaskResultsByTaskId(c *gin.Context) {
	taskId := c.Param("task_id")
	if taskId == "" {
		resp.RespondError(c, 400, "Task ID is required")
		return
	}
	results, err := tasks.GetTaskResultsByTaskId(taskId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve task results: "+err.Error())
		return
	}
	if len(results) == 0 {
		resp.RespondError(c, 404, "No results found for this task")
		return
	}
	resp.RespondSuccess(c, results)
}

func GetAllTaskResultByUUID(c *gin.Context) {
	clientId := c.Param("uuid")
	if clientId == "" {
		resp.RespondError(c, 400, "Client ID is required")
		return
	}
	results, err := tasks.GetAllTasksResultByUUID(clientId)
	if err != nil {
		resp.RespondError(c, 500, "Failed to retrieve tasks: "+err.Error())
		return
	}
	if len(results) == 0 {
		resp.RespondError(c, 404, "No tasks found for this client")
		return
	}
	resp.RespondSuccess(c, results)
}
