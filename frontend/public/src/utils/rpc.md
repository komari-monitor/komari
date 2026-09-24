# Komari RPC2 客户端 SDK

基于 JSON-RPC 2.0 规范的 Komari RPC 客户端，支持 HTTP POST 和 WebSocket 两种传输方式。

## 安装与引入

```typescript
import { getSharedRpc, KomariRpc, RpcError } from '@/utils/rpc'
```

## 快速开始

### 使用共享实例

```typescript
// 获取全局共享实例
const rpc = getSharedRpc()

// 获取所有节点
const nodes = await rpc.getNodes()

// 获取节点最新状态
const status = await rpc.getNodesLatestStatus()

// 健康检查
const pong = await rpc.ping() // 'pong'
```

### 创建独立实例

```typescript
const rpc = new KomariRpc({
  baseUrl: '/api/rpc2', // 基础路径
  timeout: 30000, // 超时时间（毫秒）
  useWebSocket: false, // 是否使用 WebSocket
})
```

## API 参考

### RpcClientOptions 配置

| 参数           | 类型      | 默认值      | 说明                    |
| -------------- | --------- | ----------- | ----------------------- |
| `baseUrl`      | `string`  | `/api/rpc2` | RPC 接口基础路径        |
| `timeout`      | `number`  | `30000`     | 请求超时时间（毫秒）    |
| `useWebSocket` | `boolean` | `false`     | 是否使用 WebSocket 传输 |

### RPC 内置方法

#### `ping(): Promise<string>`

健康检查，返回 `pong`。

```typescript
const result = await rpc.ping()
// result: 'pong'
```

#### `getVersion(): Promise<string>`

获取 RPC 接口版本号。

```typescript
const version = await rpc.getVersion()
```

#### `getMethods(internal?: boolean): Promise<string[]>`

获取所有可用的 RPC 方法名称。

```typescript
const methods = await rpc.getMethods(true) // 包含内置方法
```

#### `getHelp(method: string): Promise<MethodMeta>`

获取指定方法的帮助信息。

```typescript
const help = await rpc.getHelp('common:getNodes')
// {
//   name: 'common:getNodes',
//   summary: '获取节点信息',
//   description: '...',
//   params: [...],
//   returns: 'Client | { [uuid]: Client }'
// }
```

### 通用方法

#### `getNodes(uuid?: string): Promise<Client | Record<string, Client>>`

获取节点信息。不传参数返回所有节点，传入 UUID 返回单个节点。

```typescript
// 获取所有节点
const allNodes = await rpc.getNodes() // Record<string, Client>

// 获取单个节点
const node = await rpc.getNodes('uuid-xxx') // Client
```

#### `getPublicInfo(): Promise<PublicInfo>`

获取公开的站点与运行配置信息。

```typescript
const info = await rpc.getPublicInfo()
// {
//   sitename: 'Komari Monitor',
//   description: '...',
//   oauth_enable: true,
//   ...
// }
```

#### `getBackendVersion(): Promise<VersionInfo>`

获取后端版本与构建哈希。

```typescript
const { version, hash } = await rpc.getBackendVersion()
```

#### `getNodesLatestStatus(uuid?: string, uuids?: string[]): Promise<Record<string, NodeStatus>>`

获取节点最新运行状态。

```typescript
// 获取所有节点状态
const allStatus = await rpc.getNodesLatestStatus()

// 获取单个节点状态
const status = await rpc.getNodesLatestStatus('uuid-xxx')

// 获取多个节点状态
const multiStatus = await rpc.getNodesLatestStatus(undefined, ['uuid-1', 'uuid-2'])
```

#### `getMe(): Promise<MeInfo>`

获取当前登录用户信息。

```typescript
const me = await rpc.getMe()
// {
//   logged_in: true,
//   username: 'user',
//   uuid: 'user-uuid',
//   ...
// }
```

#### `getNodeRecentStatus(uuid: string): Promise<RecentStatusResp>`

获取指定节点的最近状态记录列表。

```typescript
const { count, records } = await rpc.getNodeRecentStatus('uuid-xxx')
```

#### `getRecords(params: GetRecordsParams): Promise<...>`

获取历史记录（负载或 Ping），支持多种参数组合。

```typescript
// 获取负载记录
const loadRecords = await rpc.getRecords({
  type: 'load',
  uuid: 'uuid-xxx',
  hours: 24,
  load_type: 'cpu',
})

// 获取 Ping 记录
const pingRecords = await rpc.getRecords({
  type: 'ping',
  hours: 1,
  task_id: -1,
})
```

#### `getLoadRecords(uuid?, hours?, loadType?, maxCount?): Promise<LoadRecordsResult | LoadRecordsMapResult>`

便捷方法：获取负载历史记录。

```typescript
// 获取所有节点最近 1 小时的负载记录
const records = await rpc.getLoadRecords()

// 获取指定节点最近 24 小时的 CPU 记录
const cpuRecords = await rpc.getLoadRecords('uuid-xxx', 24, 'cpu')
```

#### `getPingRecords(taskId?, hours?, maxCount?): Promise<PingRecordsResult>`

便捷方法：获取 Ping 历史记录。

```typescript
// 获取所有任务最近 1 小时的 Ping 记录
const pingRecords = await rpc.getPingRecords()
```

## 类型定义

### Client 节点信息

```typescript
interface Client {
  uuid: string
  name: string
  cpu_name: string
  virtualization: string
  arch: string
  cpu_cores: number
  os: string
  kernel_version: string
  gpu_name?: string
  ipv4?: string
  ipv6?: string
  region: string
  public_remark: string
  mem_total: number // 字节
  swap_total: number // 字节
  disk_total: number // 字节
  weight: number
  price: number
  billing_cycle: number
  auto_renewal: boolean
  currency: string
  expired_at: string
  group: string
  tags: string
  hidden: boolean
  traffic_limit: number
  traffic_limit_type: string
  created_at: string
  updated_at: string
}
```

### NodeStatus 节点状态

```typescript
interface NodeStatus {
  client: string
  time: string
  cpu: number // 百分比 0-100
  gpu: number // 百分比 0-100
  ram: number // 已用字节
  ram_total: number // 总量字节
  swap: number
  swap_total: number
  load: number // 1分钟负载
  load5: number // 5分钟负载
  load15: number // 15分钟负载
  temp: number // 温度
  disk: number
  disk_total: number
  net_in: number // 入网速 字节/秒
  net_out: number // 出网速 字节/秒
  net_total_up: number
  net_total_down: number
  process: number
  connections: number
  connections_udp: number
  online: boolean
}
```

### MeInfo 用户信息

```typescript
interface MeInfo {
  '2fa_enabled': boolean
  'logged_in': boolean
  'sso_id': string
  'sso_type': string
  'username': string
  'uuid': string
}
```

## 错误处理

SDK 使用 `RpcError` 类封装错误：

```typescript
import { RpcError } from '@/utils/rpc'

try {
  const result = await rpc.getNodes('invalid-uuid')
}
catch (error) {
  if (error instanceof RpcError) {
    console.error('RPC Error:', error.code, error.message)
    console.error('Error data:', error.data)
  }
}
```

### 错误码

| 错误码 | 说明                 |
| ------ | -------------------- |
| -32000 | 网络错误/HTTP 错误   |
| -32001 | 请求超时             |
| 其他   | 服务端返回的业务错误 |

## WebSocket 模式

WebSocket 模式适用于需要频繁调用的场景，可以减少连接开销：

```typescript
const rpc = new KomariRpc({ useWebSocket: true })

// 或者动态切换
rpc.getClient().setTransport(true)

// 关闭 WebSocket 连接
rpc.close()
```

## 单例管理

```typescript
import { getSharedRpc, resetSharedRpc } from '@/utils/rpc'

// 获取共享实例
const rpc = getSharedRpc()

// 重置共享实例（关闭连接并清除）
resetSharedRpc()
```

## 最佳实践

### 1. 在 Vue 组件中使用

```typescript
import { getSharedRpc } from '@/utils/rpc'

const rpc = getSharedRpc()

onMounted(async () => {
  const nodes = await rpc.getNodes()
  // ...
})
```

### 2. 使用 VueUse 的 useAsyncState

```typescript
import { useAsyncState } from '@vueuse/core'
import { getSharedRpc } from '@/utils/rpc'

const rpc = getSharedRpc()

const { state: nodes, isLoading, error } = useAsyncState(
  () => rpc.getNodes(),
  {},
)
```

### 3. 在 Pinia Store 中使用

```typescript
import { defineStore } from 'pinia'
import { getSharedRpc } from '@/utils/rpc'

export const useNodeStore = defineStore('node', () => {
  const rpc = getSharedRpc()
  const nodes = ref<Record<string, Client>>({})

  async function fetchNodes() {
    nodes.value = await rpc.getNodes() as Record<string, Client>
  }

  return { nodes, fetchNodes }
})
```

## 参考文档

- [Komari RPC2 接口文档](https://www.komari.wiki/dev/rpc.html)
- [JSON-RPC 2.0 规范](https://www.jsonrpc.org/specification)
