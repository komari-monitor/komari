# RPC2 内部架构

本目录是 komari 前端面 RPC（JSON-RPC 2.0）的服务端实现。所有对外接口最终都收敛为一条统一管道：

```
对外接口 (REST / WebSocket / 内部调用)
        │  生成 RPC 请求
        ▼
   Dispatch (私有站点检查 → 命名空间权限校验)
        │
        ▼
   rpc.CallWithContext → 已注册的 RPC handler (业务逻辑)
```

## 分层

| 文件 | 职责 |
| --- | --- |
| `pkg/rpc/*` | 协议内核：请求/响应类型、注册表、调用 (`Call`/`Invoke`)、权限模型 (`permission.go`) |
| `transport.go` | 传输适配：`/api/rpc2` 的 HTTP POST 与 WebSocket，身份识别，`CallFromGin` |
| `dispatch.go` | 统一分发入口 `Dispatch`，内部调用入口 `OnInternalRequest` |
| `register.go` | 注册便捷函数 `Register` / `RegisterWithGroupAndMeta` |
| `common*.go` | `common` 命名空间业务方法 |
| `admin.*.go` | `admin` 命名空间业务方法 |

## 命名空间与权限（声明式 ACL + 通配符）

方法名约定为 `namespace:method`，不含 `:` 时归入默认命名空间 `common`（内部方法用 `rpc.` 前缀）。

权限采用**声明式 ACL**：一组规则 `(pattern, minRole)`，pattern 支持 `*` 通配符，匹配完整方法名。
判权时在所有匹配规则中取**特异性最高**者：精确匹配 > 字面前缀更长的通配 > 全局 `*`；
特异性相同时取更严格（等级更高）的角色，偏向安全。

角色等级：`guest (0) < client (1) < admin (2)`。

内置默认规则（见 `pkg/rpc/permission.go` 的 `init`）：

| 规则 pattern | 所需角色 |
| --- | --- |
| `*`（兜底） | admin |
| `common:*` / `guest:*` / `rpc.*` / `rpc:*` | guest |
| `client:*` | client |
| `admin:*` | admin |

声明自定义权限（供插件使用）：

```go
rpc.Allow("plugin:*", rpc.RoleClient)        // 整个命名空间
rpc.Allow("plugin:publicStat", rpc.RoleGuest) // 更具体的方法级规则可放宽
rpc.RegisterNamespace("plugin", rpc.RoleAdmin) // 等价于 Allow("plugin:*", admin)
```

由于按特异性裁决，`plugin:*`=admin 与 `plugin:publicStat`=guest 可共存：访客能调用 `publicStat`，
其余 `plugin:*` 方法仍要求 admin。

## 注册一个 RPC 方法

```go
func init() {
    jsonrpc.RegisterWithGroupAndMeta("addClient", rpc.RoleAdmin, adminAddClient,
        &rpc.MethodMeta{
            Name:    "admin:addClient",
            Summary: "Create a new client",
            Params:  []rpc.ParamMeta{{Name: "name", Type: "string"}},
            Returns: "{ uuid, token }",
        })
}

func adminAddClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
    var params struct{ Name string `json:"name"` }
    req.BindParams(&params)
    // ... 业务逻辑
    return result, nil
}
```

- handler 返回 `(result, nil)` 表示成功，`(nil, *rpc.JsonRpcError)` 表示失败。
- 通过 `rpc.MetaFromContext(ctx)` 获取调用者身份（权限分组、用户 UUID、客户端 token、来源 IP 等）。
- 用 `req.BindParams(&struct)` 绑定参数，兼容具名对象与位置数组。

## 让传统 REST handler 转调 RPC

迁移中的 gin handler 用 `jsonrpc.CallFromGin` 复用同一套权限/审计上下文：

```go
func GetClient(c *gin.Context) {
    resp := jsonRpc.CallFromGin(c, "admin:getClient", map[string]any{"uuid": c.Param("uuid")})
    if resp.Error != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": resp.Error.Message})
        return
    }
    c.JSON(http.StatusOK, resp.Result)
}
```

handler 退化为薄适配层：解析 gin 参数 → 调 RPC → 把响应映射回原 REST JSON 形状（保持前端契约不变）。

## 插件扩展点（地基已就位，插件本身待实现）

`pkg/rpc` 已提供供插件运行时使用的接口：

- `rpc.Register(method, handler)` / `rpc.MustRegister` — 注册方法（禁止 `rpc.` 保留前缀，重复注册报错）。
- `rpc.Unregister(method) bool` — 动态注销方法并清理元数据（供插件卸载），保留前缀不可注销。
- `rpc.Allow(pattern, minRole)` — 声明式 ACL 规则，支持通配符。
- `rpc.RegisterNamespace(namespace, requiredRole)` — 为整个命名空间声明所需角色。

插件既可 `rpc.Invoke` 调用已有方法，也可注册自己的命名空间方法并用 `Allow` 声明权限，复用统一的权限校验与分发。

## 已迁移接口（REST → RPC2）

几乎所有 JSON 类接口已迁移为 RPC2 方法，router 通过声明式路由桥 `Bind` 直接绑定，
不再有 per-resource gin handler 层。

| 命名空间 | 方法（文件） |
| --- | --- |
| `admin` | client CRUD、ping task、session/settings/weight、notification（load/offline/traffic）、clipboard、provider（messageSender/oidc）、task 查询、system（logs/cloudflared/exec/test）、xtermjs |
| `public` | getMe、getNodesInformation、getPublicSettings、getVersion、getClientRecentRecords、getRecordsByUUID、getPingRecords、getPublicPingTasks |
| `client` | getPingTasks、uploadPingResult、taskResult |

### 声明式路由桥 `Bind`

`web/rpc/jsonrpc/bridge.go` 提供：

```go
r.GET("/api/admin/client/:uuid", jsonRpc.Bind("admin:getClient", jsonRpc.WithPath("uuid"), jsonRpc.WithRaw()))
```

- 参数装配：JSON body（对象/数组）+ `WithPath(...)` 路径参数 + `WithQuery(...)` 查询参数，合并为 RPC 参数。
- 响应渲染器（保契约）：
  - 默认 `renderStandard` → `{status:"success", message, data}`（data 为空时省略，对齐 `api.Response`）。
  - `WithFlat()` → 把 result(map) 平铺到顶层 + `{status:"success"}`（addClient/getClientToken/getSessions/provider set）。
  - `WithRaw()` → 直接输出 result（agent 裸 JSON / me / listClients / getClient）。
  - `WithMessage(msg)` → 成功带固定 message（xtermjs 保存）。
- 错误：统一 `{status:"error", message}` + JSON-RPC 错误码到 HTTP 码映射。

### 保留为 REST 的接口（不走 RPC 桥）

二进制/流/重定向/特殊鉴权类，集中在 `web/api/admin`（2fa/theme/backup/update/oauth 绑定）、
`web/api/public`（login/logout/oauth/mjpeg）、`web/api/client`（report WS+POST、v2 RPC、uploadBasicInfo、terminal、AutoDiscovery 注册）。

agent v1/v2 上报的核心逻辑已统一到 `web/api/client/ingest.go`。
