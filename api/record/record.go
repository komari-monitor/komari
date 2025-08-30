package record

import (
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	records "github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
)

func GetRecordsByUUID(c *gin.Context) {
	uuid := c.Query("uuid")
	loadType := c.Query("load_type")

	// 登录状态检查
	isLogin := false
	session, _ := c.Cookie("session_token")
	_, err := accounts.GetUserBySession(session)
	if err == nil {
		isLogin = true
	}

	// 仅在未登录时需要 Hidden 信息做过滤
	hiddenMap := map[string]bool{}
	if !isLogin {
		var hiddenClients []models.Client
		db := dbcore.GetDBInstance()
		_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
		for _, cli := range hiddenClients {
			hiddenMap[cli.UUID] = true
		}

		if hiddenMap[uuid] {
			api.RespondError(c, 400, "UUID is required") //防止未登录用户获取隐藏客户端数据
			return
		}
	}

	hours := c.Query("hours")
	if uuid == "" {
		api.RespondError(c, 400, "UUID is required")
		return
	}
	if hours == "" {
		hours = "4"
	}

	hoursInt, err := strconv.Atoi(hours)
	if err != nil {
		api.RespondError(c, 400, "Invalid hours parameter")
		return
	}

	// 验证 load_type 参数
	validLoadTypes := map[string]bool{
		"cpu": true, "gpu": true, "ram": true, "swap": true,
		"load": true, "temp": true, "disk": true, "network": true,
		"process": true, "connections": true, "all": true, "": true,
	}
	
	if !validLoadTypes[loadType] {
		api.RespondError(c, 400, "Invalid load_type parameter")
		return
	}

	records, err := records.GetRecordsByClientAndTime(uuid, time.Now().Add(-time.Duration(hoursInt)*time.Hour), time.Now())
	if err != nil {
		api.RespondError(c, 500, "Failed to fetch records: "+err.Error())
		return
	}

	// 根据 load_type 过滤返回的数据
	if loadType != "" && loadType != "all" {
		filteredRecords := filterRecordsByLoadType(records, loadType)
		api.RespondSuccess(c, gin.H{
			"records":   filteredRecords,
			"count":     len(filteredRecords),
			"load_type": loadType,
		})
	} else {
		// 返回所有数据（向后兼容）
		api.RespondSuccess(c, gin.H{
			"records": records,
			"count":   len(records),
		})
	}
}

// filterRecordsByLoadType 根据 load_type 过滤记录，只返回相关字段
func filterRecordsByLoadType(records []models.Record, loadType string) []gin.H {
	filteredRecords := make([]gin.H, 0, len(records))
	
	for _, r := range records {
		record := gin.H{
			"client": r.Client,
			"time":   r.Time,
		}
		
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
		
		filteredRecords = append(filteredRecords, record)
	}
	
	return filteredRecords
}

// GET query: uuid string, task_id int, hours int
// 支持三种查询模式：
// 1. 仅 uuid - 获取该客户端的所有 ping 任务记录
// 2. 仅 task_id - 获取该任务的所有客户端记录
// 3. uuid + task_id - 获取特定客户端在特定任务的记录
func GetPingRecords(c *gin.Context) {
	uuid := c.Query("uuid")
	taskIdStr := c.Query("task_id")

	// 必须提供 uuid 或 task_id 其中至少一个
	if uuid == "" && taskIdStr == "" {
		api.RespondError(c, 400, "UUID or task_id is required")
		return
	}

	// 登录状态检查
	isLogin := false
	session, _ := c.Cookie("session_token")
	_, err := accounts.GetUserBySession(session)
	if err == nil {
		isLogin = true
	}

	type RecordsResp struct {
		TaskId uint   `json:"task_id,omitempty"`
		Time   string `json:"time"`
		Value  int    `json:"value"`
		Client string `json:"client,omitempty"`
	}
	type ClientBasicInfo struct {
		Client string  `json:"client"`
		Loss   float64 `json:"loss"`
		Min    int     `json:"min"`
		Max    int     `json:"max"`
	}
	type Resp struct {
		Count     int               `json:"count"`
		BasicInfo []ClientBasicInfo `json:"basic_info,omitempty"`
		Records   []RecordsResp     `json:"records"`
		Tasks     []gin.H           `json:"tasks,omitempty"`
	}
	var records []models.PingRecord
	hiddenMap := map[string]bool{}
	response := &Resp{
		Count:   0,
		Records: []RecordsResp{},
	}

	// 仅在未登录时需要 Hidden 信息做过滤
	if !isLogin {
		var hiddenClients []models.Client
		db := dbcore.GetDBInstance()
		_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
		for _, cli := range hiddenClients {
			hiddenMap[cli.UUID] = true
		}
		if uuid != "" {
			if hiddenMap[uuid] {
				api.RespondSuccess(c, response) // 对于尝试获取隐藏uuid一键哈气
				return
			}
		}
	}

	hours := c.Query("hours")

	if hours == "" {
		hours = "4"
	}

	hoursInt, err := strconv.Atoi(hours)
	if err != nil {
		hoursInt = 4
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(hoursInt) * time.Hour)

	// 解析 task_id，支持同时传入 uuid 和 task_id
	taskId := -1
	if taskIdStr != "" {
		taskId, err = strconv.Atoi(taskIdStr)
		if err != nil {
			api.RespondError(c, 400, "Invalid task_id parameter")
			return
		}
	}

	// 查询记录，现在支持 uuid + task_id 组合查询
	records, err = tasks.GetPingRecords(uuid, taskId, startTime, endTime)
	if err != nil {
		api.RespondError(c, 500, "Failed to fetch ping records: "+err.Error())
		return
	}

	// 用于统计每个客户端的信息（按 task_id 查询时使用）
	clientStats := make(map[string]struct {
		total int
		loss  int
		min   int
		max   int
	})

	for _, r := range records {
		if r.Client != "" && !isLogin {
			if hiddenMap[r.Client] {
				continue // 跳过隐藏的节点
			}
		}
		toTime := r.Time.ToTime()
		rec := RecordsResp{
			Time:  toTime.Format(time.RFC3339),
			Value: r.Value,
		}
		rec.Client = r.Client
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
		rec.TaskId = r.TaskId

		response.Records = append(response.Records, rec)
	}

	// 返回 BasicInfo - 按客户端分组的统计信息
	// 在以下情况下特别有用：
	// 1. 仅 task_id 查询 - 查看该任务下所有客户端的表现
	// 2. uuid + task_id 查询 - 查看特定客户端在特定任务的表现
	if len(clientStats) > 0 {
		response.BasicInfo = make([]ClientBasicInfo, 0, len(clientStats))
		for client, stats := range clientStats {
			if client != "" && !isLogin {
				if hiddenMap[client] {
					continue // 跳过隐藏的节点
				}
			}
			loss := float64(0)
			if stats.total > 0 {
				loss = float64(stats.loss) / float64(stats.total) * 100
			}
			response.BasicInfo = append(response.BasicInfo, ClientBasicInfo{
				Client: client,
				Loss:   loss,
				Min:    stats.min,
				Max:    stats.max,
			})
		}
		
		// 如果同时指定了 uuid 和 task_id，BasicInfo 应该只有一条记录
		// 这种情况下可以在响应中标记查询模式
		if uuid != "" && taskId != -1 && len(response.BasicInfo) == 1 {
			// 这是精确查询模式
		}
	}

	// 优化后的任务信息返回逻辑
	// 1. uuid != "" - 返回该客户端参与的所有任务信息
	// 2. uuid != "" && taskId != -1 - 返回该客户端在指定任务的信息
	// 3. taskId != -1 && uuid == "" - 返回该任务的所有客户端统计（通过 BasicInfo）
	if uuid != "" || taskId != -1 {
		// 获取所有 pingTasks
		pingTasks, err := tasks.GetAllPingTasks()
		if err != nil {
			api.RespondError(c, 500, "Failed to fetch ping tasks: "+err.Error())
			return
		}

		tasksList := make([]gin.H, 0, len(pingTasks))
		for _, t := range pingTasks {
			// 如果指定了 taskId，只处理该任务
			if taskId != -1 {
				if t.Id != uint(taskId) {
					continue
				}
			}

			// 如果指定了 uuid，检查任务是否分配给该客户端
			if uuid != "" {
				found := slices.Contains(t.Clients, uuid)
				if !found {
					continue
				}
			}

			// 计算该任务的丢包率和延迟统计
			totalCount := 0
			lossCount := 0
			minLatency := 0
			maxLatency := 0
			avgLatency := 0
			sumLatency := 0
			validCount := 0

			for _, r := range records {
				// 根据查询模式过滤记录
				if r.TaskId != t.Id {
					continue
				}
				// 如果同时指定了 uuid 和 task_id，只统计该客户端的记录
				if uuid != "" && r.Client != uuid {
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

			var lossRate float64 = 0
			if totalCount > 0 {
				lossRate = float64(lossCount) / float64(totalCount) * 100
			}
			if validCount > 0 {
				avgLatency = sumLatency / validCount
			}

			taskInfo := gin.H{
				"id":       t.Id,
				"name":     t.Name,
				"type":     t.Type,
				"interval": t.Interval,
				"loss":     lossRate,
				"min":      minLatency,
				"max":      maxLatency,
				"avg":      avgLatency,
				"total":    totalCount,
			}
			
			// 如果是仅 task_id 查询，添加客户端列表信息
			if uuid == "" && taskId != -1 {
				taskInfo["clients"] = t.Clients
			}
			
			tasksList = append(tasksList, taskInfo)
		}
		response.Tasks = tasksList
	}

	response.Count = len(response.Records) // 计算最后结果保持计数一致
	api.RespondSuccess(c, response)
}
