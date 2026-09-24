interface GpuDetailLike {
  utilization?: unknown
  usage?: unknown
}

interface GpuReportLike {
  average_usage?: unknown
  averageUsage?: unknown
  detailed_info?: GpuDetailLike[]
  detailedInfo?: GpuDetailLike[]
}

export interface GpuStatusLike {
  gpu?: unknown
  gpu_average_usage?: unknown
  gpu_detailed_info?: GpuDetailLike[]
}

function asFiniteNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function average(values: number[]): number | null {
  if (!values.length)
    return null
  return values.reduce((sum, value) => sum + value, 0) / values.length
}

function usagesFromDetails(details: unknown): number[] {
  if (!Array.isArray(details))
    return []
  return details
    .map(item => asFiniteNumber(item?.utilization) ?? asFiniteNumber(item?.usage))
    .filter((value): value is number => value != null)
}

export function gpuUsageFromUnknown(value: unknown): number {
  const direct = asFiniteNumber(value)
  if (direct != null)
    return direct

  if (!value || typeof value !== 'object')
    return 0

  const report = value as GpuReportLike
  const nested = asFiniteNumber(report.average_usage) ?? asFiniteNumber(report.averageUsage)
  if (nested != null)
    return nested

  return average(usagesFromDetails(report.detailed_info ?? report.detailedInfo)) ?? 0
}

export function gpuUsageFromStatus(status: GpuStatusLike | null | undefined): number {
  if (!status)
    return 0

  const fromAverage = asFiniteNumber(status.gpu_average_usage)
  if (fromAverage != null)
    return fromAverage

  const fromGpu = gpuUsageFromUnknown(status.gpu)
  if (fromGpu !== 0)
    return fromGpu

  return average(usagesFromDetails(status.gpu_detailed_info)) ?? fromGpu
}
