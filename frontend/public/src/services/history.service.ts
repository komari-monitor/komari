import type { PingRecord, PingTaskInfo, StatusRecord } from '@/utils/rpc'
import { requestManager } from '@/services/request.service'
import { ApiError, getSharedApi } from '@/utils/api'
import { gpuUsageFromStatus } from '@/utils/gpuHelper'
import { getSharedRpc, RpcError } from '@/utils/rpc'

function numberOrZero(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function normalizeHours(hours: number): number {
  return Math.max(1, Math.floor(hours))
}

function normalizeMaxCount(maxCount: number | null | undefined): number | undefined {
  if (typeof maxCount !== 'number' || !Number.isFinite(maxCount) || maxCount <= 0)
    return undefined
  return Math.floor(maxCount)
}

function cachePart(value: string | number | undefined): string {
  return value === undefined ? 'all' : String(value)
}

function shouldRetryHistoryRequest(error: unknown): boolean {
  if (error instanceof RpcError)
    return error.code !== 401 && error.code !== 403
  if (error instanceof ApiError)
    return error.code !== 401 && error.code !== 403
  return true
}

type StatusRecordsPayload = Array<Partial<StatusRecord>> | Record<string, Array<Partial<StatusRecord>>>

function isStatusRecordsMap(records: StatusRecordsPayload): records is Record<string, Array<Partial<StatusRecord>>> {
  return !Array.isArray(records)
}

export function normalizeStatusRecordsPayload(records: StatusRecordsPayload | undefined): StatusRecord[] {
  if (!records)
    return []

  if (Array.isArray(records))
    return normalizeStatusRecords(records)

  if (isStatusRecordsMap(records))
    return Object.values(records).flatMap(clientRecords => normalizeStatusRecords(clientRecords))

  return []
}

export function getLoadRecordsRequestKey(uuid: string | undefined, hours: number, maxCount?: number): string {
  return `history:load:${cachePart(uuid)}:${normalizeHours(hours)}:${cachePart(normalizeMaxCount(maxCount))}`
}

export function getNodeLoadRecordsRequestKey(uuid: string, hours: number, maxCount?: number): string {
  return `history:node-load:${uuid}:${normalizeHours(hours)}:${cachePart(normalizeMaxCount(maxCount))}`
}

export function getPingRecordsRequestKey(hours: number, maxCount?: number, uuid?: string): string {
  return `history:ping:${cachePart(uuid)}:${normalizeHours(hours)}:${cachePart(normalizeMaxCount(maxCount))}`
}

export function abortLoadRecords(uuid: string | undefined, hours: number, maxCount?: number): void {
  requestManager.abort(getLoadRecordsRequestKey(uuid, hours, maxCount))
}

export function abortNodeLoadRecords(uuid: string, hours: number, maxCount?: number): void {
  requestManager.abort(getNodeLoadRecordsRequestKey(uuid, hours, maxCount))
}

export function abortPingRecords(hours: number, maxCount?: number, uuid?: string): void {
  requestManager.abort(getPingRecordsRequestKey(hours, maxCount, uuid))
}

export function normalizeStatusRecord(record: Partial<StatusRecord>): StatusRecord | null {
  if (!record.client || !record.time)
    return null

  return {
    client: record.client,
    time: record.time,
    cpu: numberOrZero(record.cpu),
    gpu: gpuUsageFromStatus(record),
    gpu_average_usage: gpuUsageFromStatus(record),
    gpu_detailed_info: record.gpu_detailed_info,
    ram: numberOrZero(record.ram),
    ram_total: numberOrZero(record.ram_total),
    swap: numberOrZero(record.swap),
    swap_total: numberOrZero(record.swap_total),
    load: numberOrZero(record.load),
    load5: numberOrZero(record.load5 ?? record.load),
    load15: numberOrZero(record.load15 ?? record.load5 ?? record.load),
    temp: numberOrZero(record.temp),
    disk: numberOrZero(record.disk),
    disk_total: numberOrZero(record.disk_total),
    net_in: numberOrZero(record.net_in),
    net_out: numberOrZero(record.net_out),
    net_total_up: numberOrZero(record.net_total_up),
    net_total_down: numberOrZero(record.net_total_down),
    traffic_up: numberOrZero(record.traffic_up),
    traffic_down: numberOrZero(record.traffic_down),
    process: numberOrZero(record.process),
    connections: numberOrZero(record.connections),
    connections_udp: numberOrZero(record.connections_udp),
  }
}

export function normalizeStatusRecords(records: Array<Partial<StatusRecord>> | undefined): StatusRecord[] {
  return (records ?? [])
    .map(normalizeStatusRecord)
    .filter((record): record is StatusRecord => Boolean(record))
    .sort((left, right) => new Date(left.time).getTime() - new Date(right.time).getTime())
}

export function buildRecordsByClient(records: StatusRecord[]): Map<string, StatusRecord[]> {
  const grouped = new Map<string, StatusRecord[]>()
  for (const record of records) {
    const clientRecords = grouped.get(record.client) ?? []
    clientRecords.push(record)
    grouped.set(record.client, clientRecords)
  }
  return grouped
}

export async function loadLoadRecords(uuid: string | undefined, hours: number, maxCount?: number): Promise<StatusRecord[]> {
  const safeHours = normalizeHours(hours)
  const safeMaxCount = normalizeMaxCount(maxCount)
  return requestManager.run(
    getLoadRecordsRequestKey(uuid, safeHours, safeMaxCount),
    async (signal) => {
      const result = await getSharedRpc().getLoadRecords(uuid, safeHours, undefined, safeMaxCount, signal)
      return normalizeStatusRecordsPayload(result.records)
    },
    { shouldRetry: shouldRetryHistoryRequest },
  )
}

export async function loadNodeLoadRecords(uuid: string, hours: number, maxCount?: number): Promise<StatusRecord[]> {
  const safeHours = normalizeHours(hours)
  const safeMaxCount = normalizeMaxCount(maxCount)
  return requestManager.run(
    getNodeLoadRecordsRequestKey(uuid, safeHours, safeMaxCount),
    async (signal) => {
      try {
        const result = await getSharedRpc().getLoadRecords(uuid, safeHours, undefined, safeMaxCount, signal)
        return normalizeStatusRecordsPayload(result.records)
      }
      catch {
        const result = await getSharedApi().getLoadRecords(uuid, safeHours, safeMaxCount, signal)
        return normalizeStatusRecords(result.records)
      }
    },
    { shouldRetry: shouldRetryHistoryRequest },
  )
}

export async function loadPingRecords(hours: number, maxCount?: number, uuid?: string): Promise<PingRecord[]> {
  const safeHours = normalizeHours(hours)
  const safeMaxCount = normalizeMaxCount(maxCount)
  return requestManager.run(
    getPingRecordsRequestKey(safeHours, safeMaxCount, uuid),
    async (signal) => {
      const result = await getSharedRpc().getPingRecords(undefined, safeHours, safeMaxCount, signal, uuid)
      return result.records ?? []
    },
    { shouldRetry: shouldRetryHistoryRequest },
  )
}

export async function loadPingRecordsWithTasks(hours: number, maxCount?: number, uuid?: string): Promise<{ records: PingRecord[], tasks: PingTaskInfo[] }> {
  const safeHours = normalizeHours(hours)
  const safeMaxCount = normalizeMaxCount(maxCount)
  return requestManager.run(
    `${getPingRecordsRequestKey(safeHours, safeMaxCount, uuid)}:tasks`,
    async (signal) => {
      const result = await getSharedRpc().getPingRecords(undefined, safeHours, safeMaxCount, signal, uuid)
      return {
        records: result.records ?? [],
        tasks: result.tasks ?? [],
      }
    },
    { shouldRetry: shouldRetryHistoryRequest },
  )
}
