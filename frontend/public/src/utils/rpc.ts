import { NETWORK_CONFIG } from '@/constants/network'

/**
 * Komari RPC2 Client SDK
 * @see https://www.komari.wiki/dev/rpc.html
 */

// ==================== 类型定义 ====================

/** JSON-RPC 2.0 请求结构 */
interface JsonRpcRequest {
  jsonrpc: '2.0'
  method: string
  params?: Record<string, unknown> | unknown[]
  id: number | string
}

/** JSON-RPC 2.0 成功响应 */
interface JsonRpcSuccessResponse<T = unknown> {
  jsonrpc: '2.0'
  result: T
  id: number | string
}

/** JSON-RPC 2.0 错误响应 */
interface JsonRpcErrorResponse {
  jsonrpc: '2.0'
  error: {
    code: number
    message: string
    data?: unknown
  }
  id: number | string | null
}

/** JSON-RPC 2.0 响应 */
type JsonRpcResponse<T = unknown> = JsonRpcSuccessResponse<T> | JsonRpcErrorResponse

const HTTP_PROTOCOL_PREFIX = 'http://'
const HTTPS_PROTOCOL_PREFIX = 'https://'
const WS_PROTOCOL_PREFIX = 'ws://'
const WSS_PROTOCOL_PREFIX = 'wss://'

function linkAbortSignal(controller: AbortController, signal?: AbortSignal): () => void {
  if (!signal)
    return () => {}

  const abort = () => controller.abort()
  if (signal.aborted) {
    abort()
    return () => {}
  }

  signal.addEventListener('abort', abort, { once: true })
  return () => signal.removeEventListener('abort', abort)
}

/** RPC 方法元数据 */
export interface MethodMeta {
  name: string
  summary: string
  description: string
  params: ParamMeta[]
  returns: string
}

/** 参数元数据 */
export interface ParamMeta {
  name: string
  type: string
  description: string
}

/** 节点客户端信息 */
export interface Client {
  uuid: string
  token?: string
  name: string
  cpu_name: string
  virtualization: string
  arch: string
  cpu_cores: number
  cpu_physical_cores?: number
  os: string
  kernel_version: string
  gpu_name?: string
  ipv4?: string
  ipv6?: string
  region: string
  remark?: string
  public_remark: string
  mem_total: number
  swap_total: number
  disk_total: number
  version?: string
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
  traffic_reset_day: number
  traffic_reset_time: string
  traffic_reset_timezone: string
  created_at: string
  updated_at: string
}

/** 公开站点信息 */
export interface PublicInfo {
  allow_cors: boolean
  custom_body: string
  custom_head: string
  description: string
  disable_password_login: boolean
  oauth_enable: boolean
  oauth_provider: string
  ping_record_preserve_time?: number
  private_site: boolean
  record_enabled?: boolean
  record_preserve_time?: number
  sitename: string
  theme: string
  theme_settings: Record<string, unknown>
  visitor_audit_enabled?: boolean
}

/** 版本信息 */
export interface VersionInfo {
  version: string
  hash: string
}

/**
 * 单个 Ping 任务的最新探测汇总
 * 注意：该字段在 getNodesLatestStatus 响应中实际存在，但官方文档与旧类型定义遗漏，键为 task_id 字符串
 */
export interface NodeStatusPing {
  name: string
  /** 最新探测延迟（毫秒）；<0 表示丢包，与 PingRecord.value === -1 同义 */
  latest: number
  avg: number
  tail: number
  /** 丢包率（%） */
  loss: number
  min: number
  max: number
}

export interface GpuDetailedInfo {
  name?: string
  device_name?: string
  device_index?: number
  memory_total?: number
  memory_used?: number
  utilization?: number
  usage?: number
  temperature?: number
}

/** 节点状态 */
export interface NodeStatus {
  client: string
  time: string
  cpu: number
  gpu: number
  gpu_count?: number
  gpu_average_usage?: number
  gpu_detailed_info?: GpuDetailedInfo[]
  ram: number
  ram_total: number
  swap: number
  swap_total: number
  load: number
  load5: number
  load15: number
  temp: number
  disk: number
  disk_total: number
  net_in: number
  net_out: number
  net_total_up: number
  net_total_down: number
  traffic_up?: number
  traffic_down?: number
  process: number
  connections: number
  connections_udp: number
  online: boolean
  uptime: number
  message?: string
  updated_at?: string
  /** 各 Ping 任务最新探测汇总，键为 task_id 字符串 */
  ping?: Record<string, NodeStatusPing>
}

/** 状态记录 */
export interface StatusRecord {
  client: string
  time: string
  cpu: number
  gpu: number
  gpu_count?: number
  gpu_average_usage?: number
  gpu_detailed_info?: GpuDetailedInfo[]
  ram: number
  ram_total: number
  swap: number
  swap_total: number
  load: number
  load5: number
  load15: number
  temp: number
  disk: number
  disk_total: number
  net_in: number
  net_out: number
  net_total_up: number
  net_total_down: number
  traffic_up?: number
  traffic_down?: number
  process: number
  connections: number
  connections_udp: number
}

/** Ping 记录 */
export interface PingRecord {
  client: string
  task_id: number
  time: string
  value: number
}

/** Ping 任务摘要 */
export interface PingTaskInfo {
  id: number
  weight?: number
  name: string
  interval: number
  loss: number
  default_on?: boolean
  clients?: string[]
  p99?: number
  p50?: number
  p99_p50_ratio?: number
  min?: number
  max?: number
  avg?: number
  latest?: number
  total?: number
  valid?: number
  stddev?: number
  loss_approximate?: boolean
  type?: string
}

export interface AuditLogEntry {
  id: number
  ip: string
  uuid: string
  message: string
  msg_type: string
  time: string
}

export interface AuditLogsResponse {
  logs: AuditLogEntry[]
  total: number
}

export interface VisitorAuditEventParams extends Record<string, unknown> {
  event: string
  path?: string
  route?: string
  target?: string
  detail?: Record<string, unknown>
}

export interface VisitorAuditEventResponse {
  status: 'success' | 'disabled' | 'rate_limited' | string
}

export interface MetricDefinition {
  name: string
  description: string | Record<string, string>
  type: string
  unit?: string
  retention_days: number
  metadata?: Record<string, string>
  created_at?: string
  updated_at?: string
}

export interface MetricPoint {
  time: string
  value: number | null
  count?: number
  /** @deprecated Komari 1.2.x compatibility alias; prefer tags. */
  tag?: Record<string, unknown>
  tags?: Record<string, unknown>
  labels?: Record<string, unknown>
}

export interface MetricSeries {
  metric_key: string
  entity_id: string
  type?: string
  unit?: string
  retention_days?: number
  /** @deprecated Komari 1.2.x compatibility alias; prefer tags. */
  tag?: Record<string, unknown>
  tags?: Record<string, unknown>
  downsampled: boolean
  downsample_algorithm?: string
  fill_empty?: boolean
  max_points?: number
  interval_seconds?: number
  count: number
  points: MetricPoint[]
}

export interface MetricQueryParams {
  [key: string]: unknown
  metric_key?: string
  metric_keys?: string[]
  metrics?: string[]
  entity_id?: string
  entity_ids?: string[]
  start?: string | number
  start_time?: string | number
  end?: string | number
  end_time?: string | number
  hours?: number
  tags?: Record<string, unknown>
  downsample?: boolean
  server_downsample?: boolean
  downsample_by_metric?: Record<string, boolean>
  server_downsample_by_metric?: Record<string, boolean>
  fill_empty?: boolean
  max_points?: number
  downsample_points?: number
  max_points_by_metric?: Record<string, number>
  points_by_metric?: Record<string, number>
  aggregation?: string
  downsample_algorithm?: string
  algorithm?: string
  aggregation_by_metric?: Record<string, string>
  downsample_algorithm_by_metric?: Record<string, string>
  algorithm_by_metric?: Record<string, string>
}

export interface MetricQueryResponse {
  start: string
  end: string
  server_downsample_default?: boolean
  default_points?: number
  series: MetricSeries[]
  count: number
}

export interface PingMetricStatsParams {
  [key: string]: unknown
  uuid?: string
  entity_id?: string
  entity_ids?: string[]
  task_id?: string | number
  task_ids?: Array<string | number>
  start?: string | number
  start_time?: string | number
  end?: string | number
  end_time?: string | number
  hours?: number
  max_points?: number
  downsample_points?: number
}

export interface PingMetricTaskStats {
  entity_id: string
  task_id: string
  name?: string
  type?: string
  interval?: number
  tags: Record<string, unknown>
  total: number
  valid: number
  loss: number
  loss_approximate: boolean
  min?: number
  max?: number
  avg?: number
  latest?: number
  p50?: number
  p99?: number
  stddev?: number
  p99_p50_ratio?: number
}

export interface PingMetricStatsResponse {
  start: string
  end: string
  interval_seconds: number
  stats: PingMetricTaskStats[]
  count: number
}

/** RPC 错误 */
export class RpcError extends Error {
  code: number
  data?: unknown

  constructor(code: number, message: string, data?: unknown) {
    super(message)
    this.name = 'RpcError'
    this.code = code
    this.data = data
  }
}

/** RpcClient 配置选项 */
interface RpcClientOptions {
  baseUrl?: string
  timeout?: number
  /** 是否使用 WebSocket，默认 false */
  useWebSocket?: boolean
}

/** JSON-RPC 2.0 客户端 */
export class RpcClient {
  private baseUrl: string
  private timeout: number
  private useWebSocket: boolean
  private ws: WebSocket | null = null
  private pendingRequests: Map<number | string, {
    resolve: (value: unknown) => void
    reject: (reason: unknown) => void
    timer: ReturnType<typeof setTimeout>
  }> = new Map()

  private requestId = 0
  private wsCloseListeners = new Set<() => void>()
  /** WebSocket 连接 Promise（用于等待正在进行的连接） */
  private wsConnectPromise: Promise<void> | null = null

  constructor(options: RpcClientOptions = {}) {
    const apiBase = import.meta.env.VITE_API_BASE || '/api'
    this.baseUrl = options.baseUrl || `${apiBase}/rpc2`
    this.timeout = options.timeout || NETWORK_CONFIG.timeout.request
    this.useWebSocket = options.useWebSocket || false
  }

  private getWebSocketUrl(): string {
    if (this.baseUrl.startsWith(HTTPS_PROTOCOL_PREFIX))
      return this.baseUrl.replace(HTTPS_PROTOCOL_PREFIX, WSS_PROTOCOL_PREFIX)
    if (this.baseUrl.startsWith(HTTP_PROTOCOL_PREFIX))
      return this.baseUrl.replace(HTTP_PROTOCOL_PREFIX, WS_PROTOCOL_PREFIX)

    const origin = typeof window !== 'undefined' ? window.location.origin : ''
    const url = new URL(this.baseUrl, origin || 'http://localhost')
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    return url.toString()
  }

  private rejectPendingRequests(error: RpcError): void {
    this.pendingRequests.forEach((pending, id) => {
      clearTimeout(pending.timer)
      pending.reject(error)
      this.pendingRequests.delete(id)
    })
  }

  private parseJsonRpcResponse(raw: unknown): JsonRpcResponse | null {
    if (!raw || typeof raw !== 'object')
      return null
    const record = raw as Record<string, unknown>
    if (record.jsonrpc !== '2.0')
      return null
    if (typeof record.id !== 'number' && typeof record.id !== 'string' && record.id !== null)
      return null
    if ('error' in record) {
      const error = record.error as Record<string, unknown> | null
      if (!error || typeof error.code !== 'number' || typeof error.message !== 'string')
        return null
      return record as unknown as JsonRpcResponse
    }
    if ('result' in record)
      return record as unknown as JsonRpcResponse
    return null
  }

  private emitWebSocketClose(): void {
    this.wsCloseListeners.forEach(listener => listener())
  }

  /**
   * 调用 RPC 方法（HTTP POST）
   */
  private async callHttp<T>(method: string, params?: Record<string, unknown> | unknown[], signal?: AbortSignal): Promise<T> {
    const id = ++this.requestId
    const request: JsonRpcRequest = {
      jsonrpc: '2.0',
      method,
      params,
      id,
    }

    const controller = new AbortController()
    const unlinkAbortSignal = linkAbortSignal(controller, signal)
    const timeoutId = setTimeout(() => controller.abort(), this.timeout)

    try {
      const response = await fetch(this.baseUrl, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(request),
        signal: controller.signal,
      })

      if (!response.ok) {
        throw new RpcError(response.status, `HTTP error: ${response.status}`)
      }

      const data = this.parseJsonRpcResponse(await response.json()) as JsonRpcResponse<T> | null
      if (!data)
        throw new RpcError(-32603, 'Invalid JSON-RPC response')
      return this.handleResponse(data)
    }
    catch (error) {
      if (error instanceof RpcError)
        throw error
      throw new RpcError(-32000, `Network error: ${error instanceof Error ? error.message : String(error)}`)
    }
    finally {
      clearTimeout(timeoutId)
      unlinkAbortSignal()
    }
  }

  /**
   * 确保 WebSocket 连接已建立并就绪
   * 如果已有连接正在建立中，等待其完成
   */
  private async ensureWebSocketReady(): Promise<void> {
    // 已连接，直接返回
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      return
    }

    // 有正在进行的连接，等待它
    if (this.wsConnectPromise) {
      return this.wsConnectPromise
    }

    // 创建新连接
    this.wsConnectPromise = this.initWebSocket()
    try {
      await this.wsConnectPromise
    }
    finally {
      this.wsConnectPromise = null
    }
  }

  /**
   * 初始化 WebSocket 连接
   */
  private initWebSocket(): Promise<void> {
    return new Promise((resolve, reject) => {
      const wsUrl = this.getWebSocketUrl()
      const connectTimeout = setTimeout(() => {
        if (this.ws?.readyState === WebSocket.CONNECTING)
          this.ws.close()
        reject(new RpcError(-32001, 'WebSocket connection timeout'))
      }, this.timeout)

      // 关闭现有连接（如果有）
      if (this.ws) {
        this.ws.onopen = null
        this.ws.onerror = null
        this.ws.onmessage = null
        this.ws.onclose = null
        if (this.ws.readyState !== WebSocket.CLOSED) {
          this.ws.close()
        }
      }

      this.ws = new WebSocket(wsUrl)

      this.ws.onopen = () => {
        clearTimeout(connectTimeout)
        resolve()
      }

      this.ws.onerror = () => {
        clearTimeout(connectTimeout)
        reject(new RpcError(-32000, 'WebSocket connection error'))
      }

      this.ws.onmessage = (event) => {
        try {
          const data = this.parseJsonRpcResponse(JSON.parse(event.data))
          if (!data || data.id === null)
            return
          const pending = this.pendingRequests.get(data.id)
          if (pending) {
            clearTimeout(pending.timer)
            this.pendingRequests.delete(data.id)
            try {
              pending.resolve(this.handleResponse(data))
            }
            catch (error) {
              pending.reject(error)
            }
          }
        }
        catch {
          // Ignore parse errors
        }
      }

      this.ws.onclose = () => {
        clearTimeout(connectTimeout)
        this.ws = null
        this.wsConnectPromise = null
        this.rejectPendingRequests(new RpcError(-32000, 'WebSocket closed'))
        this.emitWebSocketClose()
      }
    })
  }

  /**
   * 调用 RPC 方法（WebSocket）
   */
  private async callWebSocket<T>(method: string, params?: Record<string, unknown> | unknown[], signal?: AbortSignal): Promise<T> {
    if (signal?.aborted)
      throw new RpcError(-32000, 'Request aborted')

    await this.ensureWebSocketReady()

    return new Promise((resolve, reject) => {
      if (signal?.aborted) {
        reject(new RpcError(-32000, 'Request aborted'))
        return
      }

      const id = ++this.requestId
      const request: JsonRpcRequest = {
        jsonrpc: '2.0',
        method,
        params,
        id,
      }

      let settled = false
      let timer: ReturnType<typeof setTimeout> | null = null
      let abort = () => {}

      const cleanup = (): boolean => {
        if (settled)
          return false
        settled = true
        signal?.removeEventListener('abort', abort)
        this.pendingRequests.delete(id)
        if (timer)
          clearTimeout(timer)
        return true
      }

      abort = () => {
        if (cleanup())
          reject(new RpcError(-32000, 'Request aborted'))
      }

      timer = setTimeout(() => {
        if (cleanup())
          reject(new RpcError(-32001, 'Request timeout'))
      }, this.timeout)

      if (signal)
        signal.addEventListener('abort', abort, { once: true })

      this.pendingRequests.set(id, {
        resolve: (value) => {
          if (cleanup())
            resolve(value as T)
        },
        reject: (reason) => {
          if (cleanup())
            reject(reason)
        },
        timer,
      })

      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        if (cleanup())
          reject(new RpcError(-32000, 'WebSocket not connected'))
        return
      }

      try {
        this.ws.send(JSON.stringify(request))
      }
      catch (error) {
        if (cleanup())
          reject(new RpcError(-32000, `WebSocket send failed: ${error instanceof Error ? error.message : String(error)}`))
      }
    })
  }

  /**
   * 处理响应
   */
  private handleResponse<T>(response: JsonRpcResponse<T>): T {
    if ('error' in response) {
      throw new RpcError(response.error.code, response.error.message, response.error.data)
    }
    return response.result
  }

  /**
   * 调用 RPC 方法
   */
  async call<T>(method: string, params?: Record<string, unknown> | unknown[], signal?: AbortSignal): Promise<T> {
    if (this.useWebSocket) {
      return this.callWebSocket<T>(method, params, signal)
    }
    return this.callHttp<T>(method, params, signal)
  }

  /**
   * 切换传输方式
   */
  setTransport(useWebSocket: boolean): void {
    if (this.useWebSocket !== useWebSocket) {
      this.useWebSocket = useWebSocket
      if (!useWebSocket && this.ws) {
        this.ws.close()
        this.ws = null
      }
    }
  }

  /**
   * 确保 WebSocket 连接已建立
   */
  async ensureWebSocketConnected(): Promise<void> {
    await this.ensureWebSocketReady()
  }

  /**
   * 确保 WebSocket 连接已建立并通过 ping 验证
   */
  async ensureWebSocketConnectedWithPing(timeoutMs = 10000): Promise<void> {
    await this.ensureWebSocketReady()

    let timeoutId: ReturnType<typeof setTimeout> | null = null
    try {
      await Promise.race([
        this.callWebSocket<string>('rpc.ping'),
        new Promise<never>((_, reject) => {
          timeoutId = setTimeout(() => reject(new RpcError(-32001, 'WebSocket ping timeout')), timeoutMs)
        }),
      ])
    }
    catch (error) {
      this.close()
      throw error
    }
    finally {
      if (timeoutId)
        clearTimeout(timeoutId)
    }
  }

  /**
   * 关闭连接
   */
  close(): void {
    if (this.ws) {
      this.ws.close()
      this.ws = null
    }
    this.wsConnectPromise = null
    this.rejectPendingRequests(new RpcError(-32000, 'WebSocket closed'))
  }

  onWebSocketClose(listener: () => void): () => void {
    this.wsCloseListeners.add(listener)
    return () => this.wsCloseListeners.delete(listener)
  }

  /**
   * 获取 WebSocket 连接状态
   */
  getWsReadyState(): number {
    return this.ws?.readyState ?? WebSocket.CLOSED
  }

  /**
   * 获取 WebSocket 实例（用于状态监控）
   */
  getWebSocket(): WebSocket | null {
    return this.ws
  }
}

// ==================== KomariRpc 类 ====================

/**
 * Komari RPC 高级封装
 * 提供常用的 Komari API 方法
 */
export class KomariRpc {
  private client: RpcClient

  constructor(options: RpcClientOptions = {}) {
    this.client = new RpcClient(options)
  }

  /**
   * 获取底层 RpcClient 实例
   */
  getClient(): RpcClient {
    return this.client
  }

  // ==================== 内置方法 ====================

  /**
   * 获取所有可用方法
   */
  async getMethods(): Promise<string[]> {
    return this.client.call<string[]>('rpc.getMethods')
  }

  /**
   * 获取帮助信息
   */
  async getHelp(): Promise<MethodMeta[]> {
    return this.client.call<MethodMeta[]>('rpc.getHelp')
  }

  /**
   * Ping 测试
   */
  async ping(signal?: AbortSignal): Promise<string> {
    return this.client.call<string>('rpc.ping', undefined, signal)
  }

  /**
   * 获取版本信息
   */
  async getVersion(): Promise<VersionInfo> {
    return this.client.call<VersionInfo>('rpc.getVersion')
  }

  // ==================== 通用方法 ====================

  /**
   * 获取所有节点信息
   */
  async getNodes(): Promise<Record<string, Client>> {
    return this.client.call<Record<string, Client>>('common:getNodes')
  }

  /**
   * 获取所有节点最新状态
   */
  async getNodesLatestStatus(): Promise<Record<string, NodeStatus>> {
    return this.client.call<Record<string, NodeStatus>>('common:getNodesLatestStatus')
  }

  /**
   * 获取节点最近状态记录
   */
  async getNodeRecentStatus(uuid: string, limit?: number): Promise<{ count: number, records: StatusRecord[] }> {
    return this.client.call<{ count: number, records: StatusRecord[] }>('common:getNodeRecentStatus', { uuid, limit })
  }

  /**
   * 获取公开的站点信息
   */
  async getPublicInfo(): Promise<PublicInfo> {
    return this.client.call<PublicInfo>('common:getPublicInfo')
  }

  /**
   * 获取后端版本
   */
  async getBackendVersion(): Promise<VersionInfo> {
    return this.client.call<VersionInfo>('common:getBackendVersion')
  }

  // ==================== 历史记录方法 ====================

  /**
   * 获取历史记录（通用方法）
   */
  async getRecords(params: {
    type: 'load' | 'ping'
    uuid?: string
    hours?: number
    task_id?: number
    load_type?: string
    maxCount?: number
    max_count?: number
  }): Promise<unknown> {
    return this.client.call('common:getRecords', params)
  }

  /**
   * 获取负载记录
   */
  async getLoadRecords(uuid?: string, hours?: number, loadType?: string, maxCount?: number, signal?: AbortSignal): Promise<{ records: StatusRecord[] | Record<string, StatusRecord[]> }> {
    return this.client.call<{ records: StatusRecord[] | Record<string, StatusRecord[]> }>('common:getRecords', {
      type: 'load',
      uuid,
      hours,
      load_type: loadType,
      maxCount,
      max_count: maxCount,
    }, signal)
  }

  /**
   * 获取 Ping 记录
   */
  async getPingRecords(taskId?: number, hours?: number, maxCount?: number, signal?: AbortSignal, uuid?: string): Promise<{ records: PingRecord[], tasks?: PingTaskInfo[], basic_info?: Array<{ client: string, loss: number, min: number, max: number }> }> {
    return this.client.call<{ records: PingRecord[], tasks?: PingTaskInfo[], basic_info?: Array<{ client: string, loss: number, min: number, max: number }> }>('common:getRecords', {
      type: 'ping',
      uuid,
      task_id: taskId,
      hours,
      maxCount,
      max_count: maxCount,
    }, signal)
  }

  // ==================== Admin 方法（需登录权限） ====================

  async getAuditLogs(limit?: string, page?: string, msgType?: string, signal?: AbortSignal): Promise<AuditLogsResponse> {
    return this.client.call<AuditLogsResponse>('admin:getLogs', { limit, page, msg_type: msgType }, signal)
  }

  async updateAdminSettings(settings: Record<string, unknown>, signal?: AbortSignal): Promise<void> {
    await this.client.call('admin:editSettings', settings, signal)
  }

  // ==================== Public 方法（主题/公开页优先使用） ====================

  async getPublicMe(): Promise<unknown> {
    return this.client.call('public:getMe')
  }

  async getPublicNodesInformation(): Promise<Client[]> {
    return this.client.call<Client[]>('public:getNodesInformation')
  }

  async getPublicSettings(): Promise<PublicInfo> {
    return this.client.call<PublicInfo>('public:getPublicSettings')
  }

  async recordPublicVisitorEvent(params: VisitorAuditEventParams, signal?: AbortSignal): Promise<VisitorAuditEventResponse> {
    return this.client.call<VisitorAuditEventResponse>('public:recordVisitorEvent', params, signal)
  }

  async getPublicVersion(): Promise<VersionInfo> {
    return this.client.call<VersionInfo>('public:getVersion')
  }

  async getPublicClientRecentRecords(uuid: string, signal?: AbortSignal): Promise<StatusRecord[]> {
    return this.client.call<StatusRecord[]>('public:getClientRecentRecords', { uuid }, signal)
  }

  async getPublicRecordsByUUID(params: { uuid: string, load_type?: string, hours?: number | string }, signal?: AbortSignal): Promise<{ count: number, records: Array<Partial<StatusRecord>>, load_type?: string, has_gpu_data?: boolean, gpu_devices?: Record<string, unknown> }> {
    return this.client.call<{ count: number, records: Array<Partial<StatusRecord>>, load_type?: string, has_gpu_data?: boolean, gpu_devices?: Record<string, unknown> }>('public:getRecordsByUUID', params, signal)
  }

  async getPublicPingRecords(params: { uuid?: string, task_id?: string | number, hours?: number | string }, signal?: AbortSignal): Promise<{ count: number, records: PingRecord[], tasks?: PingTaskInfo[], basic_info?: Array<{ client: string, loss: number, min: number, max: number }> }> {
    return this.client.call<{ count: number, records: PingRecord[], tasks?: PingTaskInfo[], basic_info?: Array<{ client: string, loss: number, min: number, max: number }> }>('public:getPingRecords', params, signal)
  }

  async getPublicPingTasks(): Promise<PingTaskInfo[]> {
    return this.client.call<PingTaskInfo[]>('public:getPublicPingTasks')
  }

  async listPublicMetricDefinitions(): Promise<MetricDefinition[]> {
    return this.client.call<MetricDefinition[]>('public:listMetricDefinitions')
  }

  async queryPublicMetrics(params: MetricQueryParams, signal?: AbortSignal): Promise<MetricQueryResponse> {
    return this.client.call<MetricQueryResponse>('public:queryMetrics', params, signal)
  }

  async getPublicPingMetricStats(params: PingMetricStatsParams, signal?: AbortSignal): Promise<PingMetricStatsResponse> {
    return this.client.call<PingMetricStatsResponse>('public:getPingMetricStats', params, signal)
  }

  /**
   * 关闭连接
   */
  close(): void {
    this.client.close()
  }
}

// ==================== 单例 ====================

let sharedRpc: KomariRpc | null = null

/**
 * 获取共享的 KomariRpc 实例
 */
export function getSharedRpc(): KomariRpc {
  if (!sharedRpc) {
    sharedRpc = new KomariRpc()
  }
  return sharedRpc
}

/**
 * 重置共享实例
 */
export function resetSharedRpc(): void {
  if (sharedRpc) {
    sharedRpc.close()
    sharedRpc = null
  }
}
