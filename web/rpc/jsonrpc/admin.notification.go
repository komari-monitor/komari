package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/utils/messageSender"
	"gorm.io/gorm/clause"
)

// admin.notification.go
// 通知相关 RPC2 方法（admin 命名空间）：离线通知、发送通知。

func init() {
	reg("listNotificationChannels", adminListNotificationChannels, "List notification channels")
	reg("getNotificationChannelConfiguration", adminGetNotificationChannelConfiguration, "Get notification channel configuration")
	reg("setNotificationChannelConfiguration", adminSetNotificationChannelConfiguration, "Set notification channel configuration")
	// offline notifications
	reg("listOfflineNotifications", adminListOfflineNotifications, "List offline notifications")
	reg("editOfflineNotification", adminEditOfflineNotification, "Edit offline notifications")
	reg("enableOfflineNotification", adminEnableOfflineNotification, "Enable offline notifications for clients")
	reg("disableOfflineNotification", adminDisableOfflineNotification, "Disable offline notifications for clients")
	// send notification
	reg("sendNotification", adminSendNotification, "Send a notification")
}

func adminListNotificationChannels(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return messageSender.ListNotificationChannels(), nil
}

func adminGetNotificationChannelConfiguration(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID string `json:"id"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data: "+err.Error(), nil)
	}
	configuration, values, exists, err := messageSender.GetNotificationChannelConfiguration(params.ID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to load notification channel configuration: "+err.Error(), nil)
	}
	if !exists {
		return nil, rpc.MakeError(rpc.NotFound, "Notification channel not found: "+params.ID, nil)
	}
	return map[string]any{
		"configuration": configuration,
		"data":          values,
	}, nil
}

func adminSetNotificationChannelConfiguration(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID   string         `json:"id"`
		Data map[string]any `json:"data"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data: "+err.Error(), nil)
	}
	if !messageSender.NotificationChannelRegistered(params.ID) {
		return nil, rpc.MakeError(rpc.NotFound, "Notification channel not found: "+params.ID, nil)
	}
	if params.Data == nil {
		params.Data = map[string]any{}
	}
	addition, err := json.Marshal(params.Data)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid notification channel configuration: "+err.Error(), nil)
	}
	if err := database.SaveMessageSenderConfig(&models.MessageSenderProvider{
		Name:     params.ID,
		Addition: string(addition),
	}); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to save notification channel configuration: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	sanitized := sanitizeNotificationChannelAuditMap(params.Data)
	auditlog.Log(ip, actor, auditlog.JoinLines(
		"Update notification channel config: channel="+params.ID,
		auditlog.FormatMap(sanitized)...,
	), "info")
	return nil, nil
}

func sanitizeNotificationChannelAuditMap(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		lower := strings.ToLower(strings.TrimSpace(k))
		switch lower {
		case "headers":
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out[k] = "[REDACTED]"
			} else {
				out[k] = v
			}
		case "script":
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out[k] = fmt.Sprintf("[SCRIPT, %d chars]", utf8.RuneCountInString(s))
			} else {
				out[k] = v
			}
		default:
			out[k] = v
		}
	}
	return out
}

// adminSendNotification 发送一条通知。仅供外部（插件/脚本）通过 RPC 调用，
// 内部通知逻辑（notifier/renewal/session 等）直接调用 messageSender.SendNotification。
func adminSendNotification(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Event models.EventMessage `json:"event"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data: "+err.Error(), nil)
	}
	if fmt.Sprint(params.Event.Event) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "event is required", nil)
	}
	actor, ip := auditActor(ctx)
	if err := messageSender.SendNotificationWithActor(ip, actor, params.Event); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to send notification: "+err.Error(), nil)
	}
	return nil, nil
}

// reg 是 admin 命名空间方法的注册便捷封装。
func reg(name string, h rpc.Handler, summary string) {
	RegisterWithGroupAndMeta(name, rpc.RoleAdmin, h, &rpc.MethodMeta{Name: "admin:" + name, Summary: summary})
}

func adminListOfflineNotifications(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.OfflineNotification
	if err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).Find(&notifications).Error; err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list offline notifications: "+err.Error(), nil)
	}
	return notifications, nil
}

func adminEditOfflineNotification(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.OfflineNotification
	if err := req.BindParams(&notifications); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(notifications) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "At least one notification is required", nil)
	}
	for _, noti := range notifications {
		if noti.Client == "" {
			return nil, rpc.MakeError(rpc.InvalidParams, "Client UUID cannot be empty", nil)
		}
		if noti.GracePeriod <= 0 {
			return nil, rpc.MakeError(rpc.InvalidParams, "GracePeriod must be a positive integer", nil)
		}
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable", "grace_period"}),
		}).
		Select("*").Create(notifications).Error
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to edit offline notifications: "+err.Error(), nil)
	}
	uuids := make([]string, len(notifications))
	for i, noti := range notifications {
		uuids[i] = noti.Client
	}
	labels := auditlog.FormatClientUUIDs(uuids)
	details := make([]string, 0, len(notifications))
	for i, noti := range notifications {
		label := noti.Client
		if i < len(labels) && labels[i] != "" {
			label = labels[i]
		}
		details = append(details, fmt.Sprintf("%s (enabled=%t, grace=%ds)", label, noti.Enable, noti.GracePeriod))
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, auditlog.JoinLines(
		fmt.Sprintf("Update offline notifications: count=%d", len(notifications)),
		details...,
	), "info")
	return nil, nil
}

// setOfflineNotificationEnable 是 enable/disable 的共享实现。
func setOfflineNotificationEnable(ctx context.Context, req *rpc.JsonRpcRequest, enable bool) *rpc.JsonRpcError {
	var uuids []string
	if err := req.BindParams(&uuids); err != nil {
		return rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	notifications := make([]models.OfflineNotification, 0, len(uuids))
	for _, uuid := range uuids {
		notifications = append(notifications, models.OfflineNotification{Client: uuid, Enable: enable})
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").Create(notifications).Error
	if err != nil {
		return rpc.MakeError(rpc.InternalError, "Failed to update offline notifications: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	action := "Disable offline notifications"
	if enable {
		action = "Enable offline notifications"
	}
	auditlog.Log(ip, actor, fmt.Sprintf("%s: %s", action, auditlog.JoinLabels(auditlog.FormatClientUUIDs(uuids))), "info")
	return nil
}

func adminEnableOfflineNotification(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(ctx, req, true); e != nil {
		return nil, e
	}
	return nil, nil
}

func adminDisableOfflineNotification(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(ctx, req, false); e != nil {
		return nil, e
	}
	return nil, nil
}
