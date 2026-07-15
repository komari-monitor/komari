# Visitor Audit RPC 使用说明

本文档说明 `public:recordVisitorEvent` RPC2 接口的调用方式。该接口用于让主题前端把访客访问和前端操作记录写入 Komari 后端审计日志。

## 接口概览

- RPC 方法：`public:recordVisitorEvent`
- RPC 路径：`/api/rpc2`
- HTTP 方法：`POST`
- 写入位置：现有审计日志表 `models.Log`
- 管理端读取：复用现有 `admin:getLogs`
- 日志类型：`msg_type = "visitor"`
- 默认状态：关闭，需设置 `visitor_audit_enabled = true`

该接口不会信任前端传入的 IP。来源 IP 和 User-Agent 由后端从请求上下文记录。

## 权限

`public:recordVisitorEvent` 属于 `public` 命名空间，访客可调用。

私有站点模式下，该方法也在登录页白名单内，便于记录登录前/前台访问事件。

这是有意保留的行为；总开关关闭时，白名单中的方法仍可调用，但不会写库。

## 启用

存量实例和新实例默认都不会开放匿名日志写入。管理员可通过
`admin:editSettings` 将 `visitor_audit_enabled` 设为 `true`。公开设置
`public:getPublicSettings` 也会返回这个字段，主题可据此决定是否上报。

未启用时接口返回 `status = "disabled"`。启用后，每个来源 IP 使用内存令牌桶限流：
平均每分钟 30 次，允许短时突发 10 次；超限时返回 `status = "rate_limited"`，不写库。

## 请求参数

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `event` | `string` | 是 | 事件名，例如 `page_view`、`node_open`、`search` |
| `action` | `string` | 否 | `event` 的别名 |
| `operation` | `string` | 否 | `event` 的别名 |
| `path` | `string` | 否 | 前端路径，例如 `/`、`/instance/<uuid>` |
| `route` | `string` | 否 | 前端路由名，例如 `home`、`instance-detail` |
| `target` | `string` | 否 | 操作目标，例如节点 UUID、工具 key、按钮 key |
| `detail` | `object` | 否 | 额外元数据，大小受限 |

`event`、`action`、`operation` 三者任选一个即可，优先级为：

```text
event > action > operation
```

## 后端自动记录的信息

| 字段 | 来源 |
| --- | --- |
| IP | `rpc.ContextMeta.RemoteIP`，来自服务端 `c.ClientIP()` |
| User-Agent | `rpc.ContextMeta.UserAgent`，来自请求头 |
| 用户 UUID | 登录状态下使用 `rpc.ContextMeta.UserUUID`；访客为空 |
| 时间 | 后端写入审计日志时生成 |

## 字段限制

字符串字段按 Unicode 字符计数，`detail` 和最终 message 按序列化后的字节数计数。

| 字段 | 最大长度 |
| --- | ---: |
| `event` | 64 |
| `path` | 512 |
| `route` | 128 |
| `target` | 128 |
| User-Agent | 512 |
| `detail` JSON | 2048 |
| 最终日志 message | 4096 |

如果 `detail` 超过限制，不会完整保存，而会写入截断标记：

```json
{
  "truncated": true,
  "size": 4096
}
```

## 事件名规范化

服务端会规范化事件名：

- 转小写
- 空格转 `_`
- 仅保留字母、数字、`_`、`-`、`:`、`.`
- 最大长度 64

示例：

```text
Page View -> page_view
node:open.detail -> node:open.detail
```

## 请求示例：页面访问

```bash
curl -X POST http://localhost:25774/api/rpc2 \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc": "2.0",
    "method": "public:recordVisitorEvent",
    "params": {
      "event": "page_view",
      "path": "/",
      "route": "home",
      "detail": {
        "theme": "glassmorphism"
      }
    },
    "id": 1
  }'
```

写入成功返回：

```json
{
  "jsonrpc": "2.0",
  "result": {
    "status": "success"
  },
  "id": 1
}
```

`disabled` 和 `rate_limited` 也属于正常响应，不会返回 JSON-RPC 错误；主题上报不应影响主流程。

## 请求示例：打开节点详情

```json
{
  "jsonrpc": "2.0",
  "method": "public:recordVisitorEvent",
  "params": {
    "event": "node_open",
    "path": "/instance/6f0b-example",
    "route": "instance-detail",
    "target": "6f0b-example",
    "detail": {
      "source": "node_card"
    }
  },
  "id": 2
}
```

## 请求示例：搜索

```json
{
  "jsonrpc": "2.0",
  "method": "public:recordVisitorEvent",
  "params": {
    "event": "search",
    "path": "/",
    "route": "home",
    "detail": {
      "keyword_length": 6,
      "result_count": 3
    }
  },
  "id": 3
}
```

建议不要记录完整搜索关键词，避免把敏感内容写入审计日志。

## 前端封装示例

```ts
interface VisitorAuditEvent {
  event?: string
  action?: string
  operation?: string
  path?: string
  route?: string
  target?: string
  detail?: Record<string, unknown>
}

let auditRequestId = 0

export async function recordVisitorEvent(event: VisitorAuditEvent): Promise<void> {
  try {
    await fetch('/api/rpc2', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      credentials: 'include',
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'public:recordVisitorEvent',
        params: event,
        id: ++auditRequestId,
      }),
    })
  }
  catch {
    // 审计上报不应影响用户正常浏览
  }
}
```

## Vue Router 访问记录示例

```ts
router.afterEach((to) => {
  void recordVisitorEvent({
    event: 'page_view',
    path: to.fullPath,
    route: String(to.name ?? ''),
    detail: {
      params: Object.keys(to.params),
      query_keys: Object.keys(to.query),
    },
  })
})
```

建议只记录 query key，不记录完整 query value，避免泄露 token 或敏感参数。

## 推荐事件名

| 场景 | event |
| --- | --- |
| 页面访问 | `page_view` |
| 打开节点详情 | `node_open` |
| 搜索 | `search` |
| 切换分组 | `group_change` |
| 切换视图模式 | `view_mode_change` |
| 点击管理入口 | `admin_entry_click` |
| 打开高级工具 | `home_tool_open` |
| 审计日志刷新 | `audit_refresh` |
| 审计日志翻页 | `audit_page_change` |
| 导出 JSON | `export_json` |
| 导出 CSV | `export_csv` |
| WebRTC 自检开始 | `webrtc_check_start` |
| WebRTC 自检完成 | `webrtc_check_done` |

## 审计日志保存形态

写入现有 `models.Log`：

| 字段 | 值 |
| --- | --- |
| `ip` | 服务端看到的来源 IP |
| `uuid` | 登录用户 UUID；访客为空 |
| `msg_type` | `visitor` |
| `message` | `visitor event: {...}` |
| `time` | 后端写入时间 |

示例 `message`：

```text
visitor event: {"event":"page_view","path":"/","route":"home","user_agent":"Mozilla/5.0 ...","detail":{"theme":"glassmorphism"}}
```

## 管理端读取

复用现有接口：

```text
admin:getLogs
```

请求示例：

```json
{
  "jsonrpc": "2.0",
  "method": "admin:getLogs",
  "params": {
    "limit": "100",
    "page": "1",
    "msg_type": "visitor"
  },
  "id": 10
}
```

`msg_type` 为可选的精确匹配过滤参数。传入 `visitor` 后，计数和分页都会在 SQL 查询层过滤，
无需先拉取所有日志再由前端筛选。

## 安全注意事项

### 不要从前端传 IP

前端 IP 不可信。该接口会自动使用服务端看到的来源 IP。

### 不要记录敏感值

不建议记录：

- 密码
- token
- cookie
- 完整 URL query
- 完整搜索关键词
- 导出内容
- WebSSH 命令内容
- 剪贴板内容

建议只记录：

- 操作类型
- 路由
- 目标 ID
- 结果数量
- 布尔状态
- 非敏感摘要

### 上报不要阻塞主流程

前端调用建议使用：

```ts
void recordVisitorEvent(...)
```

即使上报失败，也不影响页面功能。
