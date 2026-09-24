# RPC2 客户端使用指南

这个 RPC2 客户端实现了 JSON-RPC 2.0 标准，支持通过 WebSocket 和 HTTP POST 调用 Komari 的 `/api/rpc2` 接口。

## 特性

- ✅ 支持 JSON-RPC 2.0 标准
- ✅ WebSocket 和 HTTP POST 双重支持
- ✅ **自动连接和维护 WebSocket**
- ✅ **自动心跳包维护连接（每5秒）**
- ✅ 自动重连机制
- ✅ 请求超时处理
- ✅ 批量请求支持
- ✅ 通知请求支持
- ✅ TypeScript 类型支持
- ✅ React Context 集成

## 快速开始

### 1. 基本用法

```typescript
import { RPC2Client } from './lib/rpc2';

// 创建客户端实例（默认启用自动连接）
const client = new RPC2Client('/api/rpc2');

// 直接调用，无需手动连接 - 会自动选择最佳方式
const result = await client.call('getStatus');

// 强制使用 HTTP 调用
const httpResult = await client.callViaHTTP('getNodes', { active: true });

// 强制使用 WebSocket 调用（会自动连接）
const wsResult = await client.callViaWebSocket('getNodes', { active: true });
```

### 2. React 应用集成

```tsx
import { RPC2Provider, useRPC2Call } from './contexts/RPC2Context';

// 在应用根组件包装 Provider
function App() {
  return (
    <RPC2Provider>
      <MyComponent />
    </RPC2Provider>
  );
}

// 在组件中使用
function MyComponent() {
  const { call } = useRPC2Call();

  const handleCall = async () => {
    try {
      const result = await call('getStatus');
      console.log(result);
    } catch (error) {
      console.error('调用失败:', error);
    }
  };

  return (
    <button onClick={handleCall} disabled={!isConnected}>
      调用 RPC
    </button>
  );
}
```

## API 参考

### RPC2Client 类

#### 构造函数
```typescript
new RPC2Client(baseUrl?: string, options?: RPC2ConnectionOptions)
```

#### 主要方法

- `call(method, params?, options?)`: **推荐使用** - 自动选择最佳调用方式
- `callViaWebSocket(method, params?, options?)`: 强制使用 WebSocket 调用
- `callViaHTTP(method, params?, options?)`: 强制使用 HTTP 调用
- `batchCall(requests)`: 批量调用（仅 HTTP）
- `connect()`: 手动建立 WebSocket 连接（通常不需要）
- `disconnect()`: 断开 WebSocket 连接

#### 配置选项

```typescript
interface RPC2ConnectionOptions {
```typescript
interface RPC2ConnectionOptions {
  autoConnect?: boolean;          // 自动建立连接，默认 true
  autoReconnect?: boolean;        // 自动重连，默认 true
  reconnectInterval?: number;     // 重连间隔，默认 3000ms
  maxReconnectAttempts?: number;  // 最大重连次数，默认 5
  requestTimeout?: number;        // 请求超时，默认 30000ms
  enableHeartbeat?: boolean;      // 启用心跳包，默认 true
  heartbeatInterval?: number;     // 心跳包间隔，默认 5000ms（5秒）
  headers?: Record<string, string>; // 自定义请求头
}
```

**注意**:
- 默认配置下，WebSocket 连接会自动建立和维护，无需手动管理
- 心跳包会每5秒自动发送，保持连接活跃

### React Hooks

#### useRPC2()
返回 RPC2 连接状态和控制方法。

#### useRPC2Call()
返回 RPC 调用方法。

## 使用示例

### 基本调用
```typescript
// 获取系统状态
const status = await client.call('getStatus');

// 获取节点列表
const nodes = await client.call('getNodes', { active: true });

// 更新节点
const result = await client.call('updateNode', {
  id: 1,
  name: 'new-name',
  weight: 100
});
```

### 批量调用
```typescript
const results = await client.batchCall([
  { method: 'getStatus' },
  { method: 'getNodes', params: { active: true } },
  { method: 'getVersion' }
]);
```

### 通知请求
```typescript
// 发送通知（不期望响应）
await client.call('notifyUpdate', {
  timestamp: Date.now()
}, { notification: true });
```

### WebSocket 连接管理

**自动模式（推荐）:**
```typescript
// 创建客户端，自动连接和管理
const client = new RPC2Client('/api/rpc2');

// 直接调用，无需手动管理连接
const result = await client.call('getStatus');

// 设置事件监听
client.setEventListeners({
  onConnect: () => console.log('WebSocket 已连接'),
  onDisconnect: () => console.log('WebSocket 连接断开'),
  onError: (error) => console.error('连接错误:', error),
  onReconnecting: (attempt) => console.log(`重连尝试 ${attempt}`)
});
```

**手动模式:**
```typescript
// 禁用自动连接
const client = new RPC2Client('/api/rpc2', { autoConnect: false });

// 手动建立连接
await client.connect();

// 手动断开连接
client.disconnect();
```

## 错误处理

客户端会自动处理以下错误情况：

1. **网络错误**: 自动重连机制
2. **请求超时**: 可配置超时时间
3. **JSON-RPC 错误**: 标准错误码处理
4. **连接断开**: 自动重连和状态通知

## 注意事项

1. **默认启用自动连接**: WebSocket 连接会自动建立和维护
2. 批量调用仅支持 HTTP 方式
3. 通知请求不会返回响应
4. 请确保服务器端支持 JSON-RPC 2.0 标准
