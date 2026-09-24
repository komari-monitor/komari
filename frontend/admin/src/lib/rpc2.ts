import type {
  JSONRPC2Request,
  JSONRPC2Response,
  JSONRPC2BatchRequest,
  JSONRPC2BatchResponse,
  RPC2ConnectionStateType,
  RPC2ConnectionOptions,
  RPC2CallOptions,
  RPC2EventListeners,
} from "../types/rpc2";
import { RPC2ConnectionState } from "../types/rpc2";
import i18n from "../i18n/config";

/**
 * RPC2 客户端类
 * 支持通过 WebSocket 和 HTTP POST 调用 JSON-RPC 2.0 接口
 */
export class RPC2Client {
  private ws: WebSocket | null = null;
  private connectionState: RPC2ConnectionStateType = RPC2ConnectionState.DISCONNECTED;
  private requestId = 0;
  private pendingRequests = new Map<string | number, {
    resolve: (value: any) => void;
    reject: (reason?: any) => void;
    timeout?: NodeJS.Timeout;
  }>();
  private reconnectAttempts = 0;
  private reconnectTimeout?: NodeJS.Timeout;
  private heartbeatInterval?: NodeJS.Timeout;
  private stableConnectionTimeout?: NodeJS.Timeout;
  private manualDisconnect = false;
  private connectionGeneration = 0;
  private eventListeners: RPC2EventListeners = {};

  // Do not reset the retry counter for a connection that immediately flaps.
  private readonly stableConnectionWindow = 10000;

  private readonly baseUrl: string;
  private readonly options: Required<RPC2ConnectionOptions>;

  constructor(
    baseUrl = "/api/rpc2",
    options: RPC2ConnectionOptions = {}
  ) {
    this.baseUrl = baseUrl;
    this.options = {
      autoConnect: true,
      autoReconnect: true,
      reconnectInterval: 3000,
      maxReconnectAttempts: 5,
      requestTimeout: 30000,
      enableHeartbeat: true,
      heartbeatInterval: 15000,
      headers: {
        "Content-Type": "application/json",
      },
      ...options,
    };

    // 自动建立连接
    if (this.options.autoConnect) {
      this.autoConnect();
    }
  }

  /**
   * 获取当前连接状态
   */
  get state(): RPC2ConnectionStateType {
    return this.connectionState;
  }

  /**
   * 设置事件监听器
   */
  setEventListeners(listeners: RPC2EventListeners): void {
    this.eventListeners = { ...this.eventListeners, ...listeners };
  }

  clearEventListeners(): void {
    this.eventListeners = {};
  }

  /**
   * 建立 WebSocket 连接
   */
  async connect(): Promise<void> {
    if (this.connectionState === RPC2ConnectionState.CONNECTED ||
        this.connectionState === RPC2ConnectionState.CONNECTING) {
      return;
    }

    this.manualDisconnect = false;
    const generation = ++this.connectionGeneration;
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = undefined;
    }
    this.setConnectionState(RPC2ConnectionState.CONNECTING);

    let socket: WebSocket | null = null;
    try {
      const wsUrl = this.getWebSocketUrl();
      socket = new WebSocket(wsUrl);
      this.ws = socket;
      this.setupWebSocketHandlers(socket);

      // 等待连接建立（不覆盖已设置的处理器，避免丢失心跳与状态更新）
      await new Promise<void>((resolve, reject) => {
        const handleOpen = () => {
          cleanup();
          resolve();
        };
        const handleError = () => {
          cleanup();
          reject(new Error(i18n.t("rpc2.websocket_connection_failed")));
        };
        const timeout = setTimeout(() => {
          cleanup();
          reject(new Error(i18n.t("rpc2.websocket_connection_timed_out")));
        }, 10000);

        const cleanup = () => {
          clearTimeout(timeout);
          socket?.removeEventListener("open", handleOpen);
          socket?.removeEventListener("error", handleError);
        };

        socket?.addEventListener("open", handleOpen, { once: true });
        socket?.addEventListener("error", handleError, { once: true });
      });
    } catch (error) {
      if (generation !== this.connectionGeneration) {
        socket?.close();
        throw error;
      }
      if (socket && this.ws === socket) {
        // A failed opening handshake does not always emit close in every
        // browser, so close and detach it here before scheduling a retry.
        this.ws = null;
        if (socket.readyState !== WebSocket.CLOSED) {
          socket.close();
        }
      } else if (socket) {
        socket.close();
      }
      this.stopHeartbeat();
      if (this.stableConnectionTimeout) {
        clearTimeout(this.stableConnectionTimeout);
        this.stableConnectionTimeout = undefined;
      }
      this.setConnectionState(RPC2ConnectionState.DISCONNECTED);
      this.eventListeners.onError?.(error as Error);
      if (!this.manualDisconnect && this.options.autoReconnect &&
          this.reconnectAttempts < this.options.maxReconnectAttempts) {
        this.attemptReconnect();
      }
      throw error;
    }
  }

  /**
   * 自动建立连接（非阻塞）
   */
  private autoConnect(): void {
    if (this.manualDisconnect || this.connectionState !== RPC2ConnectionState.DISCONNECTED) {
      return;
    }

    // 异步尝试连接，不阻塞构造函数
    this.connect().catch((error) => {
      console.warn(i18n.t("rpc2.automatic_connection_failed"), error.message);
      // 连接失败时，如果启用了自动重连，会在 onclose 处理器中进行重连
    });
  }

  /**
   * 断开 WebSocket 连接
   */
  disconnect(): void {
    this.manualDisconnect = true;
    this.connectionGeneration++;
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = undefined;
    }

    // 清理心跳包定时器
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = undefined;
    }

    if (this.stableConnectionTimeout) {
      clearTimeout(this.stableConnectionTimeout);
      this.stableConnectionTimeout = undefined;
    }

    const socket = this.ws;
    this.ws = null;
    if (socket) {
      socket.close();
    }

    this.setConnectionState(RPC2ConnectionState.DISCONNECTED);
    this.clearPendingRequests(new Error(i18n.t("rpc2.connection_disconnected")));
  }

  /**
   * 通过 WebSocket 调用 RPC 方法
   */
  async callViaWebSocket<TParams = any, TResult = any>(
    method: string,
    params?: TParams,
    options: RPC2CallOptions = {}
  ): Promise<TResult> {
    if (this.connectionState !== RPC2ConnectionState.CONNECTED) {
      throw new Error(i18n.t("rpc2.websocket_not_connected"));
    }

    const request: JSONRPC2Request<TParams> = {
      jsonrpc: "2.0",
      method,
      params,
      id: options.notification ? undefined : this.generateRequestId(),
    };

    if (options.notification) {
      // 通知请求，不期望响应
      this.sendMessage(request);
      return undefined as TResult;
    }

    return new Promise<TResult>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pendingRequests.delete(request.id!);
        reject(
          new Error(i18n.t("rpc2.request_timed_out", { method }))
        );
      }, options.timeout || this.options.requestTimeout);

      this.pendingRequests.set(request.id!, {
        resolve,
        reject,
        timeout,
      });

      try {
        this.sendMessage(request);
      } catch (error) {
        this.pendingRequests.delete(request.id!);
        clearTimeout(timeout);
        reject(error);
      }
    });
  }

  /**
   * 通过 HTTP POST 调用 RPC 方法
   */
  async callViaHTTP<TParams = any, TResult = any>(
    method: string,
    params?: TParams,
    options: RPC2CallOptions = {}
  ): Promise<TResult> {
    const request: JSONRPC2Request<TParams> = {
      jsonrpc: "2.0",
      method,
      params,
      id: options.notification ? undefined : this.generateRequestId(),
    };
    const requestAbort = this.createRequestAbort(
      options.timeout || this.options.requestTimeout,
    );

    try {
      const response = await fetch(this.baseUrl, {
        method: "POST",
        headers: this.options.headers,
        body: JSON.stringify(request),
        signal: requestAbort.signal,
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      if (options.notification) {
        return undefined as TResult;
      }

      const jsonResponse: JSONRPC2Response<TResult> = await response.json();

      if ("error" in jsonResponse) {
        throw new Error(`RPC Error ${jsonResponse.error.code}: ${jsonResponse.error.message}`);
      }

      return jsonResponse.result;
    } catch (error) {
      if (error instanceof Error) {
        throw error;
      }
      throw new Error(i18n.t("rpc2.request_failed", { method }));
    } finally {
      requestAbort.clear();
    }
  }

  /**
   * 批量调用（仅支持 HTTP）
   */
  async batchCall(requests: Array<{
    method: string;
    params?: any;
    notification?: boolean;
  }>): Promise<any[]> {
    const batchRequest: JSONRPC2BatchRequest = requests.map(req => ({
      jsonrpc: "2.0",
      method: req.method,
      params: req.params,
      id: req.notification ? undefined : this.generateRequestId(),
    }));
    const requestAbort = this.createRequestAbort(this.options.requestTimeout);

    try {
      const response = await fetch(this.baseUrl, {
        method: "POST",
        headers: this.options.headers,
        body: JSON.stringify(batchRequest),
        signal: requestAbort.signal,
      });

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const jsonResponse: JSONRPC2BatchResponse = await response.json();

      return jsonResponse.map(res => {
        if ("error" in res) {
          throw new Error(`RPC Error ${res.error.code}: ${res.error.message}`);
        }
        return res.result;
      });
    } catch (error) {
      if (error instanceof Error) {
        throw error;
      }
      throw new Error(i18n.t("rpc2.batch_request_failed"));
    } finally {
      requestAbort.clear();
    }
  }

  /**
   * AbortSignal.timeout() is unavailable in older browsers and WebViews.
   */
  private createRequestAbort(timeout: number): {
    signal: AbortSignal;
    clear: () => void;
  } {
    const timeoutSignal = AbortSignal as typeof AbortSignal & {
      timeout?: (milliseconds: number) => AbortSignal;
    };

    if (typeof timeoutSignal.timeout === "function") {
      return {
        signal: timeoutSignal.timeout(timeout),
        clear: () => undefined,
      };
    }

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), timeout);
    return {
      signal: controller.signal,
      clear: () => clearTimeout(timeoutId),
    };
  }

  /**
   * 自动选择调用方式（优先使用 WebSocket）
   */
  async call<TParams = any, TResult = any>(
    method: string,
    params?: TParams,
    options: RPC2CallOptions = {}
  ): Promise<TResult> {
    // 如果启用了自动连接，且当前未连接，尝试建立连接（不阻塞使用 HTTP 回退）
    if (this.options.autoConnect &&
        this.connectionState === RPC2ConnectionState.DISCONNECTED) {
      this.autoConnect();
    }

    // 策略：
    // 1) WS 已连接 → 尝试 WS；失败则回退一次 HTTP
    // 2) 其他状态（未连/连接中/重连中/错误）→ 直接 HTTP
    if (this.connectionState === RPC2ConnectionState.CONNECTED) {
      try {
        return await this.callViaWebSocket(method, params, options);
      } catch {
        // 回退一次 HTTP
        return this.callViaHTTP(method, params, options);
      }
    }

    // 未连或重连等情况下，直接使用 HTTP
    return this.callViaHTTP(method, params, options);
  }

  private getWebSocketUrl(): string {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const host = window.location.host;
    return `${protocol}//${host}${this.baseUrl}`;
  }

  private setupWebSocketHandlers(socket: WebSocket): void {
    socket.onopen = () => {
      if (this.ws !== socket) return;
      this.setConnectionState(RPC2ConnectionState.CONNECTED);
      if (this.stableConnectionTimeout) {
        clearTimeout(this.stableConnectionTimeout);
      }
      this.stableConnectionTimeout = setTimeout(() => {
        if (this.ws === socket && this.connectionState === RPC2ConnectionState.CONNECTED) {
          this.reconnectAttempts = 0;
        }
        this.stableConnectionTimeout = undefined;
      }, this.stableConnectionWindow);
      this.startHeartbeat(); // 启动心跳包
      this.eventListeners.onConnect?.();
    };

    socket.onmessage = (event) => {
      if (this.ws !== socket) return;
      try {
        const data = JSON.parse(event.data);
        this.handleMessage(data);
        this.eventListeners.onMessage?.(data);
      } catch (error) {
        console.error(i18n.t("rpc2.parse_websocket_message_failed"), error);
      }
    };

    socket.onclose = () => {
      if (this.ws !== socket) return;
      this.ws = null;
      this.setConnectionState(RPC2ConnectionState.DISCONNECTED);
      this.stopHeartbeat(); // 停止心跳包
      if (this.stableConnectionTimeout) {
        clearTimeout(this.stableConnectionTimeout);
        this.stableConnectionTimeout = undefined;
      }
      this.clearPendingRequests(new Error(i18n.t("rpc2.connection_disconnected")));
      this.eventListeners.onDisconnect?.();

      if (!this.manualDisconnect && this.options.autoReconnect &&
          this.reconnectAttempts < this.options.maxReconnectAttempts) {
        this.attemptReconnect();
      }
    };

    socket.onerror = (error) => {
      if (this.ws !== socket) return;
      console.error(i18n.t("rpc2.websocket_error"), error);
      this.eventListeners.onError?.(
        new Error(i18n.t("rpc2.websocket_connection_error"))
      );
    };
  }

  private handleMessage(data: JSONRPC2Response): void {
    if (data.id === undefined || data.id === null) return; // 忽略通知响应

    const pending = this.pendingRequests.get(data.id);
    if (!pending) return;

    this.pendingRequests.delete(data.id);

    if (pending.timeout) {
      clearTimeout(pending.timeout);
    }

    if ("error" in data) {
      pending.reject(new Error(`RPC Error ${data.error.code}: ${data.error.message}`));
    } else {
      pending.resolve(data.result);
    }
  }

  private sendMessage(message: JSONRPC2Request): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error(i18n.t("rpc2.websocket_not_connected"));
    }

    this.ws.send(JSON.stringify(message));
  }

  private setConnectionState(state: RPC2ConnectionStateType): void {
    this.connectionState = state;
  }

  private generateRequestId(): number {
    return ++this.requestId;
  }

  private clearPendingRequests(error: Error): void {
    for (const [, pending] of this.pendingRequests) {
      if (pending.timeout) {
        clearTimeout(pending.timeout);
      }
      pending.reject(error);
    }
    this.pendingRequests.clear();
  }

  /**
   * 启动心跳包
   */
  private startHeartbeat(): void {
    // 如果未启用心跳包，则不启动
    if (!this.options.enableHeartbeat) {
      return;
    }

    // 先清理之前的心跳包定时器
    this.stopHeartbeat();

    // 按配置的间隔发送心跳包
    this.heartbeatInterval = setInterval(() => {
      if (this.ws && this.ws.readyState === WebSocket.OPEN) {
        try {
          // 发送心跳包作为通知请求（不期望响应）
          const heartbeatRequest: JSONRPC2Request = {
            jsonrpc: "2.0",
            method: "rpc.ping",
            params: { timestamp: Date.now() }
          };
          this.ws.send(JSON.stringify(heartbeatRequest));
        } catch (error) {
          console.warn(i18n.t("rpc2.send_heartbeat_failed"), error);
        }
      }
    }, this.options.heartbeatInterval);
  }

  /**
   * 停止心跳包
   */
  private stopHeartbeat(): void {
    if (this.heartbeatInterval) {
      clearInterval(this.heartbeatInterval);
      this.heartbeatInterval = undefined;
    }
  }

  private attemptReconnect(): void {
    if (this.reconnectTimeout || this.manualDisconnect) return;
    this.reconnectAttempts++;
    this.setConnectionState(RPC2ConnectionState.RECONNECTING);
    this.eventListeners.onReconnecting?.(this.reconnectAttempts);

    this.reconnectTimeout = setTimeout(() => {
      this.reconnectTimeout = undefined;
      if (this.manualDisconnect) return;
      this.connect().catch(() => {
        // Failed handshakes are scheduled by connect() when no close event is emitted.
      });
    }, this.options.reconnectInterval);
  }
}

// 注意：避免在模块级别创建默认实例，以免在多处导入时重复建立 WebSocket 连接。
// 请通过 RPC2Provider + useRPC2Call/useRPC2 使用该客户端，或在需要的地方手动创建实例。
