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

	records, err := records.GetRecordsByClientAndTime(uuid, time.Now().Add(-time.Duration(hoursInt)*time.Hour), time.Now())
	if err != nil {
		api.RespondError(c, 500, "Failed to fetch records: "+err.Error())
		return
	}
	api.RespondSuccess(c, gin.H{
		"records": records,
		"count":   len(records),
	})
}

// GET query: uuid string OR task_id int, hours int, gs int
func GetPingRecords(c *gin.Context) {
	uuid := c.Query("uuid")
	taskIdStr := c.Query("task_id")

	// 必须提供 uuid 或 task_id 其中之一
	if uuid == "" && taskIdStr == "" {
		api.RespondError(c, 400, "UUID or task_id error")
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

	if hoursInt > 720 { // 最大查询30日内数据
		hoursInt = 720
	}

	endTime := time.Now()
	startTime := endTime.Add(-time.Duration(hoursInt) * time.Hour)

	taskId := -1
	taskId, err = strconv.Atoi(taskIdStr)
	if err != nil {
		taskId = -1
	}

	records, err = tasks.GetPingRecords(uuid, taskId, startTime, endTime)
	if err != nil {
		api.RespondError(c, 500, "Failed to fetch ping tasks: "+err.Error())
	}

	lenRecords := len(records)

	// 切分数据优化获取速度
	maxPerWindow := c.Query("gs")
	maxPerWindowInt, err := strconv.Atoi(maxPerWindow)
	if err != nil {
		maxPerWindowInt = 5000
	}
	if maxPerWindowInt < 1 && maxPerWindowInt != -1 {
		maxPerWindowInt = 5000
	}
	if maxPerWindowInt > lenRecords || maxPerWindowInt == -1 {
		maxPerWindowInt = lenRecords
	}
	var granularitySeconds int
	if maxPerWindowInt != lenRecords {
		// 自动切分粒度
		totalSeconds := hoursInt * 3600
		secondsPerRecord := float64(totalSeconds) / float64(lenRecords)     // 平均记录时间差
		densityRecord := float64(lenRecords) / float64(maxPerWindowInt)     // 记录密度
		granularitySeconds = int(float64(secondsPerRecord) * densityRecord) // 平均采样时间差

		// 保证最小粒度是1秒
		if granularitySeconds < 1 {
			granularitySeconds = 1
		}
	} else {
		granularitySeconds = 0 // 绕过计算 使其可以输出全部值
	}
	// 用于统计每个客户端的信息（按 task_id 查询时使用）
	clientStats := make(map[string]struct {
		total int
		loss  int
		min   int
		max   int
	})

	granularityMap := make(map[string]time.Time)
	for _, r := range records {
		if r.Client != "" && !isLogin {
			if hiddenMap[r.Client] {
				continue // 跳过隐藏的节点
			}
		}
		toTime := r.Time.ToTime()
		if granularitySeconds > 0 {
			windowStart, ok := granularityMap[r.Client]
			if !ok {
				granularityMap[r.Client] = toTime.Add(time.Second)
			}
			windowEnd := windowStart.Add(-time.Duration(granularitySeconds) * time.Second)
			if !toTime.After(windowEnd) { // 防止粒度过小 值已经到达末尾之后
				granularityMap[r.Client] = toTime.Add(time.Second)
				windowStart = toTime.Add(time.Second)
				windowEnd = windowStart.Add(-time.Duration(granularitySeconds) * time.Second)
			}
			if toTime.After(windowStart) {
				continue
			}
			granularityMap[r.Client] = windowEnd // 更新起始位置
		}
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

	// 都返回 BasicInfo
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
	}

	// uuid不为空返回全部 为空返回当前任务
	if uuid != "" && len(records) >= 0 {
		// 获取当前属于该 uuid 的 pingTasks
		pingTasks, err := tasks.GetAllPingTasks()
		if err != nil {
			api.RespondError(c, 500, "Failed to fetch ping tasks: "+err.Error())
			return
		}

		tasksList := make([]gin.H, 0, len(pingTasks))
		for _, t := range pingTasks {
			// 只保留当前请求的 taskInfo
			if taskId != -1 && taskIdStr != "" {
				if t.Id != uint(taskId) {
					continue
				}
			}

			// 只保留分配给该 uuid 的任务
			found := slices.Contains(t.Clients, uuid)
			if !found {
				continue
			}

			// 计算该任务的丢包率
			totalCount := 0
			lossCount := 0
			for _, r := range records {
				if r.TaskId == t.Id {
					totalCount++
					if r.Value < 0 {
						lossCount++
					}
				}
			}

			var lossRate float64 = 0
			if totalCount > 0 {
				lossRate = float64(lossCount) / float64(totalCount) * 100
			}

			tasksList = append(tasksList, gin.H{
				"id":       t.Id,
				"name":     t.Name,
				"interval": t.Interval,
				"loss":     lossRate,
			})
		}
		response.Tasks = tasksList
	}

	response.Count = len(response.Records) // 计算最后结果保持计数一致
	api.RespondSuccess(c, response)
}
