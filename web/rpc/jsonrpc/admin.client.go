package jsonrpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/rpc"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

// admin.client.go
// client 资源的 RPC2 方法（admin 命名空间）。承载原 web/api/admin/client.go 的业务逻辑，
// 包含审计日志与运行时副作用。传统 REST handler 经 CallFromGin 转调这些方法。

func init() {
	RegisterWithGroupAndMeta("addClient", rpc.RoleAdmin, adminAddClient, &rpc.MethodMeta{
		Name:    "admin:addClient",
		Summary: "Create a new client",
		Params: []rpc.ParamMeta{
			{Name: "name", Type: "string", Required: false, Description: "Optional client name"},
		},
		Returns: "{ uuid: string, token: string }",
	})
	RegisterWithGroupAndMeta("editClient", rpc.RoleAdmin, adminEditClient, &rpc.MethodMeta{
		Name:    "admin:editClient",
		Summary: "Edit a client (partial update)",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "null",
	})
	RegisterWithGroupAndMeta("removeClient", rpc.RoleAdmin, adminRemoveClient, &rpc.MethodMeta{
		Name:    "admin:removeClient",
		Summary: "Delete a client",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "null",
	})
	RegisterWithGroupAndMeta("getClient", rpc.RoleAdmin, adminGetClient, &rpc.MethodMeta{
		Name:    "admin:getClient",
		Summary: "Get a client by UUID",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "Client",
	})
	RegisterWithGroupAndMeta("listClients", rpc.RoleAdmin, adminListClients, &rpc.MethodMeta{
		Name:    "admin:listClients",
		Summary: "List all clients (basic info)",
		Returns: "Client[]",
	})
	RegisterWithGroupAndMeta("getClientToken", rpc.RoleAdmin, adminGetClientToken, &rpc.MethodMeta{
		Name:    "admin:getClientToken",
		Summary: "Get a client's token by UUID",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Client UUID"},
		},
		Returns: "{ token: string }",
	})
	RegisterWithGroupAndMeta("clearRecords", rpc.RoleAdmin, adminClearRecords, &rpc.MethodMeta{
		Name:    "admin:clearRecords",
		Summary: "Delete all load records",
		Returns: "null",
	})
}

// auditActor 从上下文提取审计用的 actor UUID 与来源 IP。
func auditActor(ctx context.Context) (uuid, ip string) {
	if meta := rpc.MetaFromContext(ctx); meta != nil {
		uuid = meta.UserUUID
		ip = meta.RemoteIP
	}
	return uuid, ip
}

func adminAddClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Name string `json:"name"`
	}
	req.BindParams(&params)

	var (
		uuid, token string
		err         error
	)
	if params.Name == "" {
		uuid, token, err = clients.CreateClient()
	} else {
		uuid, token, err = clients.CreateClientWithName(params.Name)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	clientName := strings.TrimSpace(params.Name)
	if clientName == "" && len(uuid) >= 8 {
		clientName = "client_" + uuid[:8]
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "Create client: "+auditlog.FormatClient(clientName, uuid), "info")
	return map[string]any{"uuid": uuid, "token": token}, nil
}

func adminEditClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var update map[string]interface{}
	if err := req.BindParams(&update); err != nil || update == nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid params", nil)
	}
	uuid, _ := update["uuid"].(string)
	if uuid == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	submitted := make(map[string]any, len(update))
	for k, v := range update {
		submitted[k] = v
	}
	beforeClient, errBefore := clients.GetClientByUUID(uuid)
	if err := clients.SaveClient(update); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	afterClient, errAfter := clients.GetClientByUUID(uuid)

	clientName := ""
	switch {
	case errAfter == nil && strings.TrimSpace(afterClient.Name) != "":
		clientName = afterClient.Name
	case errBefore == nil && strings.TrimSpace(beforeClient.Name) != "":
		clientName = beforeClient.Name
	default:
		if n, ok := submitted["name"].(string); ok {
			clientName = n
		}
	}
	clientLabel := auditlog.FormatClient(clientName, uuid)

	var beforeMap map[string]any
	if errBefore == nil {
		beforeMap = clientAuditSnapshot(beforeClient)
	}
	changedKeys, diffLines := auditlog.FormatDiff(beforeMap, submitted, "uuid", "updated_at")
	summary := fmt.Sprintf("Edit client: %s", clientLabel)
	if len(changedKeys) > 0 {
		summary = fmt.Sprintf("Edit client: %s (%s)", clientLabel, strings.Join(changedKeys, ", "))
	} else {
		summary = fmt.Sprintf("Edit client: %s (no effective changes)", clientLabel)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, auditlog.JoinLines(summary, diffLines...), "info")
	return nil, nil
}

func clientAuditSnapshot(c models.Client) map[string]any {
	return map[string]any{
		"token":              c.Token,
		"name":               c.Name,
		"cpu_name":           c.CpuName,
		"virtualization":     c.Virtualization,
		"arch":               c.Arch,
		"cpu_cores":          c.CpuCores,
		"cpu_physical_cores": c.CpuPhysicalCores,
		"os":                 c.OS,
		"kernel_version":     c.KernelVersion,
		"gpu_name":           c.GpuName,
		"ipv4":               c.IPv4,
		"ipv6":               c.IPv6,
		"region":             c.Region,
		"remark":             c.Remark,
		"public_remark":      c.PublicRemark,
		"mem_total":          c.MemTotal,
		"swap_total":         c.SwapTotal,
		"disk_total":         c.DiskTotal,
		"version":            c.Version,
		"weight":             c.Weight,
		"price":              c.Price,
		"billing_cycle":      c.BillingCycle,
		"auto_renewal":       c.AutoRenewal,
		"currency":           c.Currency,
		"expired_at":         c.ExpiredAt,
		"group":              c.Group,
		"tags":               c.Tags,
		"hidden":             c.Hidden,
		"traffic_limit":      c.TrafficLimit,
		"traffic_limit_type": c.TrafficLimitType,
	}
}

func adminRemoveClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	existing, existingErr := clients.GetClientByUUID(params.UUID)
	if err := clients.DeleteClient(params.UUID); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete client"+err.Error(), nil)
	}
	metricstore.DeleteEntityAsync(params.UUID)
	actor, ip := auditActor(ctx)
	clientLabel := params.UUID
	meta := make([]string, 0, 3)
	if existingErr == nil {
		clientLabel = auditlog.FormatClient(existing.Name, params.UUID)
		if strings.TrimSpace(existing.Group) != "" {
			meta = append(meta, "group="+existing.Group)
		}
		if strings.TrimSpace(existing.Region) != "" {
			meta = append(meta, "region="+existing.Region)
		}
		if strings.TrimSpace(existing.IPv4) != "" {
			meta = append(meta, "ipv4="+existing.IPv4)
		}
	}
	msg := "Delete client: " + clientLabel
	if len(meta) > 0 {
		msg += " (" + strings.Join(meta, ", ") + ")"
	}
	auditlog.Log(ip, actor, msg, "warn")
	agent_runtime.DeleteConnectedClients(params.UUID)
	agent_runtime.DeleteLatestReport(params.UUID)
	return nil, nil
}

func adminGetClient(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	result, err := clients.GetClientByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return result, nil
}

func adminListClients(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	cls, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return cls, nil
}

func adminGetClientToken(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	req.BindParams(&params)
	if params.UUID == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing UUID", nil)
	}
	token, err := clients.GetClientTokenByUUID(params.UUID)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}
	return map[string]any{"token": token}, nil
}

func adminClearRecords(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if err := records.DeleteAll(); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete Record"+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "Clear load records (all clients)", "warn")
	return nil, nil
}
