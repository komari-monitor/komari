import type { MetricDefinition, MetricQueryParams, MetricQueryResponse, PingMetricStatsParams, PingMetricStatsResponse, PingTaskInfo } from '@/utils/rpc'
import { CACHE_CONFIG } from '@/constants/cache'
import { SharedCache } from '@/services/cache.service'
import { requestManager } from '@/services/request.service'
import { getSharedRpc, RpcError } from '@/utils/rpc'

function normalizeHours(hours: number | null | undefined): number | undefined {
  if (typeof hours !== 'number' || !Number.isFinite(hours) || hours <= 0)
    return undefined
  return Math.max(1, Math.floor(hours))
}

function normalizeMaxPoints(maxPoints: number | null | undefined): number | undefined {
  if (typeof maxPoints !== 'number' || !Number.isFinite(maxPoints) || maxPoints <= 0)
    return undefined
  return Math.floor(maxPoints)
}

function cachePart(value: unknown): string {
  if (value === undefined || value === null)
    return 'all'
  if (Array.isArray(value))
    return value.map(item => String(item)).sort().join(',') || 'empty'
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value, Object.keys(value as Record<string, unknown>).sort())
    }
    catch {
      return String(value)
    }
  }
  return String(value)
}

function shouldRetryMetricRequest(error: unknown): boolean {
  if (error instanceof RpcError)
    return error.code !== 401 && error.code !== 403 && error.code !== -32601
  return true
}

function normalizeMetricKeys(params: MetricQueryParams): string[] {
  const keys = [
    ...(params.metric_keys ?? []),
    ...(params.metrics ?? []),
    ...(params.metric_key ? [params.metric_key] : []),
  ]
  return [...new Set(keys.filter(Boolean))].sort()
}

const metricDefinitionsCache = new SharedCache<MetricDefinition[]>({
  maxSize: 1,
  ttl: CACHE_CONFIG.request.ttl,
  cleanupInterval: CACHE_CONFIG.cleanup.interval,
})

export function getMetricDefinitionsRequestKey(): string {
  return 'metrics:definitions'
}

export function getQueryMetricsRequestKey(params: MetricQueryParams): string {
  return [
    'metrics:query',
    cachePart(normalizeMetricKeys(params)),
    cachePart(params.entity_id),
    cachePart(params.entity_ids),
    cachePart(params.hours),
    cachePart(params.start ?? params.start_time),
    cachePart(params.end ?? params.end_time),
    cachePart(params.max_points ?? params.downsample_points),
    cachePart(params.aggregation ?? params.downsample_algorithm ?? params.algorithm),
    cachePart(params.tags),
    cachePart(params.downsample ?? params.server_downsample),
    cachePart(params.fill_empty),
  ].join(':')
}

export function getPingMetricStatsRequestKey(params: PingMetricStatsParams): string {
  return [
    'metrics:ping-stats',
    cachePart(params.uuid ?? params.entity_id),
    cachePart(params.entity_ids),
    cachePart(params.task_id),
    cachePart(params.task_ids),
    cachePart(params.hours),
    cachePart(params.start ?? params.start_time),
    cachePart(params.end ?? params.end_time),
    cachePart(params.max_points ?? params.downsample_points),
  ].join(':')
}

export function getPublicPingTasksRequestKey(): string {
  return 'metrics:public-ping-tasks'
}

export function abortQueryMetrics(params: MetricQueryParams): void {
  requestManager.abort(getQueryMetricsRequestKey(params))
}

export function abortPingMetricStats(params: PingMetricStatsParams): void {
  requestManager.abort(getPingMetricStatsRequestKey(params))
}

export async function loadMetricDefinitions(): Promise<MetricDefinition[]> {
  const key = getMetricDefinitionsRequestKey()
  const cached = metricDefinitionsCache.get(key)
  if (cached)
    return cached

  const definitions = await requestManager.run(
    key,
    async () => getSharedRpc().listPublicMetricDefinitions(),
    { shouldRetry: shouldRetryMetricRequest },
  )
  return metricDefinitionsCache.set(key, definitions)
}

export async function queryMetrics(params: MetricQueryParams): Promise<MetricQueryResponse> {
  const normalizedParams: MetricQueryParams = {
    ...params,
    hours: normalizeHours(params.hours),
    max_points: normalizeMaxPoints(params.max_points ?? params.downsample_points),
  }

  return requestManager.run(
    getQueryMetricsRequestKey(normalizedParams),
    async signal => getSharedRpc().queryPublicMetrics(normalizedParams, signal),
    { shouldRetry: shouldRetryMetricRequest },
  )
}

export async function loadPingMetricStats(params: PingMetricStatsParams): Promise<PingMetricStatsResponse> {
  const normalizedParams: PingMetricStatsParams = {
    ...params,
    hours: normalizeHours(params.hours),
    max_points: normalizeMaxPoints(params.max_points ?? params.downsample_points),
  }

  return requestManager.run(
    getPingMetricStatsRequestKey(normalizedParams),
    async signal => getSharedRpc().getPublicPingMetricStats(normalizedParams, signal),
    { shouldRetry: shouldRetryMetricRequest },
  )
}

export async function loadPublicPingTasks(): Promise<PingTaskInfo[]> {
  return requestManager.run(
    getPublicPingTasksRequestKey(),
    async () => getSharedRpc().getPublicPingTasks(),
    { shouldRetry: shouldRetryMetricRequest },
  )
}
