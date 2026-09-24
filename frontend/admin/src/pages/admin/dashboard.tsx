import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Badge,
  Button,
  Card,
  Flex,
  IconButton,
  Popover,
  Separator,
  Text,
} from "@radix-ui/themes";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts";
import { useNodeList, type NodeBasicInfo } from "@/contexts/NodeListContext";
import { useRPC2Call } from "@/contexts/RPC2Context";
import { formatBytes } from "@/utils/unitHelper";
import Loading from "@/components/loading";
import Tips from "@/components/ui/tips";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import {
  CalendarClock,
  ChartNoAxesCombined,
  Cpu,
  Database,
  Gauge,
  List,
  MemoryStick,
  RefreshCw,
} from "lucide-react";
import { toast } from "sonner";
import type {
  MetricSeries,
  MetricTags,
  PingMetricStat,
  PingMetricStatsResponse,
  PublicPingTask,
  QueryMetricsResponse,
} from "@/types/metrics";
import {
  PING_LATENCY_METRIC,
  metricSeriesColor,
  normalizeMetricSeriesList,
  pingMetricStatKey,
  pingTaskId,
  pingTaskName,
} from "@/utils/metricSeries";

const formatSpeed = (bytes: number): string => {
  if (bytes === 0) return "0 B/s";
  const units = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const size = bytes / Math.pow(1024, i);
  let decimals = 2;
  if (i >= 3) decimals = 1;
  if (i <= 1) decimals = 0;
  if (size >= 100) decimals = 0;
  return `${size.toFixed(decimals)} ${units[i]}`;
};

const weightedP95 = (
  points: { value: number; count?: number }[],
): number | null => {
  const valid = points.filter(
    (point) =>
      Number.isFinite(point.value) &&
      point.value >= 0 &&
      (point.count ?? 1) > 0,
  );
  if (valid.length === 0) return null;
  const total = valid.reduce((sum, point) => sum + (point.count ?? 1), 0);
  const sorted = [...valid].sort((a, b) => a.value - b.value);
  let cumulative = 0;
  for (const point of sorted) {
    cumulative += point.count ?? 1;
    if (cumulative >= total * 0.95) return point.value;
  }
  return sorted[sorted.length - 1].value;
};

const weightedAverage = (
  points: { value: number; count?: number }[],
): number | null => {
  const valid = points.filter(
    (point) => Number.isFinite(point.value) && (point.count ?? 1) > 0,
  );
  if (valid.length === 0) return null;
  const totalCount = valid.reduce(
    (sum, point) => sum + (point.count ?? 1),
    0,
  );
  return (
    valid.reduce(
      (sum, point) => sum + point.value * (point.count ?? 1),
      0,
    ) / totalCount
  );
};

const formatPeakTime = (t: TFunction, timestamp: number): string => {
  const date = new Date(timestamp);
  const now = new Date();
  const startOfToday = new Date(
    now.getFullYear(),
    now.getMonth(),
    now.getDate(),
  ).getTime();
  const time = date.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
  if (timestamp >= startOfToday) {
    return `${t("dashboard.today", "Today")} ${time}`;
  }
  if (timestamp >= startOfToday - DAY_MS) {
    return `${t("dashboard.yesterday", "Yesterday")} ${time}`;
  }
  return `${date.toLocaleDateString()} ${time}`;
};

const EXPIRING_SOON_DAYS = 7;
const DAY_MS = 24 * 3600 * 1000;

const latencyColor = (ms: number): "green" | "yellow" | "red" =>
  ms < 100 ? "green" : ms <= 280 ? "yellow" : "red";

const volatilityColor = (value: number): "green" | "yellow" | "red" =>
  value < 0.3 ? "green" : value <= 1 ? "yellow" : "red";

// 与后端 utils/renewal 保持一致：
// 27-32 按自然月 +1 月，87-95 +3 月，175-185 +6 月，
// 360-370 +1 年，720-750 +2 年，1080-1150 +3 年，1800-1850 +5 年，其余 +天数。
const computeRenewalDate = (
  expiredAt: Date,
  billingCycle: number,
): Date | null => {
  if (!billingCycle || billingCycle <= 0) return null;
  const now = new Date();
  let base = new Date(expiredAt);
  if (expiredAt.getTime() < now.getTime() - 30 * DAY_MS) {
    base = now;
  }
  const result = new Date(base);
  if (billingCycle >= 27 && billingCycle <= 32) {
    result.setMonth(result.getMonth() + 1);
  } else if (billingCycle >= 87 && billingCycle <= 95) {
    result.setMonth(result.getMonth() + 3);
  } else if (billingCycle >= 175 && billingCycle <= 185) {
    result.setMonth(result.getMonth() + 6);
  } else if (billingCycle >= 360 && billingCycle <= 370) {
    result.setFullYear(result.getFullYear() + 1);
  } else if (billingCycle >= 720 && billingCycle <= 750) {
    result.setFullYear(result.getFullYear() + 2);
  } else if (billingCycle >= 1080 && billingCycle <= 1150) {
    result.setFullYear(result.getFullYear() + 3);
  } else if (billingCycle >= 1800 && billingCycle <= 1850) {
    result.setFullYear(result.getFullYear() + 5);
  } else {
    result.setDate(result.getDate() + billingCycle);
  }
  return result;
};

type TopRankItem = {
  uuid: string;
  name: string;
  value: number;
  peak: number;
  peakTime: number;
};

type TrafficNodeTotals = {
  uuid: string;
  up: number;
  down: number;
  total: number;
  peakRate: number;
  peakTime: number;
};

type PingRankItem = {
  key: string;
  entityId: string;
  taskId: string;
  label: string;
  p95: number | null;
  volatility: number;
  loss: number;
  valid: number;
};

const CPU_METRIC_KEYS = ["cpu.usage"];
const MEM_METRIC_KEYS = ["memory.used"];
const NET_METRIC_KEYS = ["net.in.rate", "net.out.rate"];
const NET_TOTAL_METRIC_KEYS = ["net.total.up", "net.total.down"];
const PING_METRIC_KEYS = [PING_LATENCY_METRIC];

// 首页所有指标卡共用一个 24h 查询（流量/CPU/内存/延迟），
// 这里从响应中分别派生流量汇总与 TOP p95 排行。
type TrafficSummary = {
  points: {
    time: number;
    upRate: number;
    downRate: number;
    upCum: number;
    downCum: number;
  }[];
  nodeTotals: TrafficNodeTotals[];
  totalUp: number;
  totalDown: number;
};

const computeTrafficSummary = (
  res: QueryMetricsResponse | null,
): TrafficSummary | null => {
  if (!res) return null;

  // The first cumulative counter after a collection gap/reset is a baseline,
  // not traffic observed during that chart bucket.
  const discontinuities = new Set<string>();
  for (const series of res.series ?? []) {
    if (!NET_TOTAL_METRIC_KEYS.includes(series.metric_key)) continue;
    const direction = series.metric_key === "net.total.up" ? "up" : "down";
    let previousValue: number | null = null;
    let gapAfterValue = false;
    let reboundBaseline: number | null = null;
    for (const point of series.points ?? []) {
      if (point.value == null) {
        if (previousValue !== null) gapAfterValue = true;
        continue;
      }
      const ts = new Date(point.time).getTime();
      let discontinuity = gapAfterValue;
      if (previousValue !== null && point.value < previousValue) {
        discontinuity = true;
        reboundBaseline = previousValue;
      } else if (reboundBaseline !== null) {
        if (point.value >= reboundBaseline) discontinuity = true;
        reboundBaseline = null;
      }
      if (discontinuity) {
        discontinuities.add(`${series.entity_id}\0${direction}\0${ts}`);
      }
      previousValue = point.value;
      gapAfterValue = false;
    }
  }

  const byTime = new Map<
    number,
    { upRate: number; downRate: number; upDelta: number; downDelta: number }
  >();
  const byEntity = new Map<string, { up: number; down: number }>();
  const byEntityRate = new Map<
    string,
    Map<number, { up: number; down: number }>
  >();
  for (const series of res.series ?? []) {
    const isRate =
      series.metric_key === "net.in.rate" ||
      series.metric_key === "net.out.rate";
    const isUp =
      series.metric_key === "net.out.rate" ||
      series.metric_key === "traffic.up";
    if (
      !isRate &&
      series.metric_key !== "traffic.up" &&
      series.metric_key !== "traffic.down"
    ) {
      continue;
    }
    const entity = series.entity_id;
    for (const point of series.points ?? []) {
      if (point.value == null) continue;
      const ts = new Date(point.time).getTime();
      const entry =
        byTime.get(ts) ?? { upRate: 0, downRate: 0, upDelta: 0, downDelta: 0 };
      if (isRate) {
        if (isUp) entry.upRate += point.value;
        else entry.downRate += point.value;
        const rateMap = byEntityRate.get(entity) ?? new Map();
        const rateEntry = rateMap.get(ts) ?? { up: 0, down: 0 };
        if (isUp) rateEntry.up += point.value;
        else rateEntry.down += point.value;
        rateMap.set(ts, rateEntry);
        byEntityRate.set(entity, rateMap);
      } else if (isUp) {
        if (discontinuities.has(`${entity}\0up\0${ts}`)) continue;
        entry.upDelta += point.value;
      } else {
        if (discontinuities.has(`${entity}\0down\0${ts}`)) continue;
        entry.downDelta += point.value;
      }
      byTime.set(ts, entry);
      if (!isRate) {
        const entityEntry = byEntity.get(entity) ?? { up: 0, down: 0 };
        if (isUp) entityEntry.up += point.value;
        else entityEntry.down += point.value;
        byEntity.set(entity, entityEntry);
      }
    }
  }
  const rate = Array.from(byTime.entries())
    .map(([time, value]) => ({ time, ...value }))
    .sort((a, b) => a.time - b.time);
  const points: TrafficSummary["points"] = [];
  let totalUp = 0;
  let totalDown = 0;
  for (const point of rate) {
    totalUp += point.upDelta;
    totalDown += point.downDelta;
    points.push({
      time: point.time,
      upRate: point.upRate,
      downRate: point.downRate,
      upCum: totalUp,
      downCum: totalDown,
    });
  }
  const nodeTotals: TrafficNodeTotals[] = Array.from(byEntity.entries())
    .map(([uuid, value]) => {
      let peakRate = 0;
      let peakTime = 0;
      for (const [ts, rateEntry] of byEntityRate.get(uuid) ?? []) {
        const combined = rateEntry.up + rateEntry.down;
        if (combined > peakRate) {
          peakRate = combined;
          peakTime = ts;
        }
      }
      return {
        uuid,
        up: value.up,
        down: value.down,
        total: value.up + value.down,
        peakRate,
        peakTime,
      };
    })
    .sort((a, b) => b.total - a.total);
  return { points, nodeTotals, totalUp, totalDown };
};

const computeTopAverageItems = (
  res: QueryMetricsResponse | null,
  metricKey: string,
  nodeNameMap: Map<string, string>,
  toPercent: (uuid: string, value: number) => number,
): TopRankItem[] => {
  if (!res) return [];
  const bucket = new Map<
    string,
    { values: { value: number; count?: number }[]; peak: number; peakTime: number }
  >();
  for (const series of res.series ?? []) {
    if (series.metric_key !== metricKey) continue;
    const entry =
      bucket.get(series.entity_id) ?? { values: [], peak: 0, peakTime: 0 };
    for (const point of series.points ?? []) {
      if (point.value == null) continue;
      entry.values.push({ value: point.value, count: point.count });
      if (point.value > entry.peak) {
        entry.peak = point.value;
        entry.peakTime = new Date(point.time).getTime();
      }
    }
    bucket.set(series.entity_id, entry);
  }
  const items: TopRankItem[] = [];
  for (const [uuid, entry] of bucket) {
    const average = weightedAverage(entry.values);
    if (average == null) continue;
    const value = toPercent(uuid, average);
    if (!Number.isFinite(value)) continue;
    items.push({
      uuid,
      name: nodeNameMap.get(uuid) ?? uuid.slice(0, 8),
      value,
      peak: toPercent(uuid, entry.peak),
      peakTime: entry.peakTime,
    });
  }
  items.sort((a, b) => b.value - a.value);
  return items;
};

const miniChartCache = new Map<string, MetricSeries[]>();

const Dashboard = () => {
  return <DashboardContent />;
};

const DashboardContent = () => {
  const { t } = useTranslation();
  const { nodeList, isLoading, error, refresh } = useNodeList();
  const { call } = useRPC2Call();

  const [latest, setLatest] = useState<Record<string, any> | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [dbInfo, setDbInfo] = useState<{
    main: number | null;
    monitoring: number | null;
  } | null>(null);
  const [metricsRes, setMetricsRes] = useState<QueryMetricsResponse | null>(
    null,
  );
  const [pingStats, setPingStats] = useState<PingMetricStat[]>([]);
  const [pingTasks, setPingTasks] = useState<PublicPingTask[]>([]);
  const [renewingUuid, setRenewingUuid] = useState<string | null>(null);
  const [renewedUuids, setRenewedUuids] = useState<Set<string>>(new Set());

  const onlineSet = useMemo(() => {
    const out = new Set<string>();
    if (latest) {
      for (const [uuid, value] of Object.entries(latest)) {
        if ((value as any)?.online) out.add(uuid);
      }
    }
    return out;
  }, [latest]);

  const stats = useMemo(() => {
    const nodes = nodeList ?? [];
    const online = onlineSet.size;
    const total = nodes.length;
    return {
      total,
      online,
      offline: total - online,
      onlineRate: total ? (online / total) * 100 : 0,
    };
  }, [nodeList, onlineSet]);

  const offlineNodes = useMemo(
    () => (nodeList ?? []).filter((node) => !onlineSet.has(node.uuid)),
    [nodeList, onlineSet],
  );

  const nodeNameMap = useMemo(
    () => new Map((nodeList ?? []).map((node) => [node.uuid, node.name])),
    [nodeList],
  );

  const memTotalMap = useMemo(
    () => new Map((nodeList ?? []).map((node) => [node.uuid, node.mem_total])),
    [nodeList],
  );

  const expiringNodes = useMemo(() => {
    const now = Date.now();
    const deadline = now + EXPIRING_SOON_DAYS * DAY_MS;
    return (nodeList ?? [])
      .filter((node) => {
        if (!node.expired_at) return false;
        if (renewedUuids.has(node.uuid)) return false;
        const ts = new Date(node.expired_at).getTime();
        return ts >= now && ts <= deadline;
      })
      .sort(
        (a, b) =>
          new Date(a.expired_at).getTime() - new Date(b.expired_at).getTime(),
      );
  }, [nodeList, renewedUuids]);

  const fetchLatest = useCallback(async () => {
    try {
      const result = await call<unknown, Record<string, any>>(
        "common:getNodesLatestStatus",
      );
      setLatest(result ?? null);
    } catch (e) {
      console.error("Failed to fetch latest status:", e);
    }
  }, [call]);

  const fetchMetrics = useCallback(async () => {
    const now = new Date();
    const start = new Date(now.getTime() - 24 * 3600 * 1000);
    try {
      const res = await call<any, QueryMetricsResponse>("public:queryMetrics", {
        metric_keys: [
          ...NET_METRIC_KEYS,
          ...NET_TOTAL_METRIC_KEYS,
          "traffic.up",
          "traffic.down",
          ...CPU_METRIC_KEYS,
          ...MEM_METRIC_KEYS,
          PING_LATENCY_METRIC,
        ],
        start: start.toISOString(),
        end: now.toISOString(),
        aggregation: "p95",
        aggregation_by_metric: {
          "traffic.up": "sum",
          "traffic.down": "sum",
          "net.total.up": "last",
          "net.total.down": "last",
          "cpu.usage": "avg",
          "memory.used": "avg",
        },
        fill_empty: true,
      });
      setMetricsRes(res ?? null);
    } catch (e) {
      console.error("Failed to fetch dashboard metrics:", e);
    }
  }, [call]);

  const fetchDbSize = useCallback(async () => {
    try {
      const res = await fetch("/api/admin/database/size");
      const data = await res.json();
      const payload = data?.data;
      setDbInfo({
        main: payload?.main?.size ?? null,
        monitoring: payload?.monitoring?.size ?? null,
      });
    } catch (e) {
      console.error("Failed to fetch database size:", e);
    }
  }, []);

  const fetchPingStats = useCallback(async () => {
    try {
      const [statsRes, tasksRes] = await Promise.all([
        call<unknown, PingMetricStatsResponse>("public:getPingMetricStats", {
          hours: 24,
        }),
        call<unknown, PublicPingTask[]>("public:getPublicPingTasks").catch(
          () => [],
        ),
      ]);
      setPingStats(Array.isArray(statsRes?.stats) ? statsRes.stats : []);
      setPingTasks(Array.isArray(tasksRes) ? tasksRes : []);
    } catch (e) {
      console.error("Failed to fetch ping stats:", e);
    }
  }, [call]);

  const fetchAll = useCallback(async () => {
    setRefreshing(true);
    miniChartCache.clear();
    try {
      await Promise.allSettled([
        refresh(),
        fetchLatest(),
        fetchMetrics(),
        fetchDbSize(),
        fetchPingStats(),
      ]);
    } finally {
      setRefreshing(false);
    }
  }, [refresh, fetchLatest, fetchMetrics, fetchDbSize, fetchPingStats]);

  useEffect(() => {
    void fetchAll();
  }, [fetchAll]);

  // 由一次 queryMetrics 响应派生各指标卡数据；nodeList 就绪后
  // nodeNameMap/memTotalMap 变化会自动重算，无需再次请求。
  const traffic = useMemo(() => computeTrafficSummary(metricsRes), [metricsRes]);

  const topCpu = useMemo<TopRankItem[]>(
    () =>
      computeTopAverageItems(
        metricsRes,
        CPU_METRIC_KEYS[0],
        nodeNameMap,
        (_uuid, value) => value,
      ),
    [metricsRes, nodeNameMap],
  );

  const topMem = useMemo<TopRankItem[]>(
    () =>
      computeTopAverageItems(
        metricsRes,
        MEM_METRIC_KEYS[0],
        nodeNameMap,
        (uuid, value) => {
          const totalBytes = memTotalMap.get(uuid) ?? 0;
          return totalBytes > 0 ? (value / totalBytes) * 100 : 0;
        },
      ),
    [metricsRes, nodeNameMap, memTotalMap],
  );

  const handleRenew = async (node: NodeBasicInfo) => {
    const expiry = computeRenewalDate(
      new Date(node.expired_at),
      node.billing_cycle,
    );
    if (!expiry) {
      toast.error(t("dashboard.renewNotSupported", "No billing cycle"));
      return;
    }
    setRenewingUuid(node.uuid);
    try {
      const res = await fetch(`/api/admin/client/${node.uuid}/edit`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          uuid: node.uuid,
          expired_at: expiry.toISOString(),
        }),
      });
      if (res.ok) {
        toast.success(
          t("dashboard.renewSuccess", "Renewed. New expiry: {{date}}", {
            date: expiry.toLocaleDateString(),
          }),
        );
        setRenewedUuids((prev) => new Set(prev).add(node.uuid));
      } else {
        toast.error(t("dashboard.renewFailed", "Renewal failed"));
      }
    } catch {
      toast.error(t("dashboard.renewFailed", "Renewal failed"));
    } finally {
      setRenewingUuid(null);
    }
  };

  const health = useMemo(() => {
    if (stats.total === 0) {
      return { level: "empty" as const, color: "gray" as const };
    }
    if (stats.onlineRate >= 95) {
      return { level: "healthy" as const, color: "green" as const };
    }
    if (stats.onlineRate >= 75) {
      return { level: "warning" as const, color: "orange" as const };
    }
    return { level: "danger" as const, color: "red" as const };
  }, [stats.total, stats.onlineRate]);

  const healthDesc = {
    empty: t("dashboard.health.emptyDesc", "No servers have been added yet."),
    healthy: t(
      "dashboard.health.healthyDesc",
      "All servers are online and healthy.",
    ),
    warning: t(
      "dashboard.health.warningDesc",
      "Some servers are offline, please check.",
    ),
    danger: t(
      "dashboard.health.dangerDesc",
      "Most servers are offline, cluster is abnormal.",
    ),
  };

  const chartConfig = {
    upRate: {
      label: t("dashboard.uploadRate", "Upload rate"),
      color: "var(--green-9)",
    },
    downRate: {
      label: t("dashboard.downloadRate", "Download rate"),
      color: "var(--blue-9)",
    },
    upCum: {
      label: t("dashboard.uploadTotal", "Upload cumulative"),
      color: "var(--green-9)",
    },
    downCum: {
      label: t("dashboard.downloadTotal", "Download cumulative"),
      color: "var(--blue-9)",
    },
  } satisfies ChartConfig;

  const pingP95Map = useMemo(() => {
    const map = new Map<string, number>();
    for (const series of metricsRes?.series ?? []) {
      const taskId = pingTaskId(series.tags);
      if (!taskId) continue;
      const p95 = weightedP95(
        (series.points ?? []).map((point) => ({
          value: point.value ?? NaN,
          count: point.count,
        })),
      );
      if (p95 != null) {
        map.set(pingMetricStatKey(series.entity_id, taskId), p95);
      }
    }
    return map;
  }, [metricsRes]);

  const pingRankItems = useMemo(() => {
    const taskMap = new Map(
      pingTasks.map((task) => [String(task.id), task]),
    );
    return pingStats.map((stat) => {
      const taskName = pingTaskName(
        stat.task_id,
        taskMap,
        (id) => `${t("ping.task", "Ping task")} ${id}`,
      );
      const nodeName =
        nodeNameMap.get(stat.entity_id) ?? stat.entity_id.slice(0, 8);
      const p95 =
        pingP95Map.get(pingMetricStatKey(stat.entity_id, stat.task_id)) ?? null;
      return {
        key: pingMetricStatKey(stat.entity_id, stat.task_id),
        entityId: stat.entity_id,
        taskId: stat.task_id,
        label: `${nodeName} · ${taskName}`,
        p95,
        volatility: stat.p99_p50_ratio ?? 0,
        loss: stat.loss ?? 0,
        valid: stat.valid,
      } satisfies PingRankItem;
    });
  }, [pingStats, pingP95Map, pingTasks, nodeNameMap, t]);

  // 无有效延迟样本(如 100% 丢包)的节点波动无意义，不参与稳定性排名
  const stableLatencyItems = useMemo(
    () =>
      [...pingRankItems]
        .filter((item) => item.valid > 0)
        .sort((a, b) => a.volatility - b.volatility),
    [pingRankItems],
  );

  const unstableLatencyItems = useMemo(
    () =>
      [...pingRankItems]
        .filter((item) => item.valid > 0)
        .sort((a, b) => b.volatility - a.volatility),
    [pingRankItems],
  );

  const highestLossItems = useMemo(
    () => [...pingRankItems].sort((a, b) => b.loss - a.loss),
    [pingRankItems],
  );

  const renderLatencyColumn = (
    title: string,
    items: PingRankItem[],
    renderValue: (item: PingRankItem) => React.ReactNode,
  ) => (
    <Flex direction="column" gap="2" className="flex-1 min-w-56">
      <Flex justify="between" align="center" gap="2">
        <Text size="2" weight="bold">
          {title}
        </Text>
        <RankListPopover
          title={title}
          ariaLabel={t("common.details", "Details")}
        >
          {items.length === 0 ? (
            <Text size="2" color="gray">
              {t("dashboard.noData", "No data")}
            </Text>
          ) : (
            <Flex direction="column" gap="2">
              {items.map((item, index) => (
                <Flex key={item.key} justify="between" align="center" gap="2">
                  <Text size="2" className="truncate" title={item.label}>
                    <Text size="2" color="gray">
                      {index + 1}.
                    </Text>{" "}
                    {item.label}
                  </Text>
                  <Flex align="center" gap="1" className="shrink-0">
                    {renderValue(item)}
                    <MiniChartButton
                      uuid={item.entityId}
                      metricKeys={PING_METRIC_KEYS}
                      tags={{ task_id: item.taskId }}
                      ariaLabel={t("dashboard.viewChart", "View 24h chart")}
                    />
                  </Flex>
                </Flex>
              ))}
            </Flex>
          )}
        </RankListPopover>
      </Flex>
      {items.length === 0 ? (
        <Text size="2" color="gray">
          {t("dashboard.noData", "No data")}
        </Text>
      ) : (
        <Flex direction="column" gap="2">
          {items.slice(0, 3).map((item, index) => (
            <Flex key={item.key} justify="between" align="center" gap="2">
              <Text size="2" className="truncate" title={item.label}>
                <Text size="2" color="gray">
                  {index + 1}.
                </Text>{" "}
                {item.label}
              </Text>
              <Flex align="center" gap="1" className="shrink-0">
                {renderValue(item)}
                <MiniChartButton
                  uuid={item.entityId}
                  metricKeys={PING_METRIC_KEYS}
                  tags={{ task_id: item.taskId }}
                  ariaLabel={t("dashboard.viewChart", "View 24h chart")}
                />
              </Flex>
            </Flex>
          ))}
        </Flex>
      )}
    </Flex>
  );

  const renderLatencyValue = (item: PingRankItem) => (
    <Text size="2" className="whitespace-nowrap">
      <Text
        size="2"
        color={item.p95 != null ? latencyColor(item.p95) : "gray"}
      >
        {item.p95 != null ? `${Math.round(item.p95)} ms` : "-"}
      </Text>{" "}
      ·{" "}
      <Text size="2" color={volatilityColor(item.volatility)}>
        {t("chart.volatility", "Volatility")} {item.volatility.toFixed(2)}
      </Text>
    </Text>
  );

  if (isLoading) return <Loading text="" />;
  if (error) return <div>{error}</div>;

  return (
    <Flex direction="column" gap="4" p="4" className="km-page-admin-dashboard">
      <Flex justify="between" align="center" wrap="wrap" gap="2">
        <Flex direction="column" gap="1">
          <Text size="5" weight="bold">
            {t("dashboard.title", "Dashboard")}
          </Text>
          <Text size="2" color="gray">
            {t(
              "dashboard.greeting",
              "May every server run smoothly and everything is under control.",
            )}
          </Text>
        </Flex>
        <Button
          size="1"
          variant="soft"
          disabled={refreshing}
          onClick={() => void fetchAll()}
        >
          <RefreshCw size={14} />
          {t("common.refresh", "Refresh")}
        </Button>
      </Flex>

      <Flex gap="4" wrap="wrap">
        <Card className="km-dashboard-card flex-1 min-w-72">
          <Flex gap="4" align="center">
            <ProgressRing
              percent={stats.onlineRate}
              color={health.color}
              ariaLabel={t(
                "dashboard.onlineRateAria",
                "{{percent}}% of servers online",
                {
                  percent: stats.onlineRate.toFixed(0),
                },
              )}
            />
            <Flex direction="column" gap="2" style={{ minWidth: 0 }}>
              <Text size="4" weight="bold" className="truncate">
                {healthDesc[health.level]}
              </Text>
              <Flex direction="column" gap="1">
                <Text size="2" color="gray">
                  {t("dashboard.overview", "Overview")}
                </Text>
                <Text size="2" weight="medium">
                  {t("dashboard.onlineNodes", "Online {{online}}/{{total}}", {
                    online: stats.online,
                    total: stats.total,
                  })}
                </Text>
                <Tips
                  side="right"
                  className="mr-auto"
                  ariaLabel={t("dashboard.offlineListAria", "Offline servers")}
                  trigger={
                    <Text
                      size="2"
                      weight="medium"
                      color={stats.offline > 0 ? "red" : "gray"}
                      className={
                        offlineNodes.length > 0
                          ? "cursor-pointer hover:underline"
                          : ""
                      }
                    >
                      {t("dashboard.offlineNodes", "Offline {{offline}}", {
                        offline: stats.offline,
                      })}
                    </Text>
                  }
                >
                  {offlineNodes.length > 0 ? (
                    <Flex direction="column" gap="1">
                      {offlineNodes.map((node) => (
                        <Text key={node.uuid} size="2">
                          {node.name}
                        </Text>
                      ))}
                    </Flex>
                  ) : (
                    <Text size="2">
                      {t("dashboard.noOfflineNodes", "All servers are online")}
                    </Text>
                  )}
                </Tips>
              </Flex>
            </Flex>
          </Flex>
        </Card>

        <Card className="km-dashboard-card flex-1 min-w-64">
          <Flex direction="column" gap="3">
            <Flex gap="2" align="center" style={{ color: "var(--gray-10)" }}>
              <Database size={18} />
              <Text size="2" color="gray">
                {t("dashboard.dbUsage", "Database usage")}
              </Text>
            </Flex>
            <Flex direction="column" gap="2">
              <Flex justify="between" align="center" gap="2">
                <Text size="2" color="gray">
                  {t("dashboard.mainDb", "Main database")}
                </Text>
                <Text size="3" weight="bold">
                  {dbInfo ? formatBytes(dbInfo.main ?? 0) : "-"}
                </Text>
              </Flex>
              <Flex justify="between" align="center" gap="2">
                <Text size="2" color="gray">
                  {t("dashboard.monitoringDb", "Monitoring database")}
                </Text>
                <Text size="3" weight="bold">
                  {dbInfo ? formatBytes(dbInfo.monitoring ?? 0) : "-"}
                </Text>
              </Flex>
              <Separator size="4" />
              <Flex justify="between" align="center" gap="2">
                <Text size="2" weight="medium">
                  {t("settings.database.local_total", "Local Database Total")}
                </Text>
                <Text size="3" weight="bold">
                  {dbInfo
                    ? formatBytes((dbInfo.main ?? 0) + (dbInfo.monitoring ?? 0))
                    : "-"}
                </Text>
              </Flex>
            </Flex>
          </Flex>
        </Card>

        <Card className="km-dashboard-card flex-1 min-w-72">
          <Flex direction="column" gap="3">
            <Flex gap="2" align="center" style={{ color: "var(--amber-11)" }}>
              <CalendarClock size={18} />
              <Text size="3" weight="bold">
                {t("dashboard.expiringSoon", "Expiring soon")}
              </Text>
            </Flex>
            {expiringNodes.length === 0 ? (
              <Text size="2" color="gray">
                {t("dashboard.noExpiring", "No servers expiring soon")}
              </Text>
            ) : (
              <Flex direction="column" gap="3">
                {expiringNodes.map((node) => {
                  const daysLeft = Math.ceil(
                    (new Date(node.expired_at).getTime() - Date.now()) / DAY_MS,
                  );
                  return (
                    <Flex
                      key={node.uuid}
                      justify="between"
                      align="center"
                      gap="2"
                    >
                      <Flex direction="column" gap="1" style={{ minWidth: 0 }}>
                        <Text
                          size="2"
                          weight="medium"
                          className="truncate"
                          title={node.name}
                        >
                          {node.name}
                        </Text>
                        <Flex gap="2" align="center">
                          <Text size="2" color="gray">
                            {new Date(node.expired_at).toLocaleDateString()}
                          </Text>
                          <Badge
                            color={daysLeft <= 3 ? "red" : "amber"}
                            variant="soft"
                          >
                            {t("dashboard.daysLeft", "{{days}} days left", {
                              days: daysLeft,
                            })}
                          </Badge>
                        </Flex>
                      </Flex>
                      <Button
                        size="1"
                        variant="soft"
                        aria-label={t("dashboard.renewed", "I've renewed")}
                        disabled={
                          renewingUuid === node.uuid ||
                          !node.billing_cycle ||
                          node.billing_cycle <= 0
                        }
                        onClick={() => handleRenew(node)}
                      >
                        {renewingUuid === node.uuid
                          ? t("dashboard.renewing", "Renewing...")
                          : t("dashboard.renewed", "I've renewed")}
                      </Button>
                    </Flex>
                  );
                })}
              </Flex>
            )}
          </Flex>
        </Card>
      </Flex>

      <Flex gap="4" wrap="wrap" align="stretch">
        <Card className="flex-1 min-w-[320px]">
          <Flex direction="column" gap="3">
            <Flex justify="between" align="center" wrap="wrap" gap="2">
              <Text size="3" weight="bold">
                {t("dashboard.traffic24h", "Last 24h traffic")}
              </Text>
              <Flex gap="4" align="center" wrap="wrap">
                <Flex gap="1" align="center">
                  <span
                    className="w-2.5 h-2.5 rounded-full"
                    style={{ backgroundColor: "var(--green-9)" }}
                  />
                  <Text size="2" color="gray">
                    {t("dashboard.uploadRate", "Upload rate")}
                  </Text>
                </Flex>
                <Flex gap="1" align="center">
                  <span
                    className="w-2.5 h-2.5 rounded-full"
                    style={{ backgroundColor: "var(--blue-9)" }}
                  />
                  <Text size="2" color="gray">
                    {t("dashboard.downloadRate", "Download rate")}
                  </Text>
                </Flex>
                <Flex gap="1" align="center">
                  <span
                    className="w-4 border-t-2 border-dashed"
                    style={{ borderColor: "var(--green-9)" }}
                  />
                  <Text size="2" color="gray">
                    {t("dashboard.uploadTotal", "Upload cumulative")}
                  </Text>
                </Flex>
                <Flex gap="1" align="center">
                  <span
                    className="w-4 border-t-2 border-dashed"
                    style={{ borderColor: "var(--blue-9)" }}
                  />
                  <Text size="2" color="gray">
                    {t("dashboard.downloadTotal", "Download cumulative")}
                  </Text>
                </Flex>
                <Text size="2" color="gray">
                  ↑ {formatBytes(traffic?.totalUp ?? 0)} ↓{" "}
                  {formatBytes(traffic?.totalDown ?? 0)}
                </Text>
              </Flex>
            </Flex>
            {traffic === null ? (
              <Flex align="center" justify="center" style={{ height: 180 }}>
                <Loading text="" />
              </Flex>
            ) : traffic.points.length === 0 ? (
              <Flex align="center" justify="center" style={{ height: 180 }}>
                <Text size="2" color="gray">
                  {t("dashboard.noData", "No data")}
                </Text>
              </Flex>
            ) : (
              <ChartContainer
                config={chartConfig}
                className="km-dashboard-chart h-[180px] w-full"
                style={{ aspectRatio: "auto" }}
                aria-label={t(
                  "dashboard.trafficChartAria",
                  "Last 24h traffic chart, showing upload and download rate and cumulative traffic",
                )}
              >
                <AreaChart
                  data={traffic.points}
                  margin={{ top: 16, right: 8, bottom: 4, left: 8 }}
                >
                  <defs>
                    <linearGradient id="gradUp" x1="0" y1="0" x2="0" y2="1">
                      <stop
                        offset="0%"
                        stopColor="var(--green-9)"
                        stopOpacity={0.35}
                      />
                      <stop
                        offset="100%"
                        stopColor="var(--green-9)"
                        stopOpacity={0.02}
                      />
                    </linearGradient>
                    <linearGradient id="gradDown" x1="0" y1="0" x2="0" y2="1">
                      <stop
                        offset="0%"
                        stopColor="var(--blue-9)"
                        stopOpacity={0.35}
                      />
                      <stop
                        offset="100%"
                        stopColor="var(--blue-9)"
                        stopOpacity={0.02}
                      />
                    </linearGradient>
                  </defs>
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey="time"
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: any) =>
                      new Date(v).toLocaleTimeString([], {
                        hour: "2-digit",
                        minute: "2-digit",
                      })
                    }
                  />
                  <YAxis
                    yAxisId="rate"
                    type="number"
                    tickLine={false}
                    axisLine={false}
                    width={1}
                    mirror
                    tick={{ dx: 8 }}
                    tickFormatter={(v: any) =>
                      formatSpeed(Number(v)).replace(/ /g, "\u00a0")
                    }
                  />
                  <YAxis
                    yAxisId="cum"
                    orientation="right"
                    type="number"
                    tickLine={false}
                    axisLine={false}
                    width={1}
                    mirror
                    tick={{ dx: -8 }}
                    tickFormatter={(v: any) =>
                      formatBytes(Number(v)).replace(/ /g, "\u00a0")
                    }
                  />
                  <ChartTooltip
                    cursor={false}
                    content={
                      <ChartTooltipContent
                        labelFormatter={(_value: any, payload: any[]) => {
                          const point = payload?.[0]?.payload;
                          return point?.time
                            ? new Date(point.time).toLocaleString()
                            : "";
                        }}
                        formatter={(value: any, name: any) =>
                          name === "upRate" || name === "downRate"
                            ? formatSpeed(Number(value))
                            : formatBytes(Number(value))
                        }
                      />
                    }
                  />
                  <Area
                    type="monotone"
                    dataKey="upRate"
                    name="upRate"
                    yAxisId="rate"
                    stroke="var(--color-upRate)"
                    strokeWidth={2}
                    fill="url(#gradUp)"
                    isAnimationActive={false}
                  />
                  <Area
                    type="monotone"
                    dataKey="downRate"
                    name="downRate"
                    yAxisId="rate"
                    stroke="var(--color-downRate)"
                    strokeWidth={2}
                    fill="url(#gradDown)"
                    isAnimationActive={false}
                  />
                  <Area
                    type="monotone"
                    dataKey="upCum"
                    name="upCum"
                    yAxisId="cum"
                    stroke="var(--color-upCum)"
                    strokeWidth={1.5}
                    strokeDasharray="6 4"
                    fill="none"
                    isAnimationActive={false}
                  />
                  <Area
                    type="monotone"
                    dataKey="downCum"
                    name="downCum"
                    yAxisId="cum"
                    stroke="var(--color-downCum)"
                    strokeWidth={1.5}
                    strokeDasharray="6 4"
                    fill="none"
                    isAnimationActive={false}
                  />
                </AreaChart>
              </ChartContainer>
            )}
            {traffic && traffic.nodeTotals.length > 0 && (
              <>
                <Separator size="4" />
                <Flex direction="column" gap="3">
                  <Flex justify="between" align="center" gap="2">
                    <Text size="3" weight="bold">
                      {t("dashboard.topTraffic", "Top traffic servers")}
                    </Text>
                    <RankListPopover
                      title={t("dashboard.topTraffic", "Top traffic servers")}
                      ariaLabel={t("common.details", "Details")}
                    >
                      <Flex direction="column" gap="2">
                        {traffic.nodeTotals.map((node, index) => (
                          <Flex
                            key={node.uuid}
                            justify="between"
                            align="center"
                            gap="2"
                          >
                            <Text size="2" className="truncate">
                              <Text size="2" color="gray">
                                {index + 1}.
                              </Text>{" "}
                              {nodeNameMap.get(node.uuid) ??
                                node.uuid.slice(0, 8)}
                            </Text>
                            <Flex align="center" gap="2" className="shrink-0">
                              <Text
                                size="2"
                                color="gray"
                                className="whitespace-nowrap"
                              >
                                ↑ {formatBytes(node.up)} ↓{" "}
                                {formatBytes(node.down)}
                              </Text>
                              <MiniChartButton
                                uuid={node.uuid}
                                metricKeys={NET_METRIC_KEYS}
                                ariaLabel={t(
                                  "dashboard.viewChart",
                                  "View 24h chart",
                                )}
                              />
                            </Flex>
                          </Flex>
                        ))}
                      </Flex>
                    </RankListPopover>
                  </Flex>
                  <Flex direction="column" gap="3">
                    {traffic.nodeTotals.slice(0, 5).map((node, index) => (
                      <Flex key={node.uuid} direction="column" gap="1">
                        <Flex justify="between" align="center" gap="2">
                          <Text size="2" className="truncate">
                            <Text size="2" color="gray">
                              {index + 1}.
                            </Text>{" "}
                            {nodeNameMap.get(node.uuid) ?? node.uuid.slice(0, 8)}
                          </Text>
                          <Flex align="center" gap="2" className="shrink-0">
                            <Text
                              size="2"
                              color="gray"
                              className="whitespace-nowrap"
                            >
                              ↑ {formatBytes(node.up)} ↓ {formatBytes(node.down)}
                            </Text>
                            <MiniChartButton
                              uuid={node.uuid}
                              metricKeys={NET_METRIC_KEYS}
                              ariaLabel={t(
                                "dashboard.viewChart",
                                "View 24h chart",
                              )}
                            />
                          </Flex>
                        </Flex>
                        {node.peakRate > 0 && (
                          <Text size="1" color="gray" className="truncate">
                            {t(
                              "dashboard.peakAt",
                              "Peak {{value}} at {{time}}",
                              {
                                value: formatSpeed(node.peakRate),
                                time: formatPeakTime(t, node.peakTime),
                              },
                            )}
                          </Text>
                        )}
                        <div
                          className="h-2 rounded-full overflow-hidden"
                          style={{ backgroundColor: "var(--gray-5)" }}
                        >
                          <div
                            className="h-full rounded-full"
                            style={{
                              width: `${
                                (node.total / traffic.nodeTotals[0].total) *
                                100
                              }%`,
                              backgroundColor: "var(--accent-9)",
                              transition: "width 0.5s ease-out",
                            }}
                          />
                        </div>
                      </Flex>
                    ))}
                  </Flex>
                </Flex>
              </>
            )}
          </Flex>
        </Card>

        <Flex direction="column" gap="4" className="w-80 shrink-0">
          <Card>
            <TopRankCard
              title={t("dashboard.topCpu", "Top CPU usage")}
              icon={<Cpu size={18} />}
              items={topCpu}
              metricKeys={CPU_METRIC_KEYS}
              t={t}
            />
          </Card>
          <Card>
            <TopRankCard
              title={t("dashboard.topMem", "Top memory usage")}
              icon={<MemoryStick size={18} />}
              items={topMem}
              metricKeys={MEM_METRIC_KEYS}
              t={t}
            />
          </Card>
        </Flex>
      </Flex>

      <Card>
        <Flex direction="column" gap="3">
          <Flex gap="2" align="center" style={{ color: "var(--gray-10)" }}>
            <Gauge size={18} />
            <Text size="3" weight="bold">
              {t("nodeCard.ping", "Ping")}
            </Text>
          </Flex>
          <Flex gap="6" wrap="wrap">
            {renderLatencyColumn(
              t("dashboard.stableLatency", "Most stable latency"),
              stableLatencyItems,
              renderLatencyValue,
            )}
            {renderLatencyColumn(
              t("dashboard.unstableLatency", "Most unstable latency"),
              unstableLatencyItems,
              renderLatencyValue,
            )}
            {renderLatencyColumn(
              t("dashboard.highestLoss", "Highest packet loss"),
              highestLossItems,
              (item) => (
                <Text size="2" className="whitespace-nowrap">
                  {item.loss.toFixed(1)}%
                </Text>
              ),
            )}
          </Flex>
        </Flex>
      </Card>
    </Flex>
  );
};

const ProgressRing = ({
  percent,
  color,
  ariaLabel,
}: {
  percent: number;
  color: "green" | "red" | "orange" | "gray";
  ariaLabel?: string;
}) => {
  const radius = 40;
  const circumference = 2 * Math.PI * radius;
  const clamped = Math.min(Math.max(percent, 0), 100);
  return (
    <svg
      width="104"
      height="104"
      viewBox="0 0 104 104"
      style={{ flex: "none" }}
      role="img"
      aria-label={ariaLabel}
    >
      <circle
        cx="52"
        cy="52"
        r={radius}
        fill="none"
        stroke="var(--gray-5)"
        strokeWidth="10"
      />
      <circle
        cx="52"
        cy="52"
        r={radius}
        fill="none"
        stroke={`var(--${color}-9)`}
        strokeWidth="10"
        strokeLinecap="round"
        strokeDasharray={circumference}
        strokeDashoffset={circumference * (1 - clamped / 100)}
        transform="rotate(-90 52 52)"
        style={{ transition: "stroke-dashoffset 0.5s ease-out" }}
      />
      <text
        x="52"
        y="59"
        textAnchor="middle"
        fontSize="20"
        fontWeight="bold"
        fill="currentColor"
      >
        {clamped.toFixed(0)}%
      </text>
    </svg>
  );
};

const TopRankCard = ({
  title,
  icon,
  items,
  metricKeys,
  t,
}: {
  title: string;
  icon: React.ReactNode;
  items: TopRankItem[];
  metricKeys: string[];
  t: TFunction;
}) => {
  return (
    <Flex direction="column" gap="3">
      <Flex
        gap="2"
        align="center"
        justify="between"
        style={{ color: "var(--gray-10)" }}
      >
        <Flex gap="2" align="center">
          {icon}
          <Text size="3" weight="bold">
            {title}
          </Text>
        </Flex>
        <RankListPopover
          title={title}
          ariaLabel={t("common.details", "Details")}
        >
          <Flex direction="column" gap="2">
            {items.map((item, index) => (
              <Flex key={item.uuid} justify="between" align="center" gap="2">
                <Text size="2" className="truncate" title={item.name}>
                  <Text size="2" color="gray">
                    {index + 1}.
                  </Text>{" "}
                  {item.name}
                </Text>
                <Flex align="center" gap="1" className="shrink-0">
                  <Text size="2" weight="bold" className="whitespace-nowrap">
                    {item.value.toFixed(1)}%
                  </Text>
                  <MiniChartButton
                    uuid={item.uuid}
                    metricKeys={metricKeys}
                    ariaLabel={t("dashboard.viewChart", "View 24h chart")}
                  />
                </Flex>
              </Flex>
            ))}
          </Flex>
        </RankListPopover>
      </Flex>
      {items.length === 0 ? (
        <Text size="2" color="gray">
          {t("dashboard.noData", "No data")}
        </Text>
      ) : (
        <Flex direction="column" gap="3">
          {items.slice(0, 4).map((item, index) => {
            const percent = Math.min(Math.max(item.value, 0), 100);
            const barColor =
              percent >= 80 ? "red" : percent >= 60 ? "orange" : "green";
            return (
              <Flex key={item.uuid} direction="column" gap="1">
                <Flex justify="between" align="center" gap="2">
                  <Text size="2" className="truncate" title={item.name}>
                    <Text size="2" color="gray">
                      {index + 1}.
                    </Text>{" "}
                    {item.name}
                  </Text>
                  <Flex align="center" gap="1" className="shrink-0">
                    <Text size="2" weight="bold" className="whitespace-nowrap">
                      {item.value.toFixed(1)}%
                    </Text>
                    <MiniChartButton
                      uuid={item.uuid}
                      metricKeys={metricKeys}
                      ariaLabel={t("dashboard.viewChart", "View 24h chart")}
                    />
                  </Flex>
                </Flex>
                <Text size="1" color="gray" className="truncate">
                  {t("dashboard.peakAt", "Peak {{value}} at {{time}}", {
                    value: `${item.peak.toFixed(1)}%`,
                    time: formatPeakTime(t, item.peakTime),
                  })}
                </Text>
                <div
                  className="h-1.5 rounded-full overflow-hidden"
                  style={{ backgroundColor: "var(--gray-5)" }}
                >
                  <div
                    className="h-full rounded-full"
                    style={{
                      width: `${percent}%`,
                      backgroundColor: `var(--${barColor}-9)`,
                      transition: "width 0.5s ease-out",
                    }}
                  />
                </div>
              </Flex>
            );
          })}
        </Flex>
      )}
    </Flex>
  );
};

const formatMetricValue = (metricKey: string, value: number): string => {
  if (metricKey === "net.in.rate" || metricKey === "net.out.rate") {
    return formatSpeed(value);
  }
  if (metricKey === "memory.used") {
    return formatBytes(value);
  }
  if (metricKey === PING_LATENCY_METRIC) {
    return `${Math.round(value)} ms`;
  }
  return `${value.toFixed(1)}%`;
};

const MiniMetricChart = ({
  uuid,
  metricKeys,
  tags,
}: {
  uuid: string;
  metricKeys: string[];
  tags?: MetricTags;
}) => {
  const { t } = useTranslation();
  const { call } = useRPC2Call();
  const [seriesList, setSeriesList] = useState<MetricSeries[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const tagsKey = JSON.stringify(tags ?? null);
  const cacheKey = `${uuid}|${metricKeys.join(",")}|${tagsKey}`;

  useEffect(() => {
    let active = true;
    const cached = miniChartCache.get(cacheKey);
    if (cached) {
      setSeriesList(cached);
      setError(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    const now = new Date();
    const start = new Date(now.getTime() - 24 * 3600 * 1000);
    call<any, QueryMetricsResponse>("public:queryMetrics", {
      metric_keys: metricKeys,
      entity_id: uuid,
      tags,
      start: start.toISOString(),
      end: now.toISOString(),
      aggregation: "avg",
      max_points: 240,
      fill_empty: true,
    })
      .then((res) => {
        if (!active) return;
        const next = normalizeMetricSeriesList(res?.series);
        miniChartCache.set(cacheKey, next);
        setSeriesList(next);
        setLoading(false);
      })
      .catch((err) => {
        if (!active) return;
        setError(err instanceof Error ? err.message : "Error");
        setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [call, uuid, metricKeys, tags, tagsKey, cacheKey]);

  const chartData = useMemo(() => {
    const rows = new Map<number, Record<string, string | number | null>>();
    const keys: string[] = [];
    for (const series of seriesList) {
      if (!keys.includes(series.metric_key)) keys.push(series.metric_key);
      for (const point of series.points ?? []) {
        if (point.value == null) continue;
        const ts = new Date(point.time).getTime();
        const row = rows.get(ts) ?? { time: ts };
        row[series.metric_key] = point.value;
        rows.set(ts, row);
      }
    }
    return {
      rows: Array.from(rows.values()).sort(
        (a, b) => Number(a.time) - Number(b.time),
      ),
      keys,
    };
  }, [seriesList]);

  const chartConfig = useMemo(() => {
    const config: ChartConfig = {};
    for (const [index, key] of chartData.keys.entries()) {
      config[key] = {
        label:
          key === "net.in.rate"
            ? t("dashboard.uploadRate", "Upload rate")
            : key === "net.out.rate"
              ? t("dashboard.downloadRate", "Download rate")
              : key === "cpu.usage"
                ? t("dashboard.avgCpu", "Average CPU")
                : key === PING_LATENCY_METRIC
                  ? t("nodeCard.ping", "Ping")
                  : key,
        color: metricSeriesColor(index),
      };
    }
    return config;
  }, [chartData.keys, t]);

  return (
    <Flex direction="column" gap="2" style={{ width: 400 }}>
      <Text size="2" weight="bold">
        {t("chart.recentDay", "Last 1 day")}
      </Text>
      {loading ? (
        <Flex align="center" justify="center" style={{ height: 180 }}>
          <Loading text="" />
        </Flex>
      ) : error ? (
        <Flex align="center" justify="center" style={{ height: 180 }}>
          <Text size="2" color="red">
            {error}
          </Text>
        </Flex>
      ) : chartData.rows.length === 0 ? (
        <Flex align="center" justify="center" style={{ height: 180 }}>
          <Text size="2" color="gray">
            {t("dashboard.noData", "No data")}
          </Text>
        </Flex>
      ) : (
        <ChartContainer
          config={chartConfig}
          className="km-dashboard-chart h-[180px] w-full"
          style={{ aspectRatio: "auto" }}
        >
          <LineChart
            data={chartData.rows}
            margin={{ top: 16, right: 8, bottom: 4, left: 8 }}
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey="time"
              tickLine={false}
              axisLine={false}
              tickFormatter={(v: any) =>
                new Date(v).toLocaleTimeString([], {
                  hour: "2-digit",
                  minute: "2-digit",
                })
              }
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              width={1}
              mirror
              tick={{ dx: 8 }}
              tickFormatter={(v: any) =>
                formatMetricValue(chartData.keys[0] ?? "", Number(v)).replace(
                  / /g,
                  "\u00a0",
                )
              }
            />
            <ChartTooltip
              cursor={false}
              content={
                <ChartTooltipContent
                  labelFormatter={(_value: any, payload: any[]) => {
                    const point = payload?.[0]?.payload;
                    return point?.time
                      ? new Date(Number(point.time)).toLocaleString()
                      : "";
                  }}
                  formatter={(value: any, name: any) =>
                    formatMetricValue(String(name), Number(value))
                  }
                />
              }
            />
            {chartData.keys.map((key, index) => (
              <Line
                key={key}
                type="monotone"
                dataKey={key}
                name={key}
                stroke={metricSeriesColor(index)}
                strokeWidth={2}
                dot={false}
                isAnimationActive={false}
              />
            ))}
          </LineChart>
        </ChartContainer>
      )}
    </Flex>
  );
};

const RankListPopover = ({
  title,
  ariaLabel,
  children,
}: {
  title: string;
  ariaLabel?: string;
  children: React.ReactNode;
}) => (
  <Popover.Root>
    <Popover.Trigger>
      <IconButton size="1" variant="ghost" color="gray" aria-label={ariaLabel}>
        <List size={14} />
      </IconButton>
    </Popover.Trigger>
    <Popover.Content style={{ width: 340 }}>
      <Flex direction="column" gap="2">
        <Text size="2" weight="bold">
          {title}
        </Text>
        <div
          className="overflow-y-auto pr-1"
          style={{ maxHeight: 320 }}
        >
          {children}
        </div>
      </Flex>
    </Popover.Content>
  </Popover.Root>
);

const MiniChartButton = ({
  uuid,
  metricKeys,
  tags,
  ariaLabel,
}: {
  uuid: string;
  metricKeys: string[];
  tags?: MetricTags;
  ariaLabel?: string;
}) => (
  <Popover.Root>
    <Popover.Trigger>
      <IconButton
        size="1"
        variant="ghost"
        color="gray"
        aria-label={ariaLabel}
      >
        <ChartNoAxesCombined size={14} />
      </IconButton>
    </Popover.Trigger>
    <Popover.Content style={{ width: 440 }}>
      <MiniMetricChart uuid={uuid} metricKeys={metricKeys} tags={tags} />
    </Popover.Content>
  </Popover.Root>
);

export default Dashboard;
