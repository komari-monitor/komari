import dayjs from 'dayjs'

/** 字节单位常量 */
const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const
const LAST_BYTE_UNIT = BYTE_UNITS.at(-1)

const SECONDS_PER_DAY = 86400

/** 时间单位配置（秒为单位） */
const TIME_UNITS = [
  { value: SECONDS_PER_DAY, label: '天' },
  { value: 3600, label: '小时' },
  { value: 60, label: '分钟' },
  { value: 1, label: '秒' },
] as const

/** 运行时间格式化精度类型 */
export type UptimeFormat = 'day' | 'hour' | 'minute' | 'second'

const FORMAT_MAX_UNIT_INDEX_MAP: Record<UptimeFormat, number> = {
  day: 0, // 只到天
  hour: 1, // 到小时
  minute: 2, // 到分钟
  second: 3, // 到秒
}

/** 字节格式化精度配置 */
export interface ByteDecimalsConfig {
  /** B 精确位数，-1 为不显示此单位 */
  B?: number
  /** KB 精确位数，-1 为不显示此单位 */
  KB?: number
  /** MB 精确位数，-1 为不显示此单位 */
  MB?: number
  /** GB 精确位数，-1 为不显示此单位 */
  GB?: number
  /** TB 及以上精确位数，-1 为不显示此单位 */
  TB?: number
}

/** 默认字节精度配置 */
const DEFAULT_BYTE_DECIMALS: ByteDecimalsConfig = {
  B: 0,
  KB: 0,
  MB: 1,
  GB: 1,
  TB: 2,
}

/**
 * 格式化字节数为可读单位
 * @param bytes 字节数
 * @param decimals 小数位数
 * @returns 格式化后的字符串，如 "1.5 GB"
 */
export function formatBytes(bytes: number, decimals = 1): string {
  if (bytes === 0)
    return '0 B'

  const k = 1024
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  const unit = BYTE_UNITS[i] ?? LAST_BYTE_UNIT
  return `${(bytes / k ** i).toFixed(decimals)} ${unit}`
}

/**
 * 格式化字节数为可读单位（支持自定义精度配置）
 * @param bytes 字节数
 * @param config 精度配置
 * @returns 格式化后的字符串，如 "1.5 GB"
 */
export function formatBytesWithConfig(bytes: number, config?: ByteDecimalsConfig): string {
  const mergedConfig = { ...DEFAULT_BYTE_DECIMALS, ...config }

  if (bytes === 0) {
    // 0 字节时，检查 B 是否被禁用
    if (mergedConfig.B === -1)
      return '0 KB'
    return '0 B'
  }

  const k = 1024
  const i = Math.floor(Math.log(bytes) / Math.log(k))

  // 获取对应单位的精度配置
  const unitKey = BYTE_UNITS[i]
  // PB 及以上单位使用 TB 的精度配置
  const decimals = (unitKey === 'TB' || unitKey === 'PB') ? mergedConfig.TB : mergedConfig[unitKey as keyof ByteDecimalsConfig]

  // 如果当前单位被禁用，向上查找可用单位
  if (decimals === -1) {
    for (let j = i + 1; j < BYTE_UNITS.length; j++) {
      const nextUnitKey = BYTE_UNITS[j]
      const nextDecimals = (nextUnitKey === 'TB' || nextUnitKey === 'PB') ? mergedConfig.TB : mergedConfig[nextUnitKey as keyof ByteDecimalsConfig]
      if (nextDecimals !== -1) {
        const unit = BYTE_UNITS[j]
        return `${(bytes / k ** j).toFixed(nextDecimals)} ${unit}`
      }
    }
    // 所有单位都被禁用，使用默认行为
    const unit = BYTE_UNITS[i] ?? LAST_BYTE_UNIT
    return `${(bytes / k ** i).toFixed(1)} ${unit}`
  }

  const unit = BYTE_UNITS[i] ?? LAST_BYTE_UNIT
  return `${(bytes / k ** i).toFixed(decimals)} ${unit}`
}

/**
 * 格式化字节数为分离的数值和单位（支持自定义精度配置）
 * @param bytes 字节数
 * @param config 精度配置
 * @returns 分离的数值和单位，如 { value: "1.5", unit: "GB" }
 */
export function formatBytesSplit(bytes: number, config?: ByteDecimalsConfig): { value: string, unit: string } {
  const mergedConfig = { ...DEFAULT_BYTE_DECIMALS, ...config }

  if (bytes === 0) {
    if (mergedConfig.B === -1)
      return { value: '0', unit: 'KB' }
    return { value: '0', unit: 'B' }
  }

  const k = 1024
  const i = Math.floor(Math.log(bytes) / Math.log(k))

  const unitKey = BYTE_UNITS[i]
  const decimals = (unitKey === 'TB' || unitKey === 'PB') ? mergedConfig.TB : mergedConfig[unitKey as keyof ByteDecimalsConfig]

  if (decimals === -1) {
    for (let j = i + 1; j < BYTE_UNITS.length; j++) {
      const nextUnitKey = BYTE_UNITS[j]
      const nextDecimals = (nextUnitKey === 'TB' || nextUnitKey === 'PB') ? mergedConfig.TB : mergedConfig[nextUnitKey as keyof ByteDecimalsConfig]
      if (nextDecimals !== -1) {
        const unit = BYTE_UNITS[j]
        return { value: (bytes / k ** j).toFixed(nextDecimals), unit: `${unit}` }
      }
    }
    const unit = BYTE_UNITS[i] ?? LAST_BYTE_UNIT
    return { value: (bytes / k ** i).toFixed(1), unit: `${unit}` }
  }

  const unit = BYTE_UNITS[i] ?? LAST_BYTE_UNIT
  return { value: (bytes / k ** i).toFixed(decimals), unit: `${unit}` }
}

/**
 * 格式化字节速率为分离的数值和单位（支持自定义精度配置）
 * @param bytes 字节速率
 * @param config 精度配置
 * @returns 分离的数值和单位，如 { value: "1.5", unit: "GB/s" }
 */
export function formatBytesPerSecondSplit(bytes: number, config?: ByteDecimalsConfig): { value: string, unit: string } {
  const result = formatBytesSplit(bytes, config)
  return { value: result.value, unit: `${result.unit}/s` }
}

/**
 * 格式化字节速率为可读单位
 * @param bytes 字节速率
 * @returns 格式化后的字符串，如 "1.5 GB/s"
 */
export function formatBytesPerSecond(bytes: number): string {
  return `${formatBytes(bytes)}/s`
}

/**
 * 格式化字节速率为可读单位（支持自定义精度配置）
 * @param bytes 字节速率
 * @param config 精度配置
 * @returns 格式化后的字符串，如 "1.5 GB/s"
 */
export function formatBytesPerSecondWithConfig(bytes: number, config?: ByteDecimalsConfig): string {
  return `${formatBytesWithConfig(bytes, config)}/s`
}

export function getUptimeDays(seconds: number | null | undefined): number {
  const normalizedSeconds = Number(seconds)
  if (!Number.isFinite(normalizedSeconds) || normalizedSeconds <= 0)
    return 0
  return Math.floor(normalizedSeconds / SECONDS_PER_DAY)
}

/**
 * 格式化运行时间
 * @param seconds 秒数
 * @returns 格式化后的字符串，如 "2 天 3 小时 15 分钟"
 */
export function formatUptime(seconds: number): string {
  if (!seconds || seconds <= 0)
    return '0 秒'

  const parts: string[] = []
  let remaining = seconds

  for (const { value, label } of TIME_UNITS) {
    const amount = Math.floor(remaining / value)
    if (amount > 0) {
      parts.push(`${amount} ${label}`)
      remaining %= value
    }
  }

  return parts.length > 0 ? parts.join(' ') : '0 秒'
}

/**
 * 格式化运行时间（支持自定义精度）
 * @param seconds 秒数
 * @param format 精度格式：'day' | 'hour' | 'minute' | 'second'
 * - 'day': 只显示天（如 "2 天"），不满一天时显示"不足 1 天"
 * - 'hour': 显示天和小时（如 "2 天 3 小时"），不满一小时时显示"不足 1 小时"
 * - 'minute': 显示天、小时、分钟（如 "2 天 3 小时 15 分钟"），不满一分钟时显示"不足 1 分钟"
 * - 'second': 显示天、小时、分钟、秒（如 "2 天 3 小时 15 分钟 30 秒"）
 * @returns 格式化后的字符串
 */
export function formatUptimeWithFormat(seconds: number, format: UptimeFormat = 'day'): string {
  const maxUnitIndex = FORMAT_MAX_UNIT_INDEX_MAP[format]
  const normalizedSeconds = Number(seconds)
  if (!Number.isFinite(normalizedSeconds) || normalizedSeconds <= 0)
    return '0 秒'

  const parts: string[] = []
  let remaining = Math.floor(normalizedSeconds)

  for (let i = 0; i < TIME_UNITS.length; i++) {
    const unit = TIME_UNITS[i]
    if (!unit)
      continue
    const { value, label } = unit
    const amount = Math.floor(remaining / value)
    if (amount > 0) {
      parts.push(`${amount} ${label}`)
      remaining %= value
    }
    // 达到最大单位索引时停止
    if (i >= maxUnitIndex) {
      break
    }
  }

  // 如果没有任何单位有值，显示"不足 1 X"
  if (parts.length === 0)
    return `不足 1 ${TIME_UNITS[maxUnitIndex]?.label ?? '秒'}`

  return parts.join(' ')
}

/**
 * 计算占用百分比
 * @param used 已使用量
 * @param total 总量
 * @returns 百分比（0-100）
 */
export function calcPercentage(used: number, total: number): number {
  if (total === 0)
    return 0
  return (used / total) * 100
}

/** 状态阈值配置 */
const STATUS_THRESHOLDS = {
  success: 60,
  warning: 80,
} as const

/**
 * 根据占用百分比返回状态
 * @param percentage 百分比
 * @returns 状态类型
 */
export function getStatus(percentage: number): 'success' | 'warning' | 'error' {
  if (percentage < STATUS_THRESHOLDS.success)
    return 'success'
  if (percentage < STATUS_THRESHOLDS.warning)
    return 'warning'
  return 'error'
}

/**
 * 格式化时间戳为可读日期时间
 * @param timestamp 时间戳字符串或 Date 对象
 * @returns 格式化后的字符串，如 "2024-01-15 14:30:00"
 */
export function formatDateTime(timestamp: string | Date | undefined, format = 'YYYY-MM-DD HH:mm:ss'): string {
  if (!timestamp)
    return '-'

  const date = dayjs(timestamp)

  if (!date.isValid())
    return '-'

  return date.format(format)
}
