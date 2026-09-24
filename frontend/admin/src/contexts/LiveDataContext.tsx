import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  LiveDataResponse,
  Record as LiveRecord,
} from "../types/LiveData";
import { useRPC2Call } from "./RPC2Context";

const LIVE_DATA_INTERVAL_MS = 2000;

const sameStringArray = (left: string[], right: string[]) =>
  left.length === right.length &&
  left.every((value, index) => value === right[index]);

const sameLiveRecord = (left: LiveRecord, right: LiveRecord) =>
  left.cpu.usage === right.cpu.usage &&
  left.ram.used === right.ram.used &&
  left.swap.used === right.swap.used &&
  left.load.load1 === right.load.load1 &&
  left.load.load5 === right.load.load5 &&
  left.load.load15 === right.load.load15 &&
  left.disk.used === right.disk.used &&
  left.network.up === right.network.up &&
  left.network.down === right.network.down &&
  left.network.totalUp === right.network.totalUp &&
  left.network.totalDown === right.network.totalDown &&
  left.connections.tcp === right.connections.tcp &&
  left.connections.udp === right.connections.udp &&
  left.gpu?.average_usage === right.gpu?.average_usage &&
  Boolean(left.gpu) === Boolean(right.gpu) &&
  left.uptime === right.uptime &&
  left.process === right.process &&
  left.message === right.message &&
  left.updated_at === right.updated_at;

const mergeLiveData = (
  result: Record<string, any>,
  previous: LiveDataResponse | null,
): LiveDataResponse => {
  const nextOnline = Object.values(result)
    .filter((value: any) => value?.online)
    .map((value: any) => value.client as string);
  const previousData = previous?.data;
  const online =
    previousData && sameStringArray(previousData.online, nextOnline)
      ? previousData.online
      : nextOnline;
  const data: Record<string, LiveRecord> = {};
  let changed = !previousData || online !== previousData.online;

  for (const [uuid, value] of Object.entries(result)) {
    const record = value as any;
    const nextRecord: LiveRecord = {
      cpu: { usage: typeof record.cpu === "number" ? record.cpu : 0 },
      ram: { used: record.ram ?? 0 },
      swap: { used: record.swap ?? 0 },
      load: {
        load1: record.load ?? 0,
        load5: record.load5 ?? 0,
        load15: record.load15 ?? 0,
      },
      disk: { used: record.disk ?? 0 },
      network: {
        up: record.net_out ?? 0,
        down: record.net_in ?? 0,
        totalUp: record.net_total_out ?? record.net_total_up ?? 0,
        totalDown: record.net_total_in ?? record.net_total_down ?? 0,
      },
      connections: {
        tcp: record.connections ?? 0,
        udp: record.connections_udp ?? 0,
      },
      gpu:
        record.gpu !== undefined
          ? { count: 0, average_usage: record.gpu, detailed_info: [] }
          : undefined,
      uptime: record.uptime ?? 0,
      process: record.process ?? 0,
      message: "",
      updated_at: record.time ?? 0,
    };
    const previousRecord = previousData?.data[uuid];
    if (previousRecord && sameLiveRecord(previousRecord, nextRecord)) {
      data[uuid] = previousRecord;
    } else {
      data[uuid] = nextRecord;
      changed = true;
    }
  }

  if (
    previousData &&
    Object.keys(previousData.data).length !== Object.keys(data).length
  ) {
    changed = true;
  }

  if (!changed && previous) return previous;
  return { data: { online, data }, status: "ok" };
};

// 创建Context
interface LiveDataContextType {
  live_data: LiveDataResponse | null;
  showCallout: boolean;
  onRefresh: (callback: (data: LiveDataResponse) => void) => () => void;
}

const LiveDataContext = createContext<LiveDataContextType>({
  live_data: null,
  showCallout: true,
  onRefresh: () => () => {},
});

// 创建Provider组件
export const LiveDataProvider: React.FC<{ children: React.ReactNode }> = ({
  children,
}) => {
  const [live_data, setLiveData] = useState<LiveDataResponse | null>(null);
  const liveDataRef = useRef<LiveDataResponse | null>(null);
  const [showCallout, setShowCallout] = useState(false);
  const refreshCallbacksRef = useRef<Set<(data: LiveDataResponse) => void>>(
    new Set(),
  );
  const { call } = useRPC2Call();

  // 注册刷新回调函数
  const onRefresh = useCallback((callback: (data: LiveDataResponse) => void) => {
    refreshCallbacksRef.current.add(callback);
    return () => {
      refreshCallbacksRef.current.delete(callback);
    };
  }, []);

  // 当数据更新时通知所有回调函数
  const notifyRefreshCallbacks = useCallback((data: LiveDataResponse) => {
    refreshCallbacksRef.current.forEach((callback) => callback(data));
  }, []);

  // 采用 RPC2 轮询最新状态，替代 WebSocket
  useEffect(() => {
    let timer: number | undefined;
    let stopped = false;
    let running = false; // 防抖：避免并发请求
    const refreshCallbacks = refreshCallbacksRef.current;

    const clearTimer = () => {
      if (timer !== undefined) {
        window.clearTimeout(timer);
        timer = undefined;
      }
    };

    const scheduleNext = () => {
      clearTimer();
      if (!stopped && !document.hidden) {
        timer = window.setTimeout(fetchLatest, LIVE_DATA_INTERVAL_MS);
      }
    };

    const fetchLatest = async () => {
      if (running || stopped || document.hidden) return;
      running = true;
      try {
        // 策略由 RPC2Client 内部实现
        const result: Record<string, any> = await call(
          "common:getNodesLatestStatus",
        );
        if (stopped) return;
        const live = mergeLiveData(result, liveDataRef.current);
        if (live !== liveDataRef.current) {
          liveDataRef.current = live;
          setLiveData(live);
          notifyRefreshCallbacks(live);
        }
        setShowCallout(true);
      } catch (e) {
        if (stopped) return;
        console.error("RPC2 获取最新状态失败:", e);
        setShowCallout(false);
      } finally {
        running = false;
        scheduleNext();
      }
    };

    const handleVisibilityChange = () => {
      if (document.hidden) {
        clearTimer();
      } else if (!running) {
        void fetchLatest();
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    if (!document.hidden) void fetchLatest();

    return () => {
      stopped = true;
      refreshCallbacks.clear();
      clearTimer();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [call, notifyRefreshCallbacks]);

  const contextValue = useMemo(
    () => ({ live_data, showCallout, onRefresh }),
    [live_data, showCallout, onRefresh],
  );

  return (
    <LiveDataContext.Provider value={contextValue}>
      {children}
    </LiveDataContext.Provider>
  );
};

export const useLiveData = () => useContext(LiveDataContext);

export default LiveDataContext;
