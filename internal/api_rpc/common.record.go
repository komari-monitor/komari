package api_rpc

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/komari-monitor/komari/internal/client"
	"github.com/komari-monitor/komari/internal/database/models"
	recordsdb "github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	Register("getRecords", getRecords)
}

func getRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	meta := rpc.MetaFromContext(ctx)
	var params struct {
		Type     string `json:"type"`      // "load" | "ping"; default "load"
		UUID     string `json:"uuid"`      // client uuid; empty = all clients
		Hours    int    `json:"hours"`     // time window in hours; default 1 if start/end not provided
		Start    string `json:"start"`     // RFC3339 start time (optional)
		End      string `json:"end"`       // RFC3339 end time (optional)
		LoadType string `json:"load_type"` // for type=load: cpu|gpu|ram|swap|load|temp|disk|network|process|connections|all
		TaskID   int    `json:"task_id"`   // for type=ping: optional task id; -1 or omitted means all
		MaxCount int    `json:"maxCount"`  // max number of points; -1 unlimited; default 4000
	}
	req.BindParams(&params)

	// defaults
	if params.Type == "" {
		params.Type = "load"
	}
	// parse time window
	var startTime, endTime time.Time
	if params.Start != "" || params.End != "" {
		// allow partial: missing end means now
		var err error
		if params.End == "" {
			endTime = time.Now()
		} else {
			endTime, err = time.Parse(time.RFC3339, params.End)
			if err != nil {
				return nil, rpc.MakeError(rpc.InvalidParams, "Invalid end time", params.End)
			}
		}
		if params.Start == "" {
			// default to 1 hour before end
			startTime = endTime.Add(-1 * time.Hour)
		} else {
			start, err := time.Parse(time.RFC3339, params.Start)
			if err != nil {
				return nil, rpc.MakeError(rpc.InvalidParams, "Invalid start time", params.Start)
			}
			startTime = start
		}
	} else {
		hours := params.Hours
		if hours <= 0 {
			hours = 1 // default 1 hour
		}
		endTime = time.Now()
		startTime = endTime.Add(-time.Duration(hours) * time.Hour)
	}

	// Hidden filtering for non-admin
	isAdmin := meta.Permission == "admin"
	hidden := map[string]bool{}
	if !isAdmin {
		cinfo, err := client.GetAllClientBasicInfo()
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to get client info", err.Error())
		}
		for _, c := range cinfo {
			if c.Hidden {
				hidden[c.UUID] = true
			}
		}
		if params.UUID != "" && hidden[params.UUID] {
			return nil, rpc.MakeError(rpc.InvalidParams, "UUID not found", params.UUID)
		}
	}

	switch params.Type {
	case "load":
		// fetch load records
		recs, err := getLoadRecordsCombined(params.UUID, startTime, endTime)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch records", err.Error())
		}
		// hidden filter on non-admin
		if !isAdmin {
			filtered := recs[:0]
			for _, r := range recs {
				if hidden[r.Client] {
					continue
				}
				filtered = append(filtered, r)
			}
			recs = filtered
		}

		// resolve maxCount default for load
		maxCount := params.MaxCount
		if maxCount == 0 {
			maxCount = 4000
		}

		// optional load_type filtering -> group by client
		if params.LoadType != "" && params.LoadType != "all" {
			items := filterRecordsByLoadType(recs, params.LoadType)
			grouped := make(map[string][]flatRecord)
			for _, it := range items {
				grouped[it.Client] = append(grouped[it.Client], it)
			}
			// sort and count
			total := 0
			groupsMeta := make([]struct {
				name   string
				length int
			}, 0, len(grouped))
			for name := range grouped {
				arr := grouped[name]
				sort.Slice(arr, func(i, j int) bool { return arr[i].Time.ToTime().Before(arr[j].Time.ToTime()) })
				grouped[name] = arr
				l := len(arr)
				total += l
				groupsMeta = append(groupsMeta, struct {
					name   string
					length int
				}{name: name, length: l})
			}
			// downsample across all clients proportionally
			if maxCount != -1 && total > maxCount {
				targets := allocateTargets(groupsMeta, maxCount)
				total = 0
				for name, k := range targets {
					grouped[name] = downsampleFlatRecords(grouped[name], k)
					total += len(grouped[name])
				}
			}
			return struct {
				Count    int                     `json:"count"`
				Records  map[string][]flatRecord `json:"records"`
				LoadType string                  `json:"load_type"`
				From     models.LocalTime        `json:"from"`
				To       models.LocalTime        `json:"to"`
			}{Count: total, Records: grouped, LoadType: params.LoadType, From: models.FromTime(startTime), To: models.FromTime(endTime)}, nil
		}
		// default: return full records, grouped by client
		grouped := make(map[string][]models.Record)
		for _, r := range recs {
			grouped[r.Client] = append(grouped[r.Client], r)
		}
		total := 0
		groupsMeta := make([]struct {
			name   string
			length int
		}, 0, len(grouped))
		for name := range grouped {
			arr := grouped[name]
			sort.Slice(arr, func(i, j int) bool { return arr[i].Time.ToTime().Before(arr[j].Time.ToTime()) })
			grouped[name] = arr
			l := len(arr)
			total += l
			groupsMeta = append(groupsMeta, struct {
				name   string
				length int
			}{name: name, length: l})
		}
		if maxCount != -1 && total > maxCount {
			targets := allocateTargets(groupsMeta, maxCount)
			total = 0
			for name, k := range targets {
				grouped[name] = downsampleModelRecords(grouped[name], k)
				total += len(grouped[name])
			}
		}
		return struct {
			Count   int                        `json:"count"`
			Records map[string][]models.Record `json:"records"`
			From    models.LocalTime           `json:"from"`
			To      models.LocalTime           `json:"to"`
		}{Count: total, Records: grouped, From: models.FromTime(startTime), To: models.FromTime(endTime)}, nil

	case "ping":
		taskId := params.TaskID
		if taskId == 0 {
			taskId = -1
		}
		recs, err := tasks.GetPingRecords(params.UUID, taskId, startTime, endTime)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping records", err.Error())
		}
		// hidden filter
		if !isAdmin {
			filtered := recs[:0]
			for _, r := range recs {
				if r.Client != "" && hidden[r.Client] {
					continue
				}
				filtered = append(filtered, r)
			}
			recs = filtered
		}

		type RecordsResp struct {
			TaskId uint             `json:"task_id,omitempty"`
			Time   models.LocalTime `json:"time"`
			Value  int              `json:"value"`
			Client string           `json:"client,omitempty"`
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
			Tasks     []map[string]any  `json:"tasks"`
			From      models.LocalTime  `json:"from"`
			To        models.LocalTime  `json:"to"`
		}

		response := &Resp{Count: 0, Records: []RecordsResp{}, From: models.FromTime(startTime), To: models.FromTime(endTime)}

		// stats per client
		clientStats := make(map[string]struct {
			total int
			loss  int
			min   int
			max   int
		})

		for _, r := range recs {
			rr := RecordsResp{
				TaskId: r.TaskId,
				Time:   r.Time,
				Value:  r.Value,
				Client: r.Client,
			}
			st := clientStats[r.Client]
			st.total++
			if r.Value < 0 {
				st.loss++
			} else {
				if st.min == 0 || r.Value < st.min {
					st.min = r.Value
				}
				if r.Value > st.max {
					st.max = r.Value
				}
			}
			clientStats[r.Client] = st
			response.Records = append(response.Records, rr)
		}

		if len(clientStats) > 0 {
			response.BasicInfo = make([]ClientBasicInfo, 0, len(clientStats))
			for client, st := range clientStats {
				if client != "" && !isAdmin && hidden[client] {
					continue
				}
				loss := float64(0)
				if st.total > 0 {
					loss = float64(st.loss) / float64(st.total) * 100
				}
				response.BasicInfo = append(response.BasicInfo, ClientBasicInfo{
					Client: client,
					Loss:   loss,
					Min:    st.min,
					Max:    st.max,
				})
			}
		}

		// tasks summary (always included for ping type; do not expose target field)
		pingTasks, err := tasks.GetAllPingTasks()
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping tasks", err.Error())
		}
		toList := make([]map[string]any, 0, len(pingTasks))
		for _, t := range pingTasks {
			if taskId != -1 && t.Id != uint(taskId) {
				continue
			}
			if params.UUID != "" { // ensure task assigned to specific client when filtering by uuid
				assigned := false
				for _, c := range t.Clients {
					if c == params.UUID {
						assigned = true
						break
					}
				}
				if !assigned {
					continue
				}
			}
			total := 0
			lossCount := 0
			minLat := 0
			maxLat := 0
			sum := 0
			valid := 0
			latestVal := -1
			var latestTs time.Time
			// 收集该任务的所有有效(非丢包)延迟值以计算百分位
			latencies := make([]int, 0, 64)
			for _, r := range recs {
				if r.TaskId != t.Id {
					continue
				}
				if params.UUID != "" && r.Client != params.UUID {
					continue
				}
				total++
				if r.Value < 0 { // 丢包
					lossCount++
					continue
				}
				valid++
				sum += r.Value
				latencies = append(latencies, r.Value)
				if minLat == 0 || r.Value < minLat {
					minLat = r.Value
				}
				if r.Value > maxLat {
					maxLat = r.Value
				}
				// track latest non-negative value
				ts := r.Time.ToTime()
				if latestTs.IsZero() || ts.After(latestTs) {
					latestTs = ts
					latestVal = r.Value
				}
			}

			// 计算 P50 / P99
			p50 := 0
			p99 := 0
			if len(latencies) > 0 {
				sort.Ints(latencies)
				getPercentileInt := func(values []int, percentile float64) int {
					if len(values) == 0 {
						return 0
					}
					if percentile <= 0 {
						return values[0]
					}
					if percentile >= 1 {
						return values[len(values)-1]
					}
					pos := (float64(len(values) - 1)) * percentile
					lo := int(math.Floor(pos))
					hi := int(math.Ceil(pos))
					if lo == hi {
						return values[lo]
					}
					frac := pos - float64(lo)
					v := float64(values[lo]) + (float64(values[hi])-float64(values[lo]))*frac
					return int(math.Round(v))
				}
				p50 = getPercentileInt(latencies, 0.50)
				p99 = getPercentileInt(latencies, 0.99)
			}
			ratio := 0.0
			if p50 > 0 && p99 >= p50 {
				jitterMs := float64(p99 - p50)
				adjustedBase := math.Max(math.Min(float64(p50), 50.0), 10.0)
				ratio = jitterMs / adjustedBase
			}
			lossRate := 0.0
			if total > 0 {
				lossRate = float64(lossCount) / float64(total) * 100
			}
			avg := 0
			if valid > 0 {
				avg = sum / valid
			}
			info := map[string]any{
				"id":            t.Id,
				"name":          t.Name,
				"type":          t.Type,
				"interval":      t.Interval,
				"loss":          lossRate,
				"min":           minLat,
				"max":           maxLat,
				"avg":           avg,
				"latest":        latestVal,
				"total":         total,
				"p50":           p50,
				"p99":           p99,
				"p99_p50_ratio": ratio,
			}
			if params.UUID == "" && taskId != -1 { // retain existing behavior of exposing clients only when filtering by task
				info["clients"] = t.Clients
			}
			toList = append(toList, info)
		}
		response.Tasks = toList
		// apply maxCount for ping
		maxCount := params.MaxCount
		if maxCount == 0 {
			maxCount = 4000
		}
		if maxCount != -1 && len(response.Records) > maxCount {
			// group records by TaskId for proportional downsampling
			taskGroups := make(map[uint][]RecordsResp)
			for _, r := range response.Records {
				taskGroups[r.TaskId] = append(taskGroups[r.TaskId], r)
			}

			// sort each group by time
			for taskId := range taskGroups {
				sort.Slice(taskGroups[taskId], func(i, j int) bool {
					return taskGroups[taskId][i].Time.ToTime().Before(taskGroups[taskId][j].Time.ToTime())
				})
			}

			// calculate proportional allocation for each task
			type taskMeta struct {
				taskId uint
				length int
			}
			groupsMeta := make([]taskMeta, 0, len(taskGroups))
			for taskId, records := range taskGroups {
				groupsMeta = append(groupsMeta, taskMeta{
					taskId: taskId,
					length: len(records),
				})
			}

			// use existing allocateTargets function (create compatible struct)
			strTargets := allocateTargets(
				func() []struct {
					name   string
					length int
				} {
					result := make([]struct {
						name   string
						length int
					}, len(groupsMeta))
					for i, meta := range groupsMeta {
						result[i] = struct {
							name   string
							length int
						}{name: fmt.Sprintf("%d", meta.taskId), length: meta.length}
					}
					return result
				}(),
				maxCount,
			)

			// downsample each task group
			downsampledRecords := make([]RecordsResp, 0, maxCount)
			samplePingRecords := func(in []RecordsResp, k int) []RecordsResp {
				n := len(in)
				if k <= 0 || n == 0 {
					return []RecordsResp{}
				}
				if k >= n {
					return in
				}
				out := make([]RecordsResp, 0, k)
				if k == 1 {
					out = append(out, in[n-1])
					return out
				}
				for i := 0; i < k; i++ {
					idx := int(math.Round(float64(i) * float64(n-1) / float64(k-1)))
					if idx < 0 {
						idx = 0
					} else if idx >= n {
						idx = n - 1
					}
					out = append(out, in[idx])
				}
				return out
			}

			for taskId, records := range taskGroups {
				targetKey := fmt.Sprintf("%d", taskId)
				targetCount := strTargets[targetKey]
				sampled := samplePingRecords(records, targetCount)
				downsampledRecords = append(downsampledRecords, sampled...)
			}

			response.Records = downsampledRecords
		}
		response.Count = len(response.Records)
		// sort by time asc
		sort.Slice(response.Records, func(i, j int) bool {
			return response.Records[i].Time.ToTime().Before(response.Records[j].Time.ToTime())
		})
		return response, nil
	default:
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid type, expected 'load' or 'ping'", params.Type)
	}
}

// ---------- helpers for load records ----------

// getLoadRecordsCombined fetches records for a client or all clients within a time range,
// combining recent short-term table and long-term table with 15-min grouping for recent part.
func getLoadRecordsCombined(uuid string, start, end time.Time) ([]models.Record, error) {
	// prefer the existing function when uuid provided
	if uuid != "" {
		return recordsdb.GetRecordsByClientAndTime(uuid, start, end)
	}
	db := dbcore.GetDBInstance()
	fourHoursAgo := time.Now().Add(-4*time.Hour - time.Minute)

	var recent []models.Record
	recentStart := start
	if end.After(fourHoursAgo) {
		if recentStart.Before(fourHoursAgo) {
			recentStart = fourHoursAgo
		}
		_ = db.Table("records").Where("time >= ? AND time <= ?", recentStart, end).Order("time ASC").Find(&recent).Error
	}

	var longTerm []models.Record
	_ = db.Table("records_long_term").Where("time >= ? AND time <= ?", start, end).Order("time ASC").Find(&longTerm).Error

	// if no long term, return all recent
	if len(longTerm) == 0 {
		return recent, nil
	}

	// group recent by client+15min, keep latest in bucket
	type key struct {
		c    string
		slot string
	}
	grouped := make(map[key]models.Record)
	for _, rec := range recent {
		k := key{c: rec.Client, slot: rec.Time.ToTime().Truncate(15 * time.Minute).Format(time.RFC3339)}
		if old, ok := grouped[k]; !ok || rec.Time.ToTime().After(old.Time.ToTime()) {
			grouped[k] = rec
		}
	}
	flat := make([]models.Record, 0, len(grouped))
	for _, rec := range grouped {
		flat = append(flat, rec)
	}
	sort.Slice(flat, func(i, j int) bool { return flat[i].Time.ToTime().Before(flat[j].Time.ToTime()) })
	flat = append(flat, longTerm...)
	return flat, nil
}

// ---------- downsampling helpers ----------

// allocateTargets splits maxTotal across groups proportionally to their lengths.
func allocateTargets(groups []struct {
	name   string
	length int
}, maxTotal int) map[string]int {
	result := make(map[string]int, len(groups))
	if maxTotal < 0 {
		for _, g := range groups {
			result[g.name] = g.length
		}
		return result
	}
	total := 0
	for _, g := range groups {
		total += g.length
	}
	if total <= maxTotal {
		for _, g := range groups {
			result[g.name] = g.length
		}
		return result
	}
	// initial allocation based on proportion
	type rem struct {
		idx  int
		frac float64
	}
	remainders := make([]rem, 0, len(groups))
	sumTargets := 0
	for i, g := range groups {
		if g.length <= 0 {
			result[g.name] = 0
			continue
		}
		raw := float64(g.length) * float64(maxTotal) / float64(total)
		t := int(math.Floor(raw))
		if t > g.length {
			t = g.length
		}
		result[groups[i].name] = t
		sumTargets += t
		remainders = append(remainders, rem{i, raw - float64(t)})
	}
	// distribute remaining by largest fractional parts
	if sumTargets < maxTotal {
		need := maxTotal - sumTargets
		sort.Slice(remainders, func(i, j int) bool { return remainders[i].frac > remainders[j].frac })
		for _, r := range remainders {
			if need == 0 {
				break
			}
			g := groups[r.idx]
			cur := result[g.name]
			if cur < g.length {
				result[g.name] = cur + 1
				need--
			}
		}
		// if still left, second pass round-robin
		if need > 0 {
			for need > 0 {
				for _, g := range groups {
					if need == 0 {
						break
					}
					if result[g.name] < g.length {
						result[g.name]++
						need--
					}
				}
				if need > 0 {
					break
				}
			}
		}
	} else if sumTargets > maxTotal {
		over := sumTargets - maxTotal
		sort.Slice(remainders, func(i, j int) bool { return remainders[i].frac < remainders[j].frac })
		for _, r := range remainders {
			if over == 0 {
				break
			}
			g := groups[r.idx]
			if result[g.name] > 0 {
				result[g.name]--
				over--
			}
		}
		if over > 0 {
			for over > 0 {
				for _, g := range groups {
					if over == 0 {
						break
					}
					if result[g.name] > 0 {
						result[g.name]--
						over--
					}
				}
				if over > 0 {
					break
				}
			}
		}
	}
	return result
}

func downsampleModelRecords(in []models.Record, k int) []models.Record {
	n := len(in)
	if k <= 0 || n == 0 {
		return []models.Record{}
	}
	if k >= n {
		return in
	}
	out := make([]models.Record, 0, k)
	if k == 1 {
		out = append(out, in[n-1])
		return out
	}
	for i := 0; i < k; i++ {
		idx := int(math.Round(float64(i) * float64(n-1) / float64(k-1)))
		if idx < 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		out = append(out, in[idx])
	}
	return out
}

func downsampleFlatRecords(in []flatRecord, k int) []flatRecord {
	n := len(in)
	if k <= 0 || n == 0 {
		return []flatRecord{}
	}
	if k >= n {
		return in
	}
	out := make([]flatRecord, 0, k)
	if k == 1 {
		out = append(out, in[n-1])
		return out
	}
	for i := 0; i < k; i++ {
		idx := int(math.Round(float64(i) * float64(n-1) / float64(k-1)))
		if idx < 0 {
			idx = 0
		} else if idx >= n {
			idx = n - 1
		}
		out = append(out, in[idx])
	}
	return out
}

// flatRecord is a projection used when load_type is specified.
type flatRecord struct {
	Client         string           `json:"client"`
	Time           models.LocalTime `json:"time"`
	Cpu            *float32         `json:"cpu,omitempty"`
	Gpu            *float32         `json:"gpu,omitempty"`
	Ram            *int64           `json:"ram,omitempty"`
	RamTotal       *int64           `json:"ram_total,omitempty"`
	Swap           *int64           `json:"swap,omitempty"`
	SwapTotal      *int64           `json:"swap_total,omitempty"`
	Load           *float32         `json:"load,omitempty"`
	Temp           *float32         `json:"temp,omitempty"`
	Disk           *int64           `json:"disk,omitempty"`
	DiskTotal      *int64           `json:"disk_total,omitempty"`
	NetIn          *int64           `json:"net_in,omitempty"`
	NetOut         *int64           `json:"net_out,omitempty"`
	NetTotalUp     *int64           `json:"net_total_up,omitempty"`
	NetTotalDown   *int64           `json:"net_total_down,omitempty"`
	Process        *int             `json:"process,omitempty"`
	Connections    *int             `json:"connections,omitempty"`
	ConnectionsUdp *int             `json:"connections_udp,omitempty"`
	Uptime         *int64           `json:"uptime,omitempty"`
}

func filterRecordsByLoadType(recs []models.Record, loadType string) []flatRecord {
	out := make([]flatRecord, 0, len(recs))
	for _, r := range recs {
		fr := flatRecord{Client: r.Client, Time: r.Time}
		// always include uptime when present
		//if r.Uptime != 0 {
		//	v := r.Uptime
		//	fr.Uptime = &v
		//}
		switch loadType {
		case "cpu":
			v := r.Cpu
			fr.Cpu = &v
		case "gpu":
			v := r.Gpu
			fr.Gpu = &v
		case "ram":
			v := r.Ram
			fr.Ram = &v
			vt := r.RamTotal
			fr.RamTotal = &vt
		case "swap":
			v := r.Swap
			fr.Swap = &v
			vt := r.SwapTotal
			fr.SwapTotal = &vt
		case "load":
			v := r.Load
			fr.Load = &v
		case "temp":
			v := r.Temp
			fr.Temp = &v
		case "disk":
			v := r.Disk
			fr.Disk = &v
			vt := r.DiskTotal
			fr.DiskTotal = &vt
		case "network":
			vi := r.NetIn
			vo := r.NetOut
			vtu := r.NetTotalUp
			vtd := r.NetTotalDown
			fr.NetIn = &vi
			fr.NetOut = &vo
			fr.NetTotalUp = &vtu
			fr.NetTotalDown = &vtd
		case "process":
			v := r.Process
			fr.Process = &v
		case "connections":
			v := r.Connections
			fr.Connections = &v
			vu := r.ConnectionsUdp
			fr.ConnectionsUdp = &vu
		default:
			// unknown type: fallback to all fields as a full record would be returned elsewhere
			v := r.Cpu
			fr.Cpu = &v
		}
		out = append(out, fr)
	}
	return out
}
