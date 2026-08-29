package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/filemanager"
)

func init() {
	reg("fileList", adminFileList, "List a directory on an agent")
	reg("fileListRoots", adminFileListRoots, "List filesystem roots exposed by an agent")
	reg("fileStat", adminFileStat, "Read file metadata from an agent")
	// File contents are served by the binary /file/download stream endpoint;
	// JSON-RPC stays metadata/control-only.
	reg("fileMkdir", adminFileMkdir, "Create a directory on an agent")
	reg("fileDelete", adminFileDelete, "Delete a path on an agent")
	reg("fileMove", adminFileMove, "Move or rename a path on an agent")
	reg("fileCopy", adminFileCopy, "Copy a file on an agent")
	reg("fileChmod", adminFileChmod, "Change file permissions on an agent")
	reg("fileChown", adminFileChown, "Change file ownership on a Unix agent")
	reg("fileSearch", adminFileSearch, "Search paths or file contents on an agent")
}

type filePathParams struct {
	UUID string `json:"uuid"`
	Path string `json:"path"`
}

func adminFileListRoots(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params filePathParams
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	return callAgentFile(ctx, params.UUID, "list_roots", map[string]any{"path": ""}, "", false)
}

func adminFileList(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params filePathParams
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	return callAgentFile(ctx, params.UUID, "list", map[string]any{"path": params.Path}, "", false)
}

func adminFileStat(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params filePathParams
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	return callAgentFile(ctx, params.UUID, "stat", map[string]any{"path": params.Path}, params.Path, false)
}

func adminFileMkdir(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		filePathParams
		Mode string `json:"mode"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	return callAgentFile(ctx, params.UUID, "mkdir", map[string]any{"path": params.Path, "mode": params.Mode}, params.Path, true)
}

func adminFileDelete(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params filePathParams
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	return callAgentFile(ctx, params.UUID, "delete", map[string]any{"path": params.Path}, params.Path, true)
}

func adminFileMove(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID        string `json:"uuid"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if strings.TrimSpace(params.Source) == "" || strings.TrimSpace(params.Destination) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "source and destination are required", nil)
	}
	return callAgentFile(ctx, params.UUID, "move", map[string]any{
		"source": params.Source, "destination": params.Destination,
	}, params.Source+" -> "+params.Destination, true)
}

func adminFileCopy(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID        string `json:"uuid"`
		Source      string `json:"source"`
		Destination string `json:"destination"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if strings.TrimSpace(params.Source) == "" || strings.TrimSpace(params.Destination) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "source and destination are required", nil)
	}
	return callAgentFile(ctx, params.UUID, "copy", map[string]any{
		"source": params.Source, "destination": params.Destination,
	}, params.Source+" -> "+params.Destination, true)
}
func adminFileChmod(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		filePathParams
		Mode string `json:"mode"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Mode) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "mode is required", nil)
	}
	return callAgentFile(ctx, params.UUID, "chmod", map[string]any{"path": params.Path, "mode": params.Mode}, params.Path, true)
}

func adminFileChown(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		filePathParams
		UID   *int   `json:"uid"`
		GID   *int   `json:"gid"`
		Owner string `json:"owner"`
		Group string `json:"group"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	if params.UID == nil && params.GID == nil &&
		strings.TrimSpace(params.Owner) == "" && strings.TrimSpace(params.Group) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uid, gid, owner or group is required", nil)
	}
	args := map[string]any{"path": params.Path}
	if params.UID != nil {
		args["uid"] = *params.UID
	}
	if params.GID != nil {
		args["gid"] = *params.GID
	}
	if params.Owner != "" {
		args["owner"] = params.Owner
	}
	if params.Group != "" {
		args["group"] = params.Group
	}
	return callAgentFile(ctx, params.UUID, "chown", args, params.Path, true)
}

func adminFileSearch(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		filePathParams
		Query   string `json:"query"`
		Content bool   `json:"content"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, invalidFileParams(err)
	}
	if err := requireFilePath(params.Path); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "query is required", nil)
	}
	return callAgentFileWithOptions(ctx, params.UUID, "search", map[string]any{
		"path": params.Path, "query": params.Query, "content": params.Content,
	}, params.Path, false, filemanager.CallOptions{Timeout: 90 * time.Second})
}

func callAgentFile(ctx context.Context, uuid, operation string, args map[string]any, auditPath string, mutating bool) (any, *rpc.JsonRpcError) {
	return callAgentFileWithOptions(ctx, uuid, operation, args, auditPath, mutating, filemanager.CallOptions{})
}

func callAgentFileWithOptions(ctx context.Context, uuid, operation string, args map[string]any, auditPath string, mutating bool, options filemanager.CallOptions) (any, *rpc.JsonRpcError) {
	if strings.TrimSpace(uuid) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	if _, err := clients.GetClientByUUID(uuid); err != nil {
		return nil, rpc.MakeError(rpc.NotFound, "client not found", nil)
	}
	raw, err := filemanager.Call(ctx, uuid, operation, args, options)
	if err != nil {
		return nil, fileOperationError(err)
	}
	var result any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "invalid file response from agent", nil)
		}
	}
	if mutating {
		actor, ip := auditActor(ctx)
		auditlog.Log(ip, actor, fmt.Sprintf("file %s: %s:%s", operation, uuid, auditPath), "warn")
	}
	return result, nil
}

func invalidFileParams(err error) *rpc.JsonRpcError {
	return rpc.MakeError(rpc.InvalidParams, "invalid request: "+err.Error(), nil)
}

func requireFilePath(path string) *rpc.JsonRpcError {
	if strings.TrimSpace(path) == "" {
		return rpc.MakeError(rpc.InvalidParams, "path is required", nil)
	}
	return nil
}

func fileOperationError(err error) *rpc.JsonRpcError {
	code := rpc.InternalError
	switch {
	case errors.Is(err, filemanager.ErrOffline):
		code = rpc.Unavailable
	case errors.Is(err, filemanager.ErrUnsupported):
		code = rpc.Unimplemented
	case errors.Is(err, filemanager.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		code = rpc.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		code = rpc.Cancelled
	case errors.Is(err, filemanager.ErrUnknownToken):
		code = rpc.NotFound
	default:
		message := strings.ToLower(err.Error())
		switch {
		case strings.Contains(message, "unsupported file operation"):
			code = rpc.Unimplemented
		case strings.Contains(message, "permission denied") || strings.Contains(message, "operation not permitted"):
			code = rpc.PermissionDenied
		case strings.Contains(message, "no such file") || strings.Contains(message, "cannot find") || strings.Contains(message, "not found"):
			code = rpc.NotFound
		}
	}
	return rpc.MakeError(code, err.Error(), nil)
}
