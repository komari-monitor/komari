import type {
  MetricPoint,
  MetricSeries,
  MetricTagged,
  MetricTags,
  PublicPingTask,
} from "@/types/metrics";

export const PING_LATENCY_METRIC = "ping.latency_ms";

export const METRIC_SERIES_COLORS = [
  "#2563EB",
  "#F97316",
  "#8B5CF6",
  "#14B8A6",
  "#E11D48",
  "#EAB308",
  "#06B6D4",
  "#D946EF",
  "#22C55E",
  "#EF4444",
  "#6366F1",
  "#84CC16",
] as const;

const hasTags = (tags?: MetricTags): tags is MetricTags =>
  Boolean(tags && Object.keys(tags).length > 0);

export const metricTags = (value: MetricTagged): MetricTags | undefined => {
  return value.tags;
};

export const metricTagsKey = (tags?: MetricTags) => {
  if (!hasTags(tags)) return "";
  return JSON.stringify(Object.keys(tags).sort().map((key) => [key, tags[key]]));
};

export const metricSeriesKey = (metricKey: string, tags?: MetricTags) =>
  `${metricKey}|${metricTagsKey(tags)}`;

const hashString = (value: string) => {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index++) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
};

export const metricSeriesDataKey = (metricKey: string, tags?: MetricTags) => {
  return `s_${hashString(metricSeriesKey(metricKey, tags)).toString(36)}`;
};

export const metricSeriesColor = (index: number) => {
  if (index < METRIC_SERIES_COLORS.length) return METRIC_SERIES_COLORS[index];
  const hue = Math.round((index * 137.508) % 360);
  return `hsl(${hue} 68% 48%)`;
};

export const normalizeMetricSeries = (series: MetricSeries): MetricSeries[] => {
  const seriesTags = metricTags(series);
  if (!series.points?.length) {
    return [{ ...series, tags: seriesTags, points: [] }];
  }

  const groups = new Map<string, { tags?: MetricTags; points: MetricPoint[] }>();
  for (const point of series.points) {
    const pointTags = metricTags(point);
    const tags = hasTags(pointTags) ? pointTags : seriesTags;
    const key = metricTagsKey(tags);
    const group = groups.get(key) ?? { tags, points: [] };
    group.points.push({ ...point, tags });
    groups.set(key, group);
  }

  return Array.from(groups.values(), (group) => ({
    ...series,
    tags: group.tags,
    count: group.points.length,
    points: group.points,
  })).sort(compareMetricSeries);
};

const compareMetricSeries = (left: MetricSeries, right: MetricSeries) => {
  const leftKey = metricSeriesKey(left.metric_key, metricTags(left));
  const rightKey = metricSeriesKey(right.metric_key, metricTags(right));
  if (leftKey === rightKey) return 0;
  return leftKey < rightKey ? -1 : 1;
};

export const normalizeMetricSeriesList = (series: MetricSeries[] | null | undefined) =>
  (Array.isArray(series) ? series : [])
    .flatMap(normalizeMetricSeries)
    .sort(compareMetricSeries);

export const isPingMetric = (metricKey: string) =>
  metricKey === "ping" || metricKey.startsWith("ping.");

export const formatRemainingTags = (tags: MetricTags | undefined, excludedKeys: string[] = []) => {
  if (!hasTags(tags)) return "";
  const excluded = new Set(excludedKeys);
  return Object.keys(tags)
    .filter((key) => !excluded.has(key))
    .sort()
    .map((key) => `${key}:${tags[key]}`)
    .join(" ");
};

export const pingTaskId = (tags?: MetricTags) => {
  const taskId = tags?.task_id?.trim();
  return taskId || undefined;
};

export const pingTaskName = (
  taskId: string,
  tasks: ReadonlyMap<string, PublicPingTask>,
  fallback: (taskId: string) => string,
) => tasks.get(taskId)?.name?.trim() || fallback(taskId);

// Keep ping metric series aligned with the backend's ORDER BY weight ASC, id ASC.
export const comparePingTaskOrder = (
  leftTags: MetricTags | undefined,
  rightTags: MetricTags | undefined,
  tasks: ReadonlyMap<string, PublicPingTask>,
) => {
  const leftId = pingTaskId(leftTags);
  const rightId = pingTaskId(rightTags);
  const leftTask = leftId ? tasks.get(leftId) : undefined;
  const rightTask = rightId ? tasks.get(rightId) : undefined;

  if (leftTask && rightTask) {
    const leftWeight = Number(leftTask.weight);
    const rightWeight = Number(rightTask.weight);
    if (Number.isFinite(leftWeight) && Number.isFinite(rightWeight)) {
      const weightDelta = leftWeight - rightWeight;
      if (weightDelta !== 0) return weightDelta;
    }

    const idDelta = leftTask.id - rightTask.id;
    if (idDelta !== 0) return idDelta;
  } else if (leftTask) {
    return -1;
  } else if (rightTask) {
    return 1;
  }

  if (leftId === rightId) return 0;
  if (!leftId) return 1;
  if (!rightId) return -1;
  return leftId.localeCompare(rightId, undefined, { numeric: true });
};

export const pingMetricStatKey = (entityId: string, taskId: string) =>
  `${entityId}:${taskId}`;

export type MetricChartRow = Record<string, string | number | null>;

export const trimMetricChartBoundaryRows = (
  rows: MetricChartRow[],
  dataKeys: readonly string[],
) => {
  if (rows.length === 0 || dataKeys.length === 0) return rows;

  const hasValue = (row: MetricChartRow) =>
    dataKeys.some((dataKey) => {
      const value = row[dataKey];
      return typeof value === "number" && Number.isFinite(value);
    });

  let first = 0;
  while (first < rows.length && !hasValue(rows[first])) first += 1;
  if (first === rows.length) return [];

  let last = rows.length - 1;
  while (last > first && !hasValue(rows[last])) last -= 1;
  return rows.slice(first, last + 1);
};

export const metricChartBoundaryTicks = (rows: MetricChartRow[]) => {
  if (rows.length === 0) return [];
  const first = String(rows[0].time);
  const last = String(rows[rows.length - 1].time);
  return first === last ? [first] : [first, last];
};

export const applyMetricEwma = <TSeries extends { dataKey: string }>(
  rows: MetricChartRow[],
  series: readonly TSeries[],
  enabled: boolean,
) => {
  if (!enabled) return rows;

  const alpha = 0.35;
  const smoothed = rows.map((row) => ({ ...row }));
  for (const item of series) {
    let previous: number | null = null;
    for (const row of smoothed) {
      const value = row[item.dataKey];
      if (typeof value !== "number" || !Number.isFinite(value)) continue;
      previous = previous === null ? value : alpha * value + (1 - alpha) * previous;
      row[item.dataKey] = previous;
    }
  }
  return smoothed;
};
