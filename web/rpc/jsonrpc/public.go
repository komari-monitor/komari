package jsonrpc

import (
	"context"
	"strconv"
	"time"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/utils"
	report_cache "github.com/komari-monitor/komari/web/report"
)

// public.go
// 公开（guest 可访问）的只读 RPC2 方法。命名空间 public:* 对 guest 开放。
// 这些方法保持与原 REST 接口完全一致的响应形状。

func init() {
	rpc.Allow("public:*", rpc.RoleGuest)
	regPublic("getMe", publicGetMe, "Get current user info (guest-aware)")
	regPublic("getNodesInformation", publicGetNodesInformation, "List visible nodes (basic info)")
	regPublic("getPublicSettings", publicGetPublicSettings, "Get public site settings")
	regPublic("getVersion", publicGetVersion, "Get server version")
	regPublic("getClientRecentRecords", publicGetClientRecentRecords, "Get a client's recent records")
	regPublic("getRecordsByUUID", publicGetRecordsByUUID, "Get load records for a client")
	regPublic("getPingRecords", publicGetPingRecords, "Get ping records")
	regPublic("getPublicPingTasks", publicGetPublicPingTasks, "List public ping tasks")
}

func regPublic(name string, h rpc.Handler, summary string) {
	RegisterWithGroupAndMeta(name, "public", h, &rpc.MethodMeta{Name: "public:" + name, Summary: summary})
}

// isLoginFromCtx 依据 meta 判断是否为已登录管理员。
func isLoginFromCtx(ctx context.Context) bool {
	if meta := rpc.MetaFromContext(ctx); meta != nil {
		return meta.Permission == rpc.RoleAdmin
	}
	return false
}

func publicGetNodesInformation(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	clientList, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	isLogin := isLoginFromCtx(ctx)
	j := 0
	for i := 0; i < len(clientList); i++ {
		if clientList[i].Hidden && !isLogin {
			continue
		}
		clientList[i].IPv4 = ""
		clientList[i].IPv6 = ""
		clientList[i].Remark = ""
		clientList[i].Version = ""
		clientList[i].Token = ""
		clientList[j] = clientList[i]
		j++
	}
	clientList = clientList[:j]
	return clientList, nil
}

func publicGetPublicSettings(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	p, e := database.GetPublicInfo()
	if e != nil {
		return nil, rpc.MakeError(rpc.InternalError, e.Error(), nil)
	}
	// 临时访问许可由 transport 层在 meta 标注；此处沿用原逻辑判断 temp_key。
	if meta := rpc.MetaFromContext(ctx); meta != nil && meta.TempShareValid {
		p["private_site"] = false
	}
	return p, nil
}

func publicGetVersion(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	return map[string]any{
		"version": utils.CurrentVersion,
		"hash":    utils.VersionHash,
	}, nil
}

// publicGetMe 返回当前用户信息；未登录时返回 Guest 占位，保持原 /api/me 的扁平形状。
func publicGetMe(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	guest := map[string]any{"username": "Guest", "logged_in": false}
	meta := rpc.MetaFromContext(ctx)
	if meta == nil || meta.User == nil {
		return guest, nil
	}
	u := meta.User
	return map[string]any{
		"username":    u.Username,
		"logged_in":   true,
		"uuid":        u.UUID,
		"sso_type":    u.SSOType,
		"sso_id":      u.SSOID,
		"2fa_enabled": u.TwoFactor != "",
	}, nil
}

func publicGetClientRecentRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	if !isLoginFromCtx(ctx) && isHiddenClient(params.UUID) {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil) // 防止未登录获取隐藏客户端
	}
	recs, _ := report_cache.Records.Get(params.UUID)
	return recs, nil
}

// isHiddenClient 查询指定 uuid 是否为隐藏节点。
func isHiddenClient(uuid string) bool {
	var hiddenClients []models.Client
	db := dbcore.GetDBInstance()
	_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
	for _, cli := range hiddenClients {
		if cli.UUID == uuid {
			return true
		}
	}
	return false
}

func publicGetRecordsByUUID(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID     string `json:"uuid"`
		LoadType string `json:"load_type"`
		Hours    string `json:"hours"`
	}
	req.BindParams(&params)
	isLogin := isLoginFromCtx(ctx)
	if !isLogin && params.UUID != "" && isHiddenClient(params.UUID) {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID is required", nil)
	}
	hours := params.Hours
	if hours == "" {
		hours = "4"
	}
	hoursInt, err := strconv.Atoi(hours)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid hours parameter", nil)
	}
	validLoadTypes := map[string]bool{
		"cpu": true, "ram": true, "swap": true,
		"load": true, "temp": true, "disk": true, "network": true,
		"process": true, "connections": true, "all": true, "": true,
	}
	if !validLoadTypes[params.LoadType] {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid load_type parameter", nil)
	}
	clientRecords, err := records.GetRecordsByClientAndTime(params.UUID, time.Now().Add(-time.Duration(hoursInt)*time.Hour), time.Now())
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch records: "+err.Error(), nil)
	}
	response := map[string]any{
		"records": clientRecords,
		"count":   len(clientRecords),
	}
	if params.LoadType != "" && params.LoadType != "all" {
		filtered := filterPublicRecordsByLoadType(clientRecords, params.LoadType)
		response = map[string]any{
			"records":   filtered,
			"count":     len(filtered),
			"load_type": params.LoadType,
		}
	}
	if params.LoadType == "" || params.LoadType == "all" || params.LoadType == "gpu" {
		gpuRecords, err := records.GetGPURecordsByClientAndTime(params.UUID, time.Now().Add(-time.Duration(hoursInt)*time.Hour), time.Now())
		if err == nil && len(gpuRecords) > 0 {
			gpuDevices := make(map[string]any)
			for _, record := range gpuRecords {
				deviceKey := strconv.Itoa(record.DeviceIndex)
				if gpuDevices[deviceKey] == nil {
					gpuDevices[deviceKey] = map[string]any{
						"device_index": record.DeviceIndex,
						"device_name":  record.DeviceName,
						"records":      []models.GPURecord{},
					}
				}
				device := gpuDevices[deviceKey].(map[string]any)
				recs := device["records"].([]models.GPURecord)
				device["records"] = append(recs, record)
				gpuDevices[deviceKey] = device
			}
			response["gpu_devices"] = gpuDevices
			response["has_gpu_data"] = true
		} else {
			response["has_gpu_data"] = false
		}
	}
	return response, nil
}

func publicGetPublicPingTasks(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	pingTasks, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	type publicPingTask struct {
		Id        uint     `json:"id"`
		Name      string   `json:"name"`
		Clients   []string `json:"clients"`
		DefaultOn bool     `json:"default_on"`
		Type      string   `json:"type"`
		Interval  int      `json:"interval"`
	}
	out := make([]publicPingTask, len(pingTasks))
	for i, task := range pingTasks {
		out[i] = publicPingTask{
			Id:        task.Id,
			Name:      task.Name,
			Clients:   task.Clients,
			DefaultOn: task.DefaultOn,
			Type:      task.Type,
			Interval:  task.Interval,
		}
	}
	return out, nil
}

// filterPublicRecordsByLoadType 复刻原 public 接口的字段投影逻辑。
func filterPublicRecordsByLoadType(recs []models.Record, loadType string) []map[string]any {
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		record := map[string]any{"client": r.Client, "time": r.Time}
		switch loadType {
		case "cpu":
			record["cpu"] = r.Cpu
		case "gpu":
			record["gpu"] = r.Gpu
		case "ram":
			record["ram"] = r.Ram
			record["ram_total"] = r.RamTotal
			if r.RamTotal > 0 {
				record["ram_percent"] = float32(r.Ram) / float32(r.RamTotal) * 100
			}
		case "swap":
			record["swap"] = r.Swap
			record["swap_total"] = r.SwapTotal
			if r.SwapTotal > 0 {
				record["swap_percent"] = float32(r.Swap) / float32(r.SwapTotal) * 100
			}
		case "load":
			record["load"] = r.Load
		case "temp":
			record["temp"] = r.Temp
		case "disk":
			record["disk"] = r.Disk
			record["disk_total"] = r.DiskTotal
			if r.DiskTotal > 0 {
				record["disk_percent"] = float32(r.Disk) / float32(r.DiskTotal) * 100
			}
		case "network":
			record["net_in"] = r.NetIn
			record["net_out"] = r.NetOut
			record["net_total_up"] = r.NetTotalUp
			record["net_total_down"] = r.NetTotalDown
		case "process":
			record["process"] = r.Process
		case "connections":
			record["connections"] = r.Connections
			record["connections_udp"] = r.ConnectionsUdp
			record["connections_tcp"] = r.Connections - r.ConnectionsUdp
		}
		out = append(out, record)
	}
	return out
}

// PUBLIC_PING_RECORDS_PLACEHOLDER

func publicGetPingRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID   string `json:"uuid"`
		TaskID string `json:"task_id"`
		Hours  string `json:"hours"`
	}
	req.BindParams(&params)
	if params.UUID == "" && params.TaskID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "UUID or task_id is required", nil)
	}
	isLogin := isLoginFromCtx(ctx)

	type recordsResp struct {
		TaskId uint   `json:"task_id,omitempty"`
		Time   string `json:"time"`
		Value  int    `json:"value"`
		Client string `json:"client,omitempty"`
	}
	type clientBasicInfo struct {
		Client string  `json:"client"`
		Loss   float64 `json:"loss"`
		Min    int     `json:"min"`
		Max    int     `json:"max"`
	}
	type resp struct {
		Count     int               `json:"count"`
		BasicInfo []clientBasicInfo `json:"basic_info,omitempty"`
		Records   []recordsResp     `json:"records"`
		Tasks     []map[string]any  `json:"tasks,omitempty"`
	}

	hiddenMap := map[string]bool{}
	response := &resp{Count: 0, Records: []recordsResp{}}

	if !isLogin {
		var hiddenClients []models.Client
		db := dbcore.GetDBInstance()
		_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
		for _, cli := range hiddenClients {
			hiddenMap[cli.UUID] = true
		}
		if params.UUID != "" && hiddenMap[params.UUID] {
			return response, nil // 对尝试获取隐藏 uuid 返回空
		}
	}

	hours := params.Hours
	if hours == "" {
		hours = "4"
	}
	hoursInt, err := strconv.Atoi(hours)
	if err != nil {
		hoursInt = 4
	}
	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(hoursInt) * time.Hour)

	taskId := -1
	if params.TaskID != "" {
		taskId, err = strconv.Atoi(params.TaskID)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Invalid task_id parameter", nil)
		}
	}

	recs, err := tasks.GetPingRecords(params.UUID, taskId, startTime, endTime)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping records: "+err.Error(), nil)
	}

	clientStats := make(map[string]struct {
		total, loss, min, max int
	})
	for _, r := range recs {
		if r.Client != "" && !isLogin && hiddenMap[r.Client] {
			continue
		}
		rec := recordsResp{Time: r.Time.ToTime().Format(time.RFC3339), Value: r.Value, Client: r.Client, TaskId: r.TaskId}
		stats := clientStats[r.Client]
		stats.total++
		if r.Value < 0 {
			stats.loss++
		} else {
			if stats.min == 0 || r.Value < stats.min {
				stats.min = r.Value
			}
			if r.Value > stats.max {
				stats.max = r.Value
			}
		}
		clientStats[r.Client] = stats
		response.Records = append(response.Records, rec)
	}

	if len(clientStats) > 0 {
		response.BasicInfo = make([]clientBasicInfo, 0, len(clientStats))
		for client, stats := range clientStats {
			if client != "" && !isLogin && hiddenMap[client] {
				continue
			}
			loss := float64(0)
			if stats.total > 0 {
				loss = float64(stats.loss) / float64(stats.total) * 100
			}
			response.BasicInfo = append(response.BasicInfo, clientBasicInfo{Client: client, Loss: loss, Min: stats.min, Max: stats.max})
		}
	}

	if params.UUID != "" || taskId != -1 {
		pingTasks, err := tasks.GetAllPingTasks()
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping tasks: "+err.Error(), nil)
		}
		tasksList := make([]map[string]any, 0, len(pingTasks))
		for _, t := range pingTasks {
			if taskId != -1 && t.Id != uint(taskId) {
				continue
			}
			if params.UUID != "" && !t.AppliesToClient(params.UUID) {
				continue
			}
			totalCount, lossCount, minLatency, maxLatency, sumLatency, validCount := 0, 0, 0, 0, 0, 0
			for _, r := range recs {
				if r.TaskId != t.Id {
					continue
				}
				if params.UUID != "" && r.Client != params.UUID {
					continue
				}
				totalCount++
				if r.Value < 0 {
					lossCount++
				} else {
					validCount++
					sumLatency += r.Value
					if minLatency == 0 || r.Value < minLatency {
						minLatency = r.Value
					}
					if r.Value > maxLatency {
						maxLatency = r.Value
					}
				}
			}
			lossRate := float64(0)
			if totalCount > 0 {
				lossRate = float64(lossCount) / float64(totalCount) * 100
			}
			avgLatency := 0
			if validCount > 0 {
				avgLatency = sumLatency / validCount
			}
			taskInfo := map[string]any{
				"id": t.Id, "name": t.Name, "type": t.Type, "interval": t.Interval,
				"default_on": t.DefaultOn, "loss": lossRate, "min": minLatency,
				"max": maxLatency, "avg": avgLatency, "total": totalCount,
			}
			if params.UUID == "" && taskId != -1 {
				taskInfo["clients"] = t.Clients
			}
			tasksList = append(tasksList, taskInfo)
		}
		response.Tasks = tasksList
	}

	response.Count = len(response.Records)
	return response, nil
}
