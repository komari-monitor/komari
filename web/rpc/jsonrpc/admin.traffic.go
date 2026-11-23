package jsonrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/internal/trafficledger"
	"github.com/komari-monitor/komari/pkg/rpc"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils/notifier"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

func init() {
	RegisterWithGroupAndMeta("getClientTraffic", rpc.RoleAdmin, adminGetClientTraffic, &rpc.MethodMeta{
		Name:    "admin:getClientTraffic",
		Summary: "Get a client's traffic cycle and usage",
		Params:  []rpc.ParamMeta{{Name: "uuid", Type: "string", Required: true}},
		Returns: "Traffic cycle configuration, current usage, and quota",
	})
	RegisterWithGroupAndMeta("updateClientTrafficConfig", rpc.RoleAdmin, adminUpdateClientTrafficConfig, &rpc.MethodMeta{
		Name:    "admin:updateClientTrafficConfig",
		Summary: "Update a client's traffic cycle and quota",
		Params:  []rpc.ParamMeta{{Name: "uuid", Type: "string", Required: true}},
		Returns: "null",
	})
	RegisterWithGroupAndMeta("calibrateClientTraffic", rpc.RoleAdmin, adminCalibrateClientTraffic, &rpc.MethodMeta{
		Name:    "admin:calibrateClientTraffic",
		Summary: "Calibrate a client's current traffic cycle",
		Params:  []rpc.ParamMeta{{Name: "uuid", Type: "string", Required: true}},
		Returns: "Calibration before and after values",
	})
	RegisterWithGroupAndMeta("resetClientTrafficCycle", rpc.RoleAdmin, adminResetClientTrafficCycle, &rpc.MethodMeta{
		Name:    "admin:resetClientTrafficCycle",
		Summary: "Reset a client's current traffic cycle",
		Params:  []rpc.ParamMeta{{Name: "uuid", Type: "string", Required: true}},
		Returns: "null",
	})
}

type clientTrafficResponse struct {
	UUID                   string     `json:"uuid"`
	Mode                   string     `json:"mode"`
	ResetDay               int        `json:"reset_day"`
	IntervalDays           int        `json:"interval_days"`
	AnchorAt               *time.Time `json:"anchor_at,omitempty"`
	AnchorDate             string     `json:"anchor_date,omitempty"`
	Timezone               string     `json:"timezone"`
	CycleStartAt           *time.Time `json:"cycle_start_at,omitempty"`
	CycleEndAt             *time.Time `json:"cycle_end_at,omitempty"`
	AccumulatedUp          int64      `json:"accumulated_up"`
	AccumulatedDown        int64      `json:"accumulated_down"`
	OffsetUp               int64      `json:"offset_up"`
	OffsetDown             int64      `json:"offset_down"`
	EffectiveUp            int64      `json:"effective_up"`
	EffectiveDown          int64      `json:"effective_down"`
	RawLastUp              int64      `json:"raw_last_up"`
	RawLastDown            int64      `json:"raw_last_down"`
	HasBaseline            bool       `json:"has_baseline"`
	CalibratedAt           *time.Time `json:"calibrated_at,omitempty"`
	TrafficLimit           int64      `json:"traffic_limit"`
	TrafficLimitType       string     `json:"traffic_limit_type"`
	AgentMonthRotate       *bool      `json:"agent_month_rotate,omitempty"`
	AgentConfigCheckFailed bool       `json:"agent_config_check_failed,omitempty"`
}

func adminGetClientTraffic(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID             string `json:"uuid"`
		CheckAgentConfig string `json:"check_agent_config"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	client, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.NotFound, "Client not found", nil)
	}

	latest := latestReportPtr(params.UUID)
	var liveUp, liveDown int64
	if latest != nil {
		liveUp, liveDown = max(latest.Network.TotalUp, 0), max(latest.Network.TotalDown, 0)
	}
	display := trafficledger.ResolveDisplay(params.UUID, liveUp, liveDown)

	resp := clientTrafficResponse{
		UUID:             params.UUID,
		Mode:             trafficledger.ModeAgent,
		ResetDay:         1,
		IntervalDays:     30,
		EffectiveUp:      display.Up,
		EffectiveDown:    display.Down,
		RawLastUp:        liveUp,
		RawLastDown:      liveDown,
		TrafficLimit:     client.TrafficLimit,
		TrafficLimitType: client.TrafficLimitType,
	}

	if snap, ok := trafficledger.Get(params.UUID); ok {
		resp.Mode = snap.Mode
		resp.ResetDay = snap.ResetDay
		resp.IntervalDays = snap.IntervalDays
		resp.AnchorAt = snap.AnchorAt
		resp.Timezone = snap.Timezone
		resp.CycleStartAt = snap.CycleStartAt
		resp.CycleEndAt = snap.CycleEndAt
		resp.AccumulatedUp = snap.AccumulatedUp
		resp.AccumulatedDown = snap.AccumulatedDown
		resp.OffsetUp = snap.OffsetUp
		resp.OffsetDown = snap.OffsetDown
		resp.HasBaseline = snap.HasBaseline
		resp.CalibratedAt = snap.CalibratedAt
		if snap.Mode != trafficledger.ModeAgent && snap.HasBaseline {
			resp.RawLastUp = snap.RawLastUp
			resp.RawLastDown = snap.RawLastDown
		}
		if snap.AnchorAt != nil {
			if loc, locErr := trafficledger.ResolveLocation(snap.Timezone, trafficledger.DefaultTimezone()); locErr == nil {
				resp.AnchorDate = snap.AnchorAt.In(loc).Format("2006-01-02")
			} else {
				resp.AnchorDate = snap.AnchorAt.UTC().Format("2006-01-02")
			}
		}
	}

	if params.CheckAgentConfig != "" {
		check, parseErr := strconv.ParseBool(params.CheckAgentConfig)
		if parseErr != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "check_agent_config must be a boolean", nil)
		}
		if check {
			startup, startupErr := agent_runtime.GetStartupConfig(ctx, params.UUID)
			if startupErr == nil {
				enabled := monthRotateEnabled(startup["month_rotate"])
				resp.AgentMonthRotate = &enabled
			} else {
				disabled := false
				resp.AgentMonthRotate = &disabled
				resp.AgentConfigCheckFailed = true
			}
		}
	}
	return resp, nil
}

func monthRotateEnabled(value any) bool {
	switch v := value.(type) {
	case float64:
		return v > 0
	case int:
		return v > 0
	case int64:
		return v > 0
	case json.Number:
		n, err := v.Int64()
		return err == nil && n > 0
	case string:
		n, err := strconv.Atoi(v)
		return err == nil && n > 0
	default:
		return false
	}
}

func adminUpdateClientTrafficConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID             string     `json:"uuid"`
		Mode             string     `json:"mode"`
		ResetDay         int        `json:"reset_day"`
		IntervalDays     int        `json:"interval_days"`
		AnchorAt         *time.Time `json:"anchor_at"`
		AnchorDate       string     `json:"anchor_date"`
		Timezone         string     `json:"timezone"`
		TrafficLimit     *int64     `json:"traffic_limit"`
		TrafficLimitType *string    `json:"traffic_limit_type"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid and traffic cycle configuration are required", nil)
	}
	params.Mode = strings.ToLower(strings.TrimSpace(params.Mode))
	params.Timezone = strings.TrimSpace(params.Timezone)

	client, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.NotFound, "Client not found", nil)
	}

	anchorAt := params.AnchorAt
	if dateStr := strings.TrimSpace(params.AnchorDate); dateStr != "" {
		parsed, parseErr := time.Parse("2006-01-02", dateStr)
		if parseErr != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "anchor_date must be YYYY-MM-DD", nil)
		}
		loc, locErr := trafficledger.ResolveLocation(params.Timezone, trafficledger.DefaultTimezone())
		if locErr != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, locErr.Error(), nil)
		}
		anchored := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc).UTC()
		anchorAt = &anchored
	}

	before, _ := trafficledger.Get(params.UUID)
	update := trafficledger.UpdateConfigRequest{
		CycleConfig: trafficledger.CycleConfig{
			Mode:         params.Mode,
			ResetDay:     params.ResetDay,
			IntervalDays: params.IntervalDays,
			AnchorAt:     anchorAt,
			Timezone:     params.Timezone,
		},
		TrafficLimit:     params.TrafficLimit,
		TrafficLimitType: params.TrafficLimitType,
	}
	if err := trafficledger.UpdateConfig(ctx, params.UUID, update, latestReportPtr(params.UUID)); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}

	if before.Mode != params.Mode {
		notifier.ResetTrafficAlertState(params.UUID)
	}

	actor, ip := auditActor(ctx)
	details := []string{fmt.Sprintf("mode=%s", params.Mode)}
	if params.Mode == trafficledger.ModeMonthly {
		details = append(details, fmt.Sprintf("reset_day=%d", params.ResetDay))
	} else if params.Mode == trafficledger.ModeFixedDays {
		details = append(details, fmt.Sprintf("interval_days=%d", params.IntervalDays))
	}
	if params.Timezone != "" {
		details = append(details, "tz="+params.Timezone)
	}
	if params.TrafficLimit != nil {
		details = append(details, fmt.Sprintf("limit=%s", formatTrafficBytes(*params.TrafficLimit)))
	}
	if params.TrafficLimitType != nil {
		details = append(details, "type="+strings.ToLower(strings.TrimSpace(*params.TrafficLimitType)))
	}
	auditlog.Log(ip, actor, fmt.Sprintf("Update client traffic config: %s (%s)",
		auditlog.FormatClient(client.Name, params.UUID), strings.Join(details, ", ")), "info")
	return nil, nil
}

func latestReportPtr(uuid string) *v2.Report {
	return agent_runtime.GetLatestReport()[uuid]
}

func adminCalibrateClientTraffic(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID          string `json:"uuid"`
		TargetUp      *int64 `json:"target_up"`
		TargetDown    *int64 `json:"target_down"`
		TargetTotal   *int64 `json:"target_total"`
		DeltaUp       *int64 `json:"delta_up"`
		DeltaDown     *int64 `json:"delta_down"`
		SuppressAlert bool   `json:"suppress_alert"`
		Note          string `json:"note"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	client, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.NotFound, "Client not found", nil)
	}

	note := strings.TrimSpace(params.Note)
	if len(note) > 500 {
		note = note[:500]
	}
	calibration := trafficledger.CalibrateRequest{
		TargetUp:      params.TargetUp,
		TargetDown:    params.TargetDown,
		TargetTotal:   params.TargetTotal,
		DeltaUp:       params.DeltaUp,
		DeltaDown:     params.DeltaDown,
		SuppressAlert: params.SuppressAlert,
		Note:          note,
	}
	before, _ := trafficledger.Get(params.UUID)
	result, calErr := trafficledger.Calibrate(ctx, params.UUID, calibration)
	if calErr != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, calErr.Error(), nil)
	}
	after, _ := trafficledger.Get(params.UUID)

	if params.SuppressAlert {
		notifier.SyncTrafficAlertForClient(client, trafficledger.ResolveDisplay(params.UUID, 0, 0))
	}

	actor, ip := auditActor(ctx)
	summary := fmt.Sprintf("Calibrate client traffic: %s (up: %s -> %s, down: %s -> %s)",
		auditlog.FormatClient(client.Name, params.UUID),
		formatTrafficBytes(result.BeforeUp), formatTrafficBytes(result.AfterUp),
		formatTrafficBytes(result.BeforeDown), formatTrafficBytes(result.AfterDown),
	)
	if note != "" {
		summary += ", note=" + note
	}
	auditlog.Log(ip, actor, summary, "warn")
	return map[string]any{"before": before, "after": after, "calibration": result}, nil
}

func adminResetClientTrafficCycle(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
		Note string `json:"note"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	client, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.NotFound, "Client not found", nil)
	}
	before, _ := trafficledger.Get(params.UUID)
	if err := trafficledger.ResetCycle(ctx, params.UUID, time.Now().UTC()); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
	}
	notifier.ResetTrafficAlertState(params.UUID)

	actor, ip := auditActor(ctx)
	summary := fmt.Sprintf("Reset client traffic cycle: %s (cleared up=%s, down=%s)",
		auditlog.FormatClient(client.Name, params.UUID),
		formatTrafficBytes(before.EffectiveUp), formatTrafficBytes(before.EffectiveDown),
	)
	if note := strings.TrimSpace(params.Note); note != "" {
		if len(note) > 500 {
			note = note[:500]
		}
		summary += ", note=" + note
	}
	auditlog.Log(ip, actor, summary, "warn")
	return nil, nil
}

func formatTrafficBytes(b int64) string {
	const unit = 1024
	if b < 0 {
		return "-" + formatTrafficBytes(-b)
	}
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
