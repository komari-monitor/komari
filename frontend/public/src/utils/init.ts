/**
 * 应用初始化模块
 * 负责应用启动时的初始化流程和 WebSocket 连接管理
 */

import type { Client, KomariRpc, NodeStatus } from '@/utils/rpc'
import { REALTIME_CONFIG } from '@/constants/realtime'
import { useAppStore } from '@/stores/app'
import { useNodesStore } from '@/stores/nodes'
import { getSharedApi } from '@/utils/api'
import { getSharedRpc, RpcError } from '@/utils/rpc'

/** 初始化配置 */
interface InitConfig {
  /** WebSocket 重连间隔（毫秒） */
  wsReconnectInterval?: number
  /** WebSocket 最大重连次数（失败后回落 POST） */
  wsMaxReconnectAttempts?: number
  /** 后端健康检查超时（毫秒） */
  healthCheckTimeout?: number
  /** 后端健康检查总尝试次数 */
  healthCheckAttempts?: number
  /** 后端健康检查重试基础间隔（毫秒） */
  healthCheckRetryInterval?: number
  /** POST 模式连续失败次数阈值 */
  postFailureThreshold?: number
}

const DEFAULT_CONFIG: Required<InitConfig> = {
  wsReconnectInterval: REALTIME_CONFIG.websocket.reconnectInterval,
  wsMaxReconnectAttempts: REALTIME_CONFIG.websocket.maxReconnectAttempts,
  healthCheckTimeout: REALTIME_CONFIG.websocket.healthCheckTimeout,
  healthCheckAttempts: REALTIME_CONFIG.websocket.healthCheckAttempts,
  healthCheckRetryInterval: REALTIME_CONFIG.websocket.healthCheckRetryInterval,
  postFailureThreshold: REALTIME_CONFIG.polling.postFailureThreshold,
}

const CLIENTS_REFRESH_INTERVAL_MS = REALTIME_CONFIG.polling.clientsRefreshInterval

/** 初始化状态管理 */
class InitManager {
  private config: Required<InitConfig>
  private rpc: KomariRpc
  private appStore: ReturnType<typeof useAppStore>
  private nodesStore: ReturnType<typeof useNodesStore>
  private pollTimer: ReturnType<typeof setTimeout> | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private unsubscribeWsClose: (() => void) | null = null
  private isPolling = false
  private destroyed = false
  private postFailureCount = 0
  private lastClientsFetchedAt = 0
  private isInitialized = false
  private redirectingToAdmin = false
  private useWebSocket: boolean | null = null // 根据主题配置决定
  constructor(config: InitConfig = {}) {
    this.config = { ...DEFAULT_CONFIG, ...config }
    this.rpc = getSharedRpc()
    this.appStore = useAppStore()
    this.nodesStore = useNodesStore()
  }

  /**
   * 获取轮询间隔（毫秒）
   * 从 publicSettings.theme_settings.dataUpdateInterval 读取，默认 3 秒
   */
  private getPollInterval(): number {
    return this.appStore.dataUpdateInterval * 1000
  }

  /**
   * 执行初始化流程
   */
  async init(): Promise<void> {
    this.destroyed = false

    if (this.isInitialized) {
      console.warn('[InitManager] Already initialized')
      return
    }

    try {
      await this.runStartupRequests()

      if (this.destroyed || this.redirectingToAdmin)
        return

      // 首次数据请求即使失败，也启动实时连接和轮询以便自动恢复。
      this.startWebSocketAndPolling()
      this.isInitialized = true
    }
    catch (error) {
      console.error('[InitManager] Initialization failed:', error)
      this.appStore.connectionError = true
      throw error
    }
    finally {
      // 即使部分请求失败也解除加载状态，让全局错误提示和公共页面可见。
      this.appStore.loading = false
    }
  }

  /**
   * 独立执行启动请求，避免任一请求失败阻断其他初始化任务。
   */
  private async runStartupRequests(): Promise<boolean> {
    const [healthResult, , , nodesResult] = await Promise.allSettled([
      this.healthCheck(),
      this.fetchPublicSettings(),
      this.fetchUserInfo(),
      this.fetchNodesData(),
    ])

    if (this.destroyed || this.redirectingToAdmin)
      return false

    const nodesAvailable = nodesResult.status === 'fulfilled'
    this.appStore.connectionError = !nodesAvailable

    if (nodesAvailable) {
      this.postFailureCount = 0
    }
    else if (healthResult.status === 'rejected') {
      console.error('[InitManager] Backend health check and initial node load both failed')
    }

    return nodesAvailable
  }

  /**
   * 重新执行启动请求，不重复创建轮询定时器或 WebSocket 监听。
   */
  async retry(): Promise<boolean> {
    if (this.destroyed || this.redirectingToAdmin)
      return false

    const recovered = await this.runStartupRequests()
    if (!this.isInitialized && !this.destroyed && !this.redirectingToAdmin) {
      this.startWebSocketAndPolling()
      this.isInitialized = true
    }
    return recovered
  }

  /**
   * 健康检查 - 测试后端服务是否正常
   */
  private async healthCheck(): Promise<void> {
    let lastError: unknown

    for (let attempt = 1; attempt <= this.config.healthCheckAttempts; attempt++) {
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), this.config.healthCheckTimeout)

      try {
        const result = await this.rpc.ping(controller.signal)
        if (result !== 'pong') {
          throw new RpcError(-32000, 'Unexpected health check response')
        }
        return
      }
      catch (error) {
        if (error instanceof RpcError && error.code === 401) {
          console.warn('[InitManager] Private site detected, redirecting to /admin')
          this.redirectingToAdmin = true
          this.appStore.updateLoginState(false)
          this.appStore.loading = false
          location.href = '/admin'
          return
        }

        lastError = error
        if (attempt < this.config.healthCheckAttempts && !this.destroyed) {
          const retryDelay = this.config.healthCheckRetryInterval * 2 ** (attempt - 1)
          console.warn(`[InitManager] Health check attempt ${attempt} failed, retrying in ${retryDelay}ms`, error)
          await new Promise(resolve => setTimeout(resolve, retryDelay))
        }
      }
      finally {
        clearTimeout(timeoutId)
      }
    }

    console.error('[InitManager] Health check failed after retries:', lastError)
    throw new Error('Backend service unavailable')
  }

  /**
   * 获取服务端公开属性
   */
  private async fetchPublicSettings(): Promise<void> {
    try {
      const api = getSharedApi()
      const publicSettings = await api.getPublicSettings()
      this.appStore.publicSettings = publicSettings
    }
    catch (error) {
      console.error('[InitManager] Failed to fetch public settings:', error)
      // 非关键错误，继续初始化
    }
  }

  /**
   * 获取用户信息
   */
  private async fetchUserInfo(): Promise<void> {
    try {
      const api = getSharedApi()
      const userInfo = await api.getMe()
      this.appStore.updateLoginState(userInfo.logged_in, userInfo)
    }
    catch (error) {
      this.appStore.updateLoginState(false)
      console.error('[InitManager] Failed to fetch user info:', error)
      // 非关键错误，继续初始化
    }
  }

  /**
   * 获取节点数据和最新状态
   */
  private async fetchNodesData(): Promise<void> {
    try {
      // 并行获取节点信息和最新状态
      const [clientsResult, statusesResult] = await Promise.all([
        this.rpc.getNodes() as Promise<Record<string, Client>>,
        this.rpc.getNodesLatestStatus() as Promise<Record<string, NodeStatus>>,
      ])

      // 初始化节点数据
      this.nodesStore.initNodes(clientsResult, statusesResult)
      this.lastClientsFetchedAt = Date.now()
    }
    catch (error) {
      console.error('[InitManager] Failed to fetch nodes data:', error)
      throw error
    }
  }

  /**
   * 启动 WebSocket 连接和轮询
   */
  private startWebSocketAndPolling(): void {
    // 根据主题配置决定初始连接模式
    const configuredMode = this.appStore.rpcTransportMode
    this.useWebSocket = configuredMode === 'websocket'

    if (this.useWebSocket) {
      // 尝试建立 WebSocket 连接
      this.connectWebSocket()
    }
    else {
      // HTTP 模式：直接设置 RPC 客户端为 HTTP 模式
      const client = this.rpc.getClient()
      client.setTransport(false)
      this.nodesStore.updateWsState('disconnected', this.config.wsMaxReconnectAttempts)
    }

    // 开始轮询（作为 WebSocket 的补充或备选方案）
    this.startPolling()
  }

  /**
   * 建立 WebSocket 连接
   */
  private async connectWebSocket(): Promise<void> {
    // 如果已回落到 POST 模式或配置为 HTTP 模式，不再尝试 WebSocket
    if (this.useWebSocket === false) {
      return
    }

    const client = this.rpc.getClient()

    // 切换到 WebSocket 模式
    client.setTransport(true)
    this.nodesStore.updateWsState('connecting', this.nodesStore.wsReconnectAttempts)

    try {
      // 使用 ping 验证连接，10 秒超时
      await client.ensureWebSocketConnectedWithPing(10000)
      this.nodesStore.updateWsState('connected', 0)

      // 连接成功，重置错误状态
      this.appStore.connectionError = false

      // 监听连接状态变化
      this.monitorWebSocketConnection()
    }
    catch (error) {
      console.error('[InitManager] WebSocket connection failed:', error)
      this.nodesStore.updateWsState('disconnected')
      this.scheduleReconnect()
    }
  }

  /**
   * 监控 WebSocket 连接状态
   */
  private monitorWebSocketConnection(): void {
    this.unsubscribeWsClose?.()
    const client = this.rpc.getClient()
    this.unsubscribeWsClose = client.onWebSocketClose(() => {
      if (this.destroyed)
        return
      // 如果当前是已连接状态且还在使用 WebSocket 模式，触发重连
      if (this.useWebSocket === true && this.nodesStore.wsConnectionState === 'connected') {
        this.nodesStore.updateWsState('disconnected')
        this.scheduleReconnect()
      }
    })
  }

  /**
   * 安排重连
   */
  private scheduleReconnect(): void {
    if (this.destroyed || this.useWebSocket === false || this.reconnectTimer)
      return

    const attempts = this.nodesStore.wsReconnectAttempts

    // 达到最大重连次数，回落到 POST 模式
    if (attempts >= this.config.wsMaxReconnectAttempts) {
      console.error('[InitManager] Max reconnect attempts reached, falling back to POST mode')
      this.fallbackToPostMode()
      return
    }

    // 首次失败时显示提示
    if (attempts === 0) {
      window.$message?.error('WebSocket 建立失败，正在尝试重连。')
    }

    const nextAttempts = attempts + 1
    this.nodesStore.updateWsState('reconnecting', nextAttempts)
    const backoff = Math.min(this.config.wsReconnectInterval * 2 ** Math.max(0, nextAttempts - 1), 30_000)

    this.reconnectTimer = setTimeout(async () => {
      this.reconnectTimer = null
      try {
        const client = this.rpc.getClient()
        client.close()
        await this.connectWebSocket()
      }
      catch (error) {
        console.error('[InitManager] Reconnect failed:', error)
        this.scheduleReconnect()
      }
    }, backoff)
  }

  /**
   * 回落到 POST 模式
   */
  private fallbackToPostMode(): void {
    this.useWebSocket = false
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.unsubscribeWsClose?.()
    this.unsubscribeWsClose = null
    this.nodesStore.updateWsState('disconnected', this.config.wsMaxReconnectAttempts)

    // 关闭 WebSocket 连接
    const client = this.rpc.getClient()
    client.setTransport(false)
    client.close()

    // 显示提示
    window.$message?.warning('WebSocket 无法连接，尝试回落 POST 模式。')
  }

  /**
   * 开始轮询
   */
  private startPolling(): void {
    if (this.pollTimer) {
      clearTimeout(this.pollTimer)
    }

    const schedulePoll = () => {
      this.pollTimer = setTimeout(async () => {
        await this.poll()
        if (!this.destroyed)
          schedulePoll()
      }, this.getPollInterval())
    }

    schedulePoll()
  }

  /**
   * 执行轮询任务
   */
  private async poll(): Promise<void> {
    if (this.isPolling) {
      return
    }

    this.isPolling = true

    try {
      const now = Date.now()
      const shouldRefreshClients = now - this.lastClientsFetchedAt >= CLIENTS_REFRESH_INTERVAL_MS

      const [statusesResult, clientsResult] = await Promise.all([
        this.rpc.getNodesLatestStatus() as Promise<Record<string, NodeStatus>>,
        shouldRefreshClients
          ? this.rpc.getNodes() as Promise<Record<string, Client>>
          : Promise.resolve(null),
      ])

      if (clientsResult) {
        this.nodesStore.updateNodeClients(clientsResult)
        this.lastClientsFetchedAt = now
      }

      this.nodesStore.updateNodeStatuses(statusesResult)

      // 连接恢复正常，重置错误状态
      this.postFailureCount = 0
      this.appStore.connectionError = false
    }
    catch (error) {
      if (error instanceof RpcError) {
        console.error('[InitManager] Poll RPC error:', error.message)
      }
      else {
        console.error('[InitManager] Poll error:', error)
      }

      this.postFailureCount += 1
      this.appStore.connectionError = this.postFailureCount >= this.config.postFailureThreshold
    }
    finally {
      this.isPolling = false
    }
  }

  /**
   * 停止轮询
   */
  stopPolling(): void {
    if (this.pollTimer) {
      clearTimeout(this.pollTimer)
      this.pollTimer = null
    }
  }

  /**
   * 销毁管理器
   */
  destroy(): void {
    this.destroyed = true
    this.stopPolling()
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.unsubscribeWsClose?.()
    this.unsubscribeWsClose = null
    this.rpc.close()
    this.nodesStore.clearNodes()
    this.isInitialized = false
  }
}

// 单例实例
let initManager: InitManager | null = null

/**
 * 初始化应用
 */
export async function initApp(): Promise<void> {
  if (!initManager) {
    initManager = new InitManager()
  }

  await initManager.init()
}

/**
 * 重试启动数据请求，不重复初始化实时连接。
 */
export async function retryInitApp(): Promise<boolean> {
  if (!initManager) {
    initManager = new InitManager()
  }

  return initManager.retry()
}

/**
 * 获取初始化管理器实例
 */
export function getInitManager(): InitManager | null {
  return initManager
}

/**
 * 销毁初始化管理器
 */
export function destroyInitManager(): void {
  if (initManager) {
    initManager.destroy()
    initManager = null
  }
}
