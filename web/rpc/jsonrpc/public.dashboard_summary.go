package jsonrpc

import (
	"context"
	"time"

	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func init() {
	regPublic("getDashboardSummary", publicGetDashboardSummary, "Dashboard overview summaries without full series")
}

type publicDashboardSummaryParams struct {
	Hours     float64    `json:"hours"`
	Start     *time.Time `json:"start"`
	StartTime *time.Time `json:"start_time"`
	End       *time.Time `json:"end"`
	EndTime   *time.Time `json:"end_time"`
}

type publicDashboardTrafficPoint struct {
	Time     int64   `json:"time"`
	UpRate   float64 `json:"upRate"`
	DownRate float64 `json:"downRate"`
	UpCum    float64 `json:"upCum"`
	DownCum  float64 `json:"downCum"`
}

type publicDashboardTrafficNodeTotal struct {
	UUID     string  `json:"uuid"`
	Up       float64 `json:"up"`
	Down     float64 `json:"down"`
	Total    float64 `json:"total"`
	PeakRate float64 `json:"peakRate"`
	PeakTime int64   `json:"peakTime"`
}

type publicDashboardTrafficSummary struct {
	Points     []publicDashboardTrafficPoint     `json:"points"`
	NodeTotals []publicDashboardTrafficNodeTotal `json:"nodeTotals"`
	TotalUp    float64                           `json:"totalUp"`
	TotalDown  float64                           `json:"totalDown"`
}

type publicDashboardRankItem struct {
	UUID     string  `json:"uuid"`
	Value    float64 `json:"value"`
	Peak     float64 `json:"peak"`
	PeakTime int64   `json:"peakTime"`
}

type publicDashboardSummaryResponse struct {
	Start   time.Time                     `json:"start"`
	End     time.Time                     `json:"end"`
	Traffic publicDashboardTrafficSummary `json:"traffic"`
	TopCPU  []publicDashboardRankItem     `json:"top_cpu"`
	TopMem  []publicDashboardRankItem     `json:"top_mem"`
}

// publicGetDashboardSummary returns only the aggregates the admin dashboard
// paints on first paint. Full timeseries stay behind queryMetrics and are
// fetched per entity when a chart button is opened.
func publicGetDashboardSummary(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicDashboardSummaryParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	queryNow := time.Now().UTC()
	end := metricQueryTimeOrDefault(firstMetricQueryTime(params.End, params.EndTime), queryNow)
	startFallback := end.Add(-metricQueryHours(params.Hours))
	if params.Hours <= 0 && params.Start == nil && params.StartTime == nil {
		startFallback = end.Add(-24 * time.Hour)
	}
	start := metricQueryTimeOrDefault(firstMetricQueryTime(params.Start, params.StartTime), startFallback)
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	entityIDs, rpcErr := publicMetricEntityIDs(ctx, nil)
	if rpcErr != nil {
		return nil, rpcErr
	}
	empty := publicDashboardSummaryResponse{
		Start:   start.UTC(),
		End:     end.UTC(),
		Traffic: publicDashboardTrafficSummary{Points: []publicDashboardTrafficPoint{}, NodeTotals: []publicDashboardTrafficNodeTotal{}},
		TopCPU:  []publicDashboardRankItem{},
		TopMem:  []publicDashboardRankItem{},
	}
	if len(entityIDs) == 0 {
		return empty, nil
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	overview, err := store.DashboardOverview(ctx, entityIDs, start, end)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query dashboard metrics: "+err.Error(), nil)
	}

	trafficPoints := make([]publicDashboardTrafficPoint, len(overview.TrafficPoints))
	for i, point := range overview.TrafficPoints {
		trafficPoints[i] = publicDashboardTrafficPoint{
			Time: point.Time, UpRate: point.UpRate, DownRate: point.DownRate,
			UpCum: point.UpCum, DownCum: point.DownCum,
		}
	}
	nodeTotals := make([]publicDashboardTrafficNodeTotal, len(overview.NodeTotals))
	for i, node := range overview.NodeTotals {
		nodeTotals[i] = publicDashboardTrafficNodeTotal{
			UUID: node.UUID, Up: node.Up, Down: node.Down, Total: node.Total,
			PeakRate: node.PeakRate, PeakTime: node.PeakTime,
		}
	}
	topCPU := make([]publicDashboardRankItem, len(overview.TopCPU))
	for i, item := range overview.TopCPU {
		topCPU[i] = publicDashboardRankItem{UUID: item.UUID, Value: item.Value, Peak: item.Peak, PeakTime: item.PeakTime}
	}
	topMem := make([]publicDashboardRankItem, len(overview.TopMem))
	for i, item := range overview.TopMem {
		topMem[i] = publicDashboardRankItem{UUID: item.UUID, Value: item.Value, Peak: item.Peak, PeakTime: item.PeakTime}
	}

	return publicDashboardSummaryResponse{
		Start: start.UTC(),
		End:   end.UTC(),
		Traffic: publicDashboardTrafficSummary{
			Points:     trafficPoints,
			NodeTotals: nodeTotals,
			TotalUp:    overview.TotalUp,
			TotalDown:  overview.TotalDown,
		},
		TopCPU: topCPU,
		TopMem: topMem,
	}, nil
}
