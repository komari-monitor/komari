package jsonrpc

import (
	"context"
	"strconv"

	"github.com/komari-monitor/komari/database/auditlog"
	clipboardDB "github.com/komari-monitor/komari/database/clipboard"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.clipboard.go
// 剪贴板 RPC2 方法（admin 命名空间）。

func init() {
	reg("getClipboard", adminGetClipboard, "Get a clipboard entry by id")
	reg("listClipboard", adminListClipboard, "List clipboard entries")
	reg("createClipboard", adminCreateClipboard, "Create a clipboard entry")
	reg("updateClipboard", adminUpdateClipboard, "Update a clipboard entry")
	reg("deleteClipboard", adminDeleteClipboard, "Delete a clipboard entry")
	reg("batchDeleteClipboard", adminBatchDeleteClipboard, "Batch delete clipboard entries")
}

func adminGetClipboard(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID string `json:"id"`
	}
	req.BindParams(&params)
	id, err := strconv.Atoi(params.ID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid ID", nil)
	}
	cb, err := clipboardDB.GetClipboardByID(id)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get clipboard: "+err.Error(), nil)
	}
	return cb, nil
}

func adminListClipboard(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	list, err := clipboardDB.ListClipboard()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list clipboard: "+err.Error(), nil)
	}
	return list, nil
}

func adminCreateClipboard(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var cb models.Clipboard
	if err := req.BindParams(&cb); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request: "+err.Error(), nil)
	}
	if err := clipboardDB.CreateClipboard(&cb); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to create clipboard: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "create clipboard:"+strconv.Itoa(cb.Id), "info")
	return cb, nil
}

func adminUpdateClipboard(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// id 来自路径参数（bridge 注入），其余字段为更新内容。
	fields := map[string]interface{}{}
	if err := req.BindParams(&fields); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request: "+err.Error(), nil)
	}
	idStr, _ := fields["id"].(string)
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid ID", nil)
	}
	delete(fields, "id") // 路径注入的 id 不作为更新字段
	if err := clipboardDB.UpdateClipboardFields(id, fields); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to update clipboard: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "update clipboard:"+strconv.Itoa(id), "info")
	return nil, nil
}

func adminDeleteClipboard(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID string `json:"id"`
	}
	req.BindParams(&params)
	id, err := strconv.Atoi(params.ID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid ID", nil)
	}
	if err := clipboardDB.DeleteClipboard(id); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete clipboard: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "delete clipboard:"+strconv.Itoa(id), "warn")
	return nil, nil
}

func adminBatchDeleteClipboard(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		IDs []int `json:"ids"`
	}
	req.BindParams(&params)
	if len(params.IDs) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "IDs cannot be empty", nil)
	}
	if err := clipboardDB.DeleteClipboardBatch(params.IDs); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to batch delete clipboard: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "batch delete clipboard: "+strconv.Itoa(len(params.IDs))+" items", "warn")
	return nil, nil
}
