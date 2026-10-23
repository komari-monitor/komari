package jsonrpc

import (
	"context"
	"sort"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	recordsdb "github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/internal/sampling"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	Register("getRecords", getRecords)
}

func getRecords(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	meta := rpc.MetaFromContext(ctx)
	var params struct {
		Type     string     `json:"type"`      // "load" | "ping"; default "load"
		UUID     string     `json:"uuid"`      // client uuid; empty = all clients
		Hours    int        `json:"hours"`     // time window in hours; default 1 if start/end not provided
		Start    *time.Time `json:"start"`     // RFC3339 with an explicit timezone (optional)
		End      *time.Time `json:"end"`       // RFC3339 with an explicit timezone (optional)
		LoadType string     `json:"load_type"` // for type=load: cpu|gpu|ram|swap|load|temp|disk|network|process|connections|all
		TaskID   int        `json:"task_id"`   // for type=ping: optional task id; -1 or omitted means all
		MaxCount int        `json:"maxCount"`  // max number of points; -1 unlimited; default 4000
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	// defaults
	if params.Type == "" {
		params.Type = "load"
	}
	// parse time window
	var startTime, endTime time.Time
	if params.Start != nil || params.End != nil {
		// allow partial: missing end means now
		if params.End == nil {
			endTime = time.Now().UTC()
		} else {
			endTime = params.End.UTC()
		}
		if params.Start == nil {
			// default to 1 hour before end
			startTime = endTime.Add(-1 * time.Hour)
		} else {
			startTime = params.Start.UTC()
		}
	} else {
		hours := params.Hours
		if hours <= 0 {
			hours = 1 // default 1 hour
		}
		endTime = time.Now().UTC()
		startTime = endTime.Add(-time.Duration(hours) * time.Hour)
	}

	// Hidden filtering for non-admin
	isAdmin := meta.Principal != nil && meta.Principal.HasRole(rpc.RoleAdmin)
	hidden := map[string]bool{}
	if !isAdmin {
		cinfo, err := clients.GetAllClientBasicInfo()
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
		// resolve maxCount before querying so the store can choose a coarser
		// interval instead of materializing points that will be sampled away.
		maxCount := params.MaxCount
		if maxCount == 0 {
			maxCount = 4000
		}
		// Fetch only the metrics backing load_type. Unknown types keep the
		// compatible full-query path; the later filter still zeroes other fields.
		opts := recordsdb.RecordQueryOptions{
			MetricNames: recordsdb.LoadTypeMetricNames(params.LoadType),
			MaxPoints:   maxCount,
		}
		recs, err := recordsdb.QueryRecords(ctx, params.UUID, startTime, endTime, opts)
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

		// optional load_type filtering -> group by client
		if params.LoadType != "" && params.LoadType != "all" {
			items := filterRecordsByLoadType(recs, params.LoadType)
			grouped := make(map[string][]flatRecord)
			for _, it := range items {
				grouped[it.Client] = append(grouped[it.Client], it)
			}
			// sort and count
			total := 0
			groupsMeta := make([]allocationGroup[string], 0, len(grouped))
			for name := range grouped {
				arr := grouped[name]
				sort.Slice(arr, func(i, j int) bool { return arr[i].Time.Before(arr[j].Time) })
				grouped[name] = arr
				l := len(arr)
				total += l
				groupsMeta = append(groupsMeta, allocationGroup[string]{key: name, length: l})
			}
			// downsample across all clients proportionally
			if maxCount != -1 && total > maxCount {
				targets := allocateTargets(groupsMeta, maxCount)
				total = 0
				for name, k := range targets {
					grouped[name] = sampleEvenly(grouped[name], k)
					total += len(grouped[name])
				}
			}
			return struct {
				Count    int                     `json:"count"`
				Records  map[string][]flatRecord `json:"records"`
				LoadType string                  `json:"load_type"`
				From     time.Time               `json:"from"`
				To       time.Time               `json:"to"`
			}{Count: total, Records: grouped, LoadType: params.LoadType, From: startTime.UTC(), To: endTime.UTC()}, nil
		}
		// default: return full records, grouped by client
		grouped := make(map[string][]models.Record)
		for _, r := range recs {
			grouped[r.Client] = append(grouped[r.Client], r)
		}
		total := 0
		groupsMeta := make([]allocationGroup[string], 0, len(grouped))
		for name := range grouped {
			arr := grouped[name]
			sort.Slice(arr, func(i, j int) bool { return arr[i].Time.Before(arr[j].Time) })
			grouped[name] = arr
			l := len(arr)
			total += l
			groupsMeta = append(groupsMeta, allocationGroup[string]{key: name, length: l})
		}
		if maxCount != -1 && total > maxCount {
			targets := allocateTargets(groupsMeta, maxCount)
			total = 0
			for name, k := range targets {
				grouped[name] = sampleEvenly(grouped[name], k)
				total += len(grouped[name])
			}
		}
		return struct {
			Count   int                        `json:"count"`
			Records map[string][]models.Record `json:"records"`
			From    time.Time                  `json:"from"`
			To      time.Time                  `json:"to"`
		}{Count: total, Records: grouped, From: startTime.UTC(), To: endTime.UTC()}, nil

	case "ping":
		taskId := params.TaskID
		if taskId == 0 {
			taskId = -1
		}
		recs, err := metricstore.GetPingRecords(ctx, params.UUID, taskId, startTime, endTime)
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
			TaskId uint      `json:"task_id,omitempty"`
			Time   time.Time `json:"time"`
			Value  int       `json:"value"`
			Client string    `json:"client,omitempty"`
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
			From      time.Time         `json:"from"`
			To        time.Time         `json:"to"`
		}

		response := &Resp{Count: 0, Records: []RecordsResp{}, From: startTime.UTC(), To: endTime.UTC()}

		maxCount := params.MaxCount
		if maxCount == 0 {
			maxCount = 4000
		}
		displayRecs := recs
		if maxCount != -1 && len(recs) > maxCount {
			displayRecs = tasks.SamplePingRecordsByTask(recs, maxCount)
		}
		for _, r := range displayRecs {
			response.Records = append(response.Records, RecordsResp{
				TaskId: r.TaskId,
				Time:   r.Time,
				Value:  r.Value,
				Client: r.Client,
			})
		}

		summary := tasks.SummarizePingRecords(recs)
		if len(summary.Clients) > 0 {
			response.BasicInfo = make([]ClientBasicInfo, 0, len(summary.Clients))
			for client, st := range summary.Clients {
				response.BasicInfo = append(response.BasicInfo, ClientBasicInfo{
					Client: client,
					Loss:   st.LossPercent,
					Min:    st.Min,
					Max:    st.Max,
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
				if !t.AppliesToClient(params.UUID) {
					continue
				}
			}
			st := summary.Tasks[t.Id]
			latest := st.Latest
			if st.Total == 0 {
				latest = -1
			}
			info := map[string]any{
				"id":            t.Id,
				"name":          t.Name,
				"type":          t.Type,
				"interval":      t.Interval,
				"default_on":    t.DefaultOn,
				"loss":          st.LossPercent,
				"min":           st.Min,
				"max":           st.Max,
				"avg":           st.Avg,
				"latest":        latest,
				"total":         st.Total,
				"p50":           st.P50,
				"p99":           st.P99,
				"p99_p50_ratio": st.P99P50Ratio,
			}
			if params.UUID == "" && taskId != -1 { // retain existing behavior of exposing clients only when filtering by task
				info["clients"] = t.Clients
			}
			toList = append(toList, info)
		}
		response.Tasks = toList
		response.Count = len(response.Records)
		// sort by time asc
		sort.Slice(response.Records, func(i, j int) bool {
			return response.Records[i].Time.Before(response.Records[j].Time)
		})
		return response, nil
	default:
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid type, expected 'load' or 'ping'", params.Type)
	}
}

// ---------- downsampling helpers ----------

type allocationGroup[K comparable] struct {
	key    K
	length int
}

// allocateTargets splits maxTotal across groups proportionally to their lengths.
func allocateTargets[K comparable](groups []allocationGroup[K], maxTotal int) map[K]int {
	converted := make([]sampling.Group[K], 0, len(groups))
	for _, g := range groups {
		converted = append(converted, sampling.Group[K]{Key: g.key, Length: g.length})
	}
	return sampling.AllocateTargets(converted, maxTotal)
}

func sampleEvenly[T any](in []T, k int) []T {
	return sampling.SampleEvenly(in, k)
}

// flatRecord is the legacy load_type response shape. The selected fields come
// from records.ProjectLoad so public/admin adapters share one switch.
type flatRecord struct {
	Client string    `json:"client"`
	Time   time.Time `json:"time"`
	recordsdb.LoadProjection
}

func filterRecordsByLoadType(recs []models.Record, loadType string) []flatRecord {
	out := make([]flatRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, flatRecord{
			Client:         r.Client,
			Time:           r.Time,
			LoadProjection: recordsdb.ProjectLoad(r, loadType),
		})
	}
	return out
}
