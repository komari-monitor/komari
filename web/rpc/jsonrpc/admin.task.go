package jsonrpc

import (
	"context"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.task.go
// 任务查询 RPC2 方法（admin 命名空间）。

func init() {
	reg("getTasks", adminGetTasks, "List all exec tasks with results")
	reg("getTaskById", adminGetTaskById, "Get an exec task by task_id")
	reg("getTasksByClientId", adminGetTasksByClientId, "List tasks assigned to a client")
	reg("getSpecificTaskResult", adminGetSpecificTaskResult, "Get a task result for a client")
	reg("getTaskResultsByTaskId", adminGetTaskResultsByTaskId, "List results of a task")
}

// projectTaskResults 投影任务结果，保持与原 REST 输出字段一致。
func projectTaskResults(results []models.TaskResult) []map[string]any {
	out := []map[string]any{}
	for _, r := range results {
		out = append(out, map[string]any{
			"client":      r.Client,
			"result":      r.Result,
			"exit_code":   r.ExitCode,
			"finished_at": r.FinishedAt,
			"created_at":  r.CreatedAt,
		})
	}
	return out
}

func adminGetTasks(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	dbTasks, err := tasks.GetAllTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve tasks: "+err.Error(), nil)
	}
	responseTasks := []map[string]any{}
	for _, t := range dbTasks {
		results, err := tasks.GetTaskResultsByTaskId(t.TaskId)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve task results: "+err.Error(), nil)
		}
		responseTasks = append(responseTasks, map[string]any{
			"task_id": t.TaskId,
			"clients": t.Clients,
			"command": t.Command,
			"results": projectTaskResults(results),
		})
	}
	return responseTasks, nil
}

func adminGetTaskById(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	req.BindParams(&params)
	if params.TaskID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Task ID is required", nil)
	}
	task, err := tasks.GetTaskByTaskId(params.TaskID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve task: "+err.Error(), nil)
	}
	if task == nil {
		return nil, rpc.MakeError(rpc.NotFound, "Task not found", nil)
	}
	results, err := tasks.GetTaskResultsByTaskId(params.TaskID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve task results: "+err.Error(), nil)
	}
	return map[string]any{
		"task_id": task.TaskId,
		"clients": task.Clients,
		"command": task.Command,
		"results": projectTaskResults(results),
	}, nil
}

func adminGetTasksByClientId(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Client ID is required", nil)
	}
	list, err := tasks.GetTasksByClientId(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve tasks: "+err.Error(), nil)
	}
	if len(list) == 0 {
		return nil, rpc.MakeError(rpc.NotFound, "No tasks found for this client", nil)
	}
	return list, nil
}

func adminGetSpecificTaskResult(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		TaskID string `json:"task_id"`
		UUID   string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.TaskID == "" || params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Task ID and Client ID are required", nil)
	}
	result, err := tasks.GetSpecificTaskResult(params.TaskID, params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve task result: "+err.Error(), nil)
	}
	if result == nil {
		return nil, rpc.MakeError(rpc.NotFound, "No result found for this task and client", nil)
	}
	return result, nil
}

func adminGetTaskResultsByTaskId(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	req.BindParams(&params)
	if params.TaskID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Task ID is required", nil)
	}
	results, err := tasks.GetTaskResultsByTaskId(params.TaskID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve task results: "+err.Error(), nil)
	}
	if len(results) == 0 {
		return nil, rpc.MakeError(rpc.NotFound, "No results found for this task", nil)
	}
	return results, nil
}
