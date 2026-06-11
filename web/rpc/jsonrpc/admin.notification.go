package jsonrpc

import (
	"context"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/pkg/rpc"
	"gorm.io/gorm/clause"
)

// admin.notification.go
// 通知相关 RPC2 方法（admin 命名空间）：负载告警、离线通知、流量报告。

func init() {
	// load notifications
	reg("addLoadNotification", adminAddLoadNotification, "Create a load notification")
	reg("deleteLoadNotification", adminDeleteLoadNotification, "Delete load notifications by ids")
	reg("editLoadNotification", adminEditLoadNotification, "Edit load notifications")
	reg("getAllLoadNotifications", adminGetAllLoadNotifications, "List all load notifications")
	// offline notifications
	reg("listOfflineNotifications", adminListOfflineNotifications, "List offline notifications")
	reg("editOfflineNotification", adminEditOfflineNotification, "Edit offline notifications")
	reg("enableOfflineNotification", adminEnableOfflineNotification, "Enable offline notifications for clients")
	reg("disableOfflineNotification", adminDisableOfflineNotification, "Disable offline notifications for clients")
	// traffic report notifications
	reg("listTrafficReportNotifications", adminListTrafficReport, "List traffic report notifications")
	reg("editTrafficReportNotifications", adminEditTrafficReport, "Edit traffic report notifications")
	reg("enableTrafficReportNotifications", adminEnableTrafficReport, "Enable traffic report notifications")
	reg("disableTrafficReportNotifications", adminDisableTrafficReport, "Disable traffic report notifications")
}

// reg 是 admin 命名空间方法的注册便捷封装。
func reg(name string, h rpc.Handler, summary string) {
	RegisterWithGroupAndMeta(name, rpc.RoleAdmin, h, &rpc.MethodMeta{Name: "admin:" + name, Summary: summary})
}

func adminAddLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Clients   []string `json:"clients"`
		Name      string   `json:"name"`
		Metric    string   `json:"metric"`
		Threshold float32  `json:"threshold"`
		Ratio     float32  `json:"ratio"`
		Interval  int      `json:"interval"`
	}
	req.BindParams(&params)
	if len(params.Clients) == 0 || params.Metric == "" || params.Threshold == 0 || params.Ratio == 0 || params.Interval == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "clients, metric, threshold, ratio and interval are required", nil)
	}
	if params.Interval > 4*60 || params.Interval <= 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Interval must be between 1 and 240 minutes", nil)
	}
	if params.Ratio <= 0 || params.Ratio > 1 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Ratio must be between 0 and 1", nil)
	}
	taskID, err := notification.AddLoadNotification(params.Clients, params.Name, params.Metric, params.Threshold, params.Ratio, params.Interval)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return map[string]any{"task_id": taskID}, nil
}

func adminDeleteLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		ID []uint `json:"id"`
	}
	req.BindParams(&params)
	if len(params.ID) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "id is required", nil)
	}
	if err := notification.DeleteLoadNotification(params.ID); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminEditLoadNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Notifications []*models.LoadNotification `json:"notifications"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request data", nil)
	}
	if err := notification.EditLoadNotification(params.Notifications); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return nil, nil
}

func adminGetAllLoadNotifications(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	list, err := notification.GetAllLoadNotifications()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return list, nil
}

func adminListOfflineNotifications(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.OfflineNotification
	if err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).Find(&notifications).Error; err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list offline notifications: "+err.Error(), nil)
	}
	return notifications, nil
}

func adminEditOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
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
	return nil, nil
}

// setOfflineNotificationEnable 是 enable/disable 的共享实现。
func setOfflineNotificationEnable(req *rpc.JsonRpcRequest, enable bool) *rpc.JsonRpcError {
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
	return nil
}

func adminEnableOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(req, true); e != nil {
		return nil, e
	}
	return nil, nil
}

func adminDisableOfflineNotification(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if e := setOfflineNotificationEnable(req, false); e != nil {
		return nil, e
	}
	return nil, nil
}

func adminListTrafficReport(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	list, err := notification.ListTrafficReportNotifications()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list traffic report notifications: "+err.Error(), nil)
	}
	return list, nil
}

func adminEditTrafficReport(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var notifications []models.TrafficReportNotification
	if err := req.BindParams(&notifications); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if len(notifications) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "At least one notification is required", nil)
	}
	if err := notification.ValidateTrafficReportNotifications(notifications); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	if err := notification.EditTrafficReportNotifications(notifications); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to edit traffic report notifications: "+err.Error(), nil)
	}
	return nil, nil
}

func adminEnableTrafficReport(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var uuids []string
	if err := req.BindParams(&uuids); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if err := notification.EnableTrafficReportNotifications(uuids); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to enable traffic report notifications: "+err.Error(), nil)
	}
	return nil, nil
}

func adminDisableTrafficReport(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var uuids []string
	if err := req.BindParams(&uuids); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	if err := notification.DisableTrafficReportNotifications(uuids); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to disable traffic report notifications: "+err.Error(), nil)
	}
	return nil, nil
}
