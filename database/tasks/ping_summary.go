package tasks

import (
	"math"
	"sort"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/sampling"
)

// PingClientSummary is the per-client aggregate shared by ping RPC adapters.
type PingClientSummary struct {
	Total       int
	Loss        int
	Min         int
	Max         int
	LossPercent float64
}

// PingTaskSummary is the per-task aggregate shared by ping RPC adapters.
type PingTaskSummary struct {
	Total       int
	Loss        int
	Valid       int
	Min         int
	Max         int
	Avg         int
	Latest      int
	P50         int
	P99         int
	LossPercent float64
	P99P50Ratio float64
}

// PingSummary aggregates records once so legacy and public ping handlers only
// differ in response shape.
type PingSummary struct {
	Clients map[string]PingClientSummary
	Tasks   map[uint]PingTaskSummary
}

type pingClientAccumulator struct {
	total, loss, min, max int
}

type pingTaskAccumulator struct {
	total, loss, min, max, sum, valid int
	latest                            int
	latestTime                        time.Time
	latencies                         []int
}

// SummarizePingRecords computes client and task aggregates in one pass.
func SummarizePingRecords(recs []models.PingRecord) PingSummary {
	clients := make(map[string]*pingClientAccumulator)
	taskMap := make(map[uint]*pingTaskAccumulator)
	for _, r := range recs {
		cs := clients[r.Client]
		if cs == nil {
			cs = &pingClientAccumulator{}
			clients[r.Client] = cs
		}
		cs.total++
		ts := taskMap[r.TaskId]
		if ts == nil {
			ts = &pingTaskAccumulator{latest: -1}
			taskMap[r.TaskId] = ts
		}
		ts.total++
		if r.Value < 0 {
			cs.loss++
			ts.loss++
			continue
		}
		if cs.min == 0 || r.Value < cs.min {
			cs.min = r.Value
		}
		if r.Value > cs.max {
			cs.max = r.Value
		}
		ts.valid++
		ts.sum += r.Value
		if ts.min == 0 || r.Value < ts.min {
			ts.min = r.Value
		}
		if r.Value > ts.max {
			ts.max = r.Value
		}
		if ts.latestTime.IsZero() || r.Time.After(ts.latestTime) {
			ts.latestTime = r.Time
			ts.latest = r.Value
		}
		ts.latencies = append(ts.latencies, r.Value)
	}

	out := PingSummary{Clients: make(map[string]PingClientSummary, len(clients)), Tasks: make(map[uint]PingTaskSummary, len(taskMap))}
	for client, v := range clients {
		loss := 0.0
		if v.total > 0 {
			loss = float64(v.loss) / float64(v.total) * 100
		}
		out.Clients[client] = PingClientSummary{Total: v.total, Loss: v.loss, Min: v.min, Max: v.max, LossPercent: loss}
	}
	for id, v := range taskMap {
		loss := 0.0
		if v.total > 0 {
			loss = float64(v.loss) / float64(v.total) * 100
		}
		avg := 0
		if v.valid > 0 {
			avg = v.sum / v.valid
		}
		p50, p99 := pingPercentiles(v.latencies)
		ratio := 0.0
		if p50 > 0 && p99 >= p50 {
			adjustedBase := math.Max(math.Min(float64(p50), 50.0), 10.0)
			ratio = float64(p99-p50) / adjustedBase
		}
		out.Tasks[id] = PingTaskSummary{
			Total: v.total, Loss: v.loss, Valid: v.valid, Min: v.min, Max: v.max,
			Avg: avg, Latest: v.latest, P50: p50, P99: p99, LossPercent: loss, P99P50Ratio: ratio,
		}
	}
	return out
}

// SamplePingRecordsByTask applies the legacy ping maxCount policy: allocate
// points proportionally by task, sample each task evenly and return ascending
// by time. Statistics must be computed from the unsampled records.
func SamplePingRecordsByTask(recs []models.PingRecord, maxCount int) []models.PingRecord {
	if maxCount <= 0 || len(recs) <= maxCount {
		return recs
	}
	groups := make(map[uint][]models.PingRecord)
	for _, r := range recs {
		groups[r.TaskId] = append(groups[r.TaskId], r)
	}
	meta := make([]sampling.Group[uint], 0, len(groups))
	for id, points := range groups {
		sort.Slice(points, func(i, j int) bool { return points[i].Time.Before(points[j].Time) })
		groups[id] = points
		meta = append(meta, sampling.Group[uint]{Key: id, Length: len(points)})
	}
	targets := sampling.AllocateTargets(meta, maxCount)
	out := make([]models.PingRecord, 0, maxCount)
	for id, points := range groups {
		out = append(out, sampling.SampleEvenly(points, targets[id])...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func pingPercentiles(values []int) (int, int) {
	if len(values) == 0 {
		return 0, 0
	}
	sort.Ints(values)
	return pingPercentile(values, 0.50), pingPercentile(values, 0.99)
}

func pingPercentile(values []int, percentile float64) int {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		return values[0]
	}
	if percentile >= 1 {
		return values[len(values)-1]
	}
	pos := float64(len(values)-1) * percentile
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return values[lo]
	}
	frac := pos - float64(lo)
	return int(math.Round(float64(values[lo]) + (float64(values[hi])-float64(values[lo]))*frac))
}
