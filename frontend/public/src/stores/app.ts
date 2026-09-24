import type { PermissionKey, VerifyLoginOptions } from '@/services/auth.service'
import type { MeInfo, PublicSettings } from '@/utils/api'
import type { ByteDecimalsConfig } from '@/utils/helper'
import { useStorageAsync } from '@vueuse/core'
import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { getAuthSession, requirePermission, setAuthSessionFromLogin, verifyLogin } from '@/services/auth.service'

export type ThemeMode = 'auto' | 'light' | 'dark'
export type ManagedThemeMode = 'beijing' | 'light' | 'dark'
export type GeneralCardKey
  = | 'currentTime'
    | 'memory'
    | 'disk'
    | 'remainingValue'
    | 'totalTraffic'
    | 'uploadSpeed'
    | 'downloadSpeed'
    | 'onlineNodes'
    | 'avgCpu'
    | 'avgGpu'
    | 'avgLoad'
    | 'swap'
    | 'processes'
    | 'connections'
    | 'cpuCores'
    | 'gpuNodes'
    | 'gpuPeakNode'
    | 'trafficQuota'
    | 'trafficPeak'
    | 'uploadPeakNode'
    | 'downloadPeakNode'
    | 'offlineNodes'
    | 'highLoadNodes'
    | 'expiringNodes'
    | 'trafficWarnings'
    | 'connectionPeakNode'
    | 'regionDistribution'
    | 'systemDistribution'
    | 'virtualizationDistribution'
    | 'monthlyCost'
    | 'yearlyCost'

export type HomeQuickControlKey
  = | 'favorite'
    | 'monthlyCost'
    | 'totalTraffic'
    | 'upload'
    | 'download'
    | 'peak'
    | 'offline'
    | 'highLoad'
    | 'expiring'

export type DetailMetricCardKey
  = | 'nodePrice'
    | 'monthlyCost'
    | 'remainingTime'
    | 'remainingValue'
    | 'cpuUsage'
    | 'gpuUsage'
    | 'memoryUsage'
    | 'swapUsage'
    | 'diskUsage'
    | 'load'
    | 'temperature'
    | 'processes'
    | 'connections'
    | 'uptime'
    | 'uploadSpeed'
    | 'downloadSpeed'
    | 'totalTraffic'
    | 'trafficQuota'

export type NodeListMetadataField
  = | 'provider'
    | 'region'
    | 'city'
    | 'asn'
    | 'tags'
    | 'group'

type GeneralCardPreset = 'official' | 'basic' | 'ops' | 'resource' | 'finance' | 'traffic' | 'gpu' | 'asset' | 'full' | 'custom'
type HomeQuickControlPreset = 'basic' | 'traffic' | 'ops' | 'full' | 'custom'
type DetailMetricCardPreset = 'finance' | 'status' | 'resource' | 'network' | 'gpu' | 'full' | 'custom'
type ChartDashboardPreset = 'all' | 'compact' | 'resource' | 'network' | 'gpu' | 'latency' | 'ops' | 'full' | 'custom' | 'advanced'
type Lang = 'zh-CN' | 'en-US'
type NodeViewMode = 'card' | 'list'
type NodeCardSize = 'mini' | 'compact' | 'comfortable' | 'large'
type RpcTransportMode = 'websocket' | 'http'
type EarthRenderer = 'realistic' | 'cobe' | 'tiled'
type GlassColorPreset = 'emerald' | 'soft' | 'contrast' | 'midnight' | 'custom'
type ColorVisionMode = 'default' | 'accessible'
export type ChartDashboardCardKey
  = | 'cpu'
    | 'memory'
    | 'disk'
    | 'network'
    | 'traffic'
    | 'gpu'
    | 'gpuMemory'
    | 'temperature'
    | 'connections'
    | 'process'
    | 'ping'
    | 'pingLoss'

export interface GlassCustomColors {
  lightCard: string
  lightControl: string
  lightText: string
  lightMutedText: string
  lightBorder: string
  darkCard: string
  darkControl: string
  darkText: string
  darkMutedText: string
  darkBorder: string
}

export interface ChartDashboardTemplate {
  cards: ChartDashboardCardKey[]
}

type ThemeSettings = Record<string, unknown>

/** 固定的字节精度配置 */
const BYTE_DECIMALS: ByteDecimalsConfig = {
  B: 0,
  KB: 0,
  MB: 1,
  GB: 1,
  TB: 2,
}

const DEFAULT_GENERAL_CARD_ORDER: GeneralCardKey[] = [
  'memory',
  'disk',
  'remainingValue',
  'totalTraffic',
  'uploadSpeed',
  'downloadSpeed',
]

const ALL_GENERAL_CARD_KEYS = [
  'currentTime',
  'memory',
  'disk',
  'remainingValue',
  'monthlyCost',
  'totalTraffic',
  'uploadSpeed',
  'downloadSpeed',
  'onlineNodes',
  'offlineNodes',
  'avgCpu',
  'avgGpu',
  'avgLoad',
  'swap',
  'processes',
  'connections',
  'cpuCores',
  'gpuNodes',
  'gpuPeakNode',
  'trafficQuota',
  'trafficPeak',
  'uploadPeakNode',
  'downloadPeakNode',
  'highLoadNodes',
  'expiringNodes',
  'trafficWarnings',
  'connectionPeakNode',
  'regionDistribution',
  'systemDistribution',
  'virtualizationDistribution',
  'yearlyCost',
] as const satisfies readonly GeneralCardKey[]

const DEFAULT_GENERAL_CARD_ENABLED: Record<GeneralCardKey, boolean> = {
  currentTime: false,
  memory: true,
  disk: true,
  remainingValue: true,
  totalTraffic: true,
  uploadSpeed: true,
  downloadSpeed: true,
  onlineNodes: false,
  avgCpu: false,
  avgGpu: false,
  avgLoad: false,
  swap: false,
  processes: false,
  connections: false,
  cpuCores: false,
  gpuNodes: false,
  gpuPeakNode: false,
  trafficQuota: false,
  trafficPeak: false,
  uploadPeakNode: false,
  downloadPeakNode: false,
  offlineNodes: false,
  highLoadNodes: false,
  expiringNodes: false,
  trafficWarnings: false,
  connectionPeakNode: false,
  regionDistribution: false,
  systemDistribution: false,
  virtualizationDistribution: false,
  monthlyCost: false,
  yearlyCost: false,
}

const LEGACY_GENERAL_CARD_SETTING_KEYS: Partial<Record<GeneralCardKey, string>> = {
  memory: 'generalCardMemoryEnabled',
  disk: 'generalCardDiskEnabled',
  remainingValue: 'generalCardRemainingValueEnabled',
  totalTraffic: 'generalCardTotalTrafficEnabled',
  uploadSpeed: 'generalCardUploadSpeedEnabled',
  downloadSpeed: 'generalCardDownloadSpeedEnabled',
  onlineNodes: 'generalCardOnlineNodesEnabled',
  avgCpu: 'generalCardAvgCpuEnabled',
  avgLoad: 'generalCardAvgLoadEnabled',
  swap: 'generalCardSwapEnabled',
  processes: 'generalCardProcessesEnabled',
  connections: 'generalCardConnectionsEnabled',
  cpuCores: 'generalCardCpuCoresEnabled',
  trafficQuota: 'generalCardTrafficQuotaEnabled',
}

const DEFAULT_HOME_QUICK_CONTROL_ORDER: HomeQuickControlKey[] = [
  'favorite',
  'monthlyCost',
  'totalTraffic',
  'peak',
  'offline',
  'highLoad',
  'expiring',
]

const ALL_HOME_QUICK_CONTROL_KEYS = [
  ...DEFAULT_HOME_QUICK_CONTROL_ORDER,
  'upload',
  'download',
] as const satisfies readonly HomeQuickControlKey[]

const DEFAULT_NODE_LIST_METADATA_FIELDS: NodeListMetadataField[] = [
  'provider',
  'region',
  'asn',
]

const DEFAULT_CHART_DASHBOARD_CARDS: ChartDashboardCardKey[] = ['cpu', 'memory', 'disk', 'network', 'gpu', 'connections', 'process']
const ALL_CHART_DASHBOARD_CARDS = [
  ...DEFAULT_CHART_DASHBOARD_CARDS,
  'traffic',
  'gpuMemory',
  'temperature',
  'ping',
  'pingLoss',
] as const satisfies readonly ChartDashboardCardKey[]

const DEFAULT_DETAIL_METRIC_CARD_ORDER: DetailMetricCardKey[] = [
  'nodePrice',
  'monthlyCost',
  'remainingTime',
  'remainingValue',
]

const ALL_DETAIL_METRIC_CARD_KEYS = [
  ...DEFAULT_DETAIL_METRIC_CARD_ORDER,
  'cpuUsage',
  'gpuUsage',
  'memoryUsage',
  'swapUsage',
  'diskUsage',
  'load',
  'temperature',
  'processes',
  'connections',
  'uptime',
  'uploadSpeed',
  'downloadSpeed',
  'totalTraffic',
  'trafficQuota',
] as const satisfies readonly DetailMetricCardKey[]

const ALL_NODE_LIST_METADATA_FIELDS = [
  ...DEFAULT_NODE_LIST_METADATA_FIELDS,
  'tags',
  'group',
] as const satisfies readonly NodeListMetadataField[]

const GENERAL_CARD_PRESETS: Record<GeneralCardPreset, GeneralCardKey[]> = {
  official: [
    'currentTime',
    'onlineNodes',
    'regionDistribution',
    'totalTraffic',
    'uploadSpeed',
    'downloadSpeed',
  ],
  basic: DEFAULT_GENERAL_CARD_ORDER,
  ops: [
    'onlineNodes',
    'offlineNodes',
    'highLoadNodes',
    'trafficWarnings',
    'avgCpu',
    'avgLoad',
  ],
  resource: [
    'avgCpu',
    'avgLoad',
    'memory',
    'disk',
    'swap',
    'cpuCores',
  ],
  finance: [
    'remainingValue',
    'monthlyCost',
    'yearlyCost',
    'expiringNodes',
    'totalTraffic',
    'trafficQuota',
  ],
  traffic: [
    'totalTraffic',
    'trafficQuota',
    'uploadSpeed',
    'downloadSpeed',
    'trafficPeak',
    'trafficWarnings',
  ],
  gpu: [
    'gpuNodes',
    'avgGpu',
    'gpuPeakNode',
    'avgCpu',
    'memory',
    'trafficPeak',
  ],
  asset: [
    'onlineNodes',
    'regionDistribution',
    'systemDistribution',
    'virtualizationDistribution',
    'cpuCores',
    'gpuNodes',
  ],
  full: [...ALL_GENERAL_CARD_KEYS],
  custom: DEFAULT_GENERAL_CARD_ORDER,
}

const HOME_QUICK_CONTROL_PRESETS: Record<HomeQuickControlPreset, HomeQuickControlKey[]> = {
  basic: ['favorite', 'monthlyCost', 'peak', 'offline'],
  traffic: ['favorite', 'totalTraffic', 'peak'],
  ops: ['favorite', 'monthlyCost', 'offline', 'highLoad', 'expiring'],
  full: DEFAULT_HOME_QUICK_CONTROL_ORDER,
  custom: DEFAULT_HOME_QUICK_CONTROL_ORDER,
}

const DETAIL_METRIC_CARD_PRESETS: Record<DetailMetricCardPreset, DetailMetricCardKey[]> = {
  finance: ['nodePrice', 'monthlyCost', 'remainingTime', 'remainingValue', 'totalTraffic', 'trafficQuota', 'uptime', 'connections'],
  status: ['cpuUsage', 'memoryUsage', 'diskUsage', 'load', 'temperature', 'uptime', 'processes', 'connections'],
  resource: ['cpuUsage', 'gpuUsage', 'memoryUsage', 'swapUsage', 'diskUsage', 'load', 'temperature', 'processes', 'connections', 'uptime', 'uploadSpeed', 'downloadSpeed'],
  network: ['uploadSpeed', 'downloadSpeed', 'totalTraffic', 'trafficQuota', 'connections', 'processes', 'uptime', 'remainingTime'],
  gpu: ['gpuUsage', 'cpuUsage', 'memoryUsage', 'temperature', 'load', 'processes', 'connections', 'uptime'],
  full: ['nodePrice', 'monthlyCost', 'remainingTime', 'remainingValue', 'cpuUsage', 'gpuUsage', 'memoryUsage', 'swapUsage', 'diskUsage', 'load', 'temperature', 'processes', 'connections', 'uploadSpeed', 'downloadSpeed', 'totalTraffic'],
  custom: DEFAULT_DETAIL_METRIC_CARD_ORDER,
}

const CHART_DASHBOARD_PRESETS: Record<Exclude<ChartDashboardPreset, 'advanced'>, ChartDashboardCardKey[]> = {
  all: DEFAULT_CHART_DASHBOARD_CARDS,
  compact: ['cpu', 'memory', 'network'],
  resource: ['cpu', 'memory', 'disk', 'temperature', 'process'],
  network: ['network', 'traffic', 'connections'],
  gpu: ['gpu', 'gpuMemory', 'temperature', 'cpu', 'memory'],
  latency: ['ping', 'pingLoss', 'network'],
  ops: ['cpu', 'memory', 'disk', 'network', 'temperature', 'connections', 'process', 'ping', 'pingLoss'],
  full: ['cpu', 'memory', 'disk', 'network', 'traffic', 'gpu', 'gpuMemory', 'temperature', 'connections', 'process', 'ping', 'pingLoss'],
  custom: DEFAULT_CHART_DASHBOARD_CARDS,
}

const GENERAL_CARD_PRESET_ALIASES: Record<string, GeneralCardPreset> = {
  official: 'official',
  官方: 'official',
  basic: 'basic',
  基础: 'basic',
  ops: 'ops',
  运维: 'ops',
  resource: 'resource',
  资源: 'resource',
  finance: 'finance',
  财务: 'finance',
  traffic: 'traffic',
  流量: 'traffic',
  gpu: 'gpu',
  GPU: 'gpu',
  asset: 'asset',
  资产: 'asset',
  full: 'full',
  完整: 'full',
  custom: 'custom',
  自定义: 'custom',
}

const GENERAL_CARD_SLOT_COUNT = 8
const GENERAL_CARD_LABEL_ALIASES: Record<string, GeneralCardKey> = {
  当前时间: 'currentTime',
  内存用量: 'memory',
  硬盘用量: 'disk',
  剩余价值: 'remainingValue',
  累计流量: 'totalTraffic',
  实时上行: 'uploadSpeed',
  实时下行: 'downloadSpeed',
  在线节点: 'onlineNodes',
  离线节点: 'offlineNodes',
  平均CPU: 'avgCpu',
  平均GPU: 'avgGpu',
  平均负载: 'avgLoad',
  交换内存: 'swap',
  进程总数: 'processes',
  连接数: 'connections',
  CPU核心: 'cpuCores',
  GPU节点: 'gpuNodes',
  GPU峰值: 'gpuPeakNode',
  流量配额: 'trafficQuota',
  实时峰值: 'trafficPeak',
  上行最高: 'uploadPeakNode',
  下行最高: 'downloadPeakNode',
  高负载节点: 'highLoadNodes',
  即将到期: 'expiringNodes',
  流量预警: 'trafficWarnings',
  连接峰值: 'connectionPeakNode',
  地区分布: 'regionDistribution',
  系统分布: 'systemDistribution',
  虚拟化分布: 'virtualizationDistribution',
  月费用估算: 'monthlyCost',
  年费用估算: 'yearlyCost',
}

const DETAIL_METRIC_CARD_SLOT_COUNT = 8
const DETAIL_METRIC_CARD_PRESET_ALIASES: Record<string, DetailMetricCardPreset> = {
  finance: 'finance',
  财务: 'finance',
  status: 'status',
  状态: 'status',
  resource: 'resource',
  资源: 'resource',
  network: 'network',
  网络: 'network',
  gpu: 'gpu',
  GPU: 'gpu',
  full: 'full',
  综合: 'full',
  custom: 'custom',
  自定义: 'custom',
}

const DETAIL_METRIC_CARD_LABEL_ALIASES: Record<string, DetailMetricCardKey> = {
  节点价格: 'nodePrice',
  月均支出: 'monthlyCost',
  剩余时间: 'remainingTime',
  剩余价值: 'remainingValue',
  CPU使用率: 'cpuUsage',
  GPU使用率: 'gpuUsage',
  内存使用率: 'memoryUsage',
  交换内存使用率: 'swapUsage',
  硬盘使用率: 'diskUsage',
  系统负载: 'load',
  系统温度: 'temperature',
  进程数: 'processes',
  连接数: 'connections',
  运行时间: 'uptime',
  实时上行: 'uploadSpeed',
  实时下行: 'downloadSpeed',
  累计流量: 'totalTraffic',
  流量配额: 'trafficQuota',
}

const CHART_DASHBOARD_SLOT_COUNT = 7
const CHART_DASHBOARD_PRESET_ALIASES: Record<string, ChartDashboardPreset> = {
  all: 'all',
  默认: 'all',
  compact: 'compact',
  精简: 'compact',
  resource: 'resource',
  资源: 'resource',
  network: 'network',
  网络: 'network',
  gpu: 'gpu',
  GPU: 'gpu',
  latency: 'latency',
  延迟: 'latency',
  ops: 'ops',
  运维: 'ops',
  full: 'full',
  完整: 'full',
  custom: 'custom',
  自定义: 'custom',
  advanced: 'advanced',
  高级JSON: 'advanced',
}

const CHART_DASHBOARD_LABEL_ALIASES: Record<string, ChartDashboardCardKey> = {
  CPU: 'cpu',
  内存: 'memory',
  硬盘: 'disk',
  网络: 'network',
  流量: 'traffic',
  GPU: 'gpu',
  GPU显存: 'gpuMemory',
  温度: 'temperature',
  连接: 'connections',
  进程: 'process',
  延迟: 'ping',
  丢包: 'pingLoss',
}

const HOME_QUICK_CONTROL_PRESET_ALIASES: Record<string, HomeQuickControlPreset> = {
  basic: 'basic',
  基础: 'basic',
  traffic: 'traffic',
  流量: 'traffic',
  ops: 'ops',
  运维: 'ops',
  full: 'full',
  完整: 'full',
  custom: 'custom',
  自定义: 'custom',
}

const GLASS_COLOR_PRESET_ALIASES: Record<string, GlassColorPreset> = {
  emerald: 'emerald',
  翡翠: 'emerald',
  soft: 'soft',
  柔和: 'soft',
  contrast: 'contrast',
  高对比: 'contrast',
  midnight: 'midnight',
  午夜: 'midnight',
  custom: 'custom',
  自定义: 'custom',
}

const COLOR_VISION_MODE_ALIASES: Record<string, ColorVisionMode> = {
  default: 'default',
  标准: 'default',
  accessible: 'accessible',
  colorblind: 'accessible',
  色觉友好: 'accessible',
}

const DEFAULT_GLASS_CUSTOM_COLORS: GlassCustomColors = {
  lightCard: '#f1f5f9bd',
  lightControl: '#e2e8f0c2',
  lightText: '#14151a',
  lightMutedText: '#3f4552',
  lightBorder: '#cbd5e199',
  darkCard: '#0d111ad9',
  darkControl: '#101624cc',
  darkText: '#f7f8fb',
  darkMutedText: '#d6dae4',
  darkBorder: '#ffffff2e',
}

const HEX_COLOR_REGEX = /^#[0-9a-f]{6}(?:[0-9a-f]{2})?$/i
const KEY_LIST_SEPARATOR_REGEX = /[\s,，;；]+/u
const EMPTY_THEME_SETTINGS: ThemeSettings = {}

function isValidThemeMode(value: unknown): value is ThemeMode {
  return value === 'auto' || value === 'light' || value === 'dark'
}

function isValidManagedThemeMode(value: unknown): value is ManagedThemeMode {
  return value === 'beijing' || value === 'light' || value === 'dark'
}

function getBeijingHour(timestamp: number): number {
  const hour = new Intl.DateTimeFormat('en-US', {
    hour: '2-digit',
    hour12: false,
    timeZone: 'Asia/Shanghai',
  }).format(new Date(timestamp))

  const parsed = Number.parseInt(hour, 10)
  if (!Number.isFinite(parsed))
    return new Date(timestamp).getHours()

  return parsed === 24 ? 0 : parsed
}

function isGeneralCardKey(value: string): value is GeneralCardKey {
  return (ALL_GENERAL_CARD_KEYS as readonly string[]).includes(value)
}

function isDetailMetricCardKey(value: string): value is DetailMetricCardKey {
  return (ALL_DETAIL_METRIC_CARD_KEYS as readonly string[]).includes(value)
}

function isHomeQuickControlKey(value: string): value is HomeQuickControlKey {
  return (ALL_HOME_QUICK_CONTROL_KEYS as readonly string[]).includes(value)
}

function isNodeListMetadataField(value: string): value is NodeListMetadataField {
  return (ALL_NODE_LIST_METADATA_FIELDS as readonly string[]).includes(value)
}

function isChartDashboardCardKey(value: string): value is ChartDashboardCardKey {
  return (ALL_CHART_DASHBOARD_CARDS as readonly string[]).includes(value)
}

function parseGeneralCardPreset(value: unknown): GeneralCardPreset {
  if (typeof value !== 'string')
    return 'basic'

  return GENERAL_CARD_PRESET_ALIASES[value.trim()] ?? 'basic'
}

function parseDetailMetricCardPreset(value: unknown): DetailMetricCardPreset {
  if (typeof value !== 'string')
    return 'finance'

  return DETAIL_METRIC_CARD_PRESET_ALIASES[value.trim()] ?? 'finance'
}

function parseChartDashboardPreset(value: unknown): ChartDashboardPreset {
  if (typeof value !== 'string')
    return 'all'

  return CHART_DASHBOARD_PRESET_ALIASES[value.trim()] ?? 'all'
}

function parseGeneralCardSlots(settings: ThemeSettings): GeneralCardKey[] {
  const keys: GeneralCardKey[] = []
  const seenKeys = new Set<GeneralCardKey>()

  for (let index = 1; index <= GENERAL_CARD_SLOT_COUNT; index += 1) {
    const value = settings[`generalCardSlot${index}`]
    if (typeof value !== 'string')
      continue

    const normalized = value.trim()
    const key = isGeneralCardKey(normalized)
      ? normalized
      : GENERAL_CARD_LABEL_ALIASES[normalized]
    if (!key || seenKeys.has(key))
      continue

    keys.push(key)
    seenKeys.add(key)
  }

  return keys
}

function parseDetailMetricCardSlots(settings: ThemeSettings): DetailMetricCardKey[] {
  const keys: DetailMetricCardKey[] = []
  const seenKeys = new Set<DetailMetricCardKey>()

  for (let index = 1; index <= DETAIL_METRIC_CARD_SLOT_COUNT; index += 1) {
    const value = settings[`detailMetricCardSlot${index}`]
    if (typeof value !== 'string')
      continue

    const normalized = value.trim()
    const key = isDetailMetricCardKey(normalized)
      ? normalized
      : DETAIL_METRIC_CARD_LABEL_ALIASES[normalized]
    if (!key || seenKeys.has(key))
      continue

    keys.push(key)
    seenKeys.add(key)
  }

  return keys
}

function parseChartDashboardSlots(settings: ThemeSettings): ChartDashboardCardKey[] {
  const keys: ChartDashboardCardKey[] = []
  const seenKeys = new Set<ChartDashboardCardKey>()

  for (let index = 1; index <= CHART_DASHBOARD_SLOT_COUNT; index += 1) {
    const value = settings[`chartDashboardSlot${index}`]
    if (typeof value !== 'string')
      continue

    const normalized = value.trim()
    const key = isChartDashboardCardKey(normalized)
      ? normalized
      : CHART_DASHBOARD_LABEL_ALIASES[normalized]
    if (!key || seenKeys.has(key))
      continue

    keys.push(key)
    seenKeys.add(key)
  }

  return keys
}

function parseHomeQuickControlPreset(value: unknown): HomeQuickControlPreset {
  if (typeof value !== 'string')
    return 'full'

  return HOME_QUICK_CONTROL_PRESET_ALIASES[value.trim()] ?? 'full'
}

function normalizeHomeQuickControlOrder(keys: HomeQuickControlKey[]): HomeQuickControlKey[] {
  return [...new Set(keys)]
}

function normalizeThemeSettings(raw: unknown): ThemeSettings {
  if (!raw)
    return EMPTY_THEME_SETTINGS

  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw) as unknown
      return normalizeThemeSettings(parsed)
    }
    catch {
      return EMPTY_THEME_SETTINGS
    }
  }

  if (typeof raw === 'object' && !Array.isArray(raw))
    return raw as ThemeSettings

  return EMPTY_THEME_SETTINGS
}

function parseKeyList<T extends string>(rawValue: unknown, isValid: (value: string) => value is T, fallback: readonly T[]): T[] {
  const parsedKeys: T[] = []
  const seenKeys = new Set<T>()

  const rawItems = Array.isArray(rawValue)
    ? rawValue
    : typeof rawValue === 'string'
      ? rawValue.split(KEY_LIST_SEPARATOR_REGEX)
      : []

  for (const item of rawItems) {
    const key = typeof item === 'string' ? item.trim() : ''
    if (!isValid(key) || seenKeys.has(key))
      continue
    parsedKeys.push(key)
    seenKeys.add(key)
  }

  return parsedKeys.length > 0 ? parsedKeys : [...fallback]
}

function parseChartDashboardTemplate(rawValue: unknown): ChartDashboardTemplate {
  if (!rawValue)
    return { cards: [...DEFAULT_CHART_DASHBOARD_CARDS] }

  let value: unknown = rawValue
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value) as unknown
    }
    catch {
      return { cards: parseKeyList(value, isChartDashboardCardKey, DEFAULT_CHART_DASHBOARD_CARDS) }
    }
  }

  if (!value || typeof value !== 'object' || Array.isArray(value))
    return { cards: [...DEFAULT_CHART_DASHBOARD_CARDS] }

  const record = value as Record<string, unknown>
  return {
    cards: parseKeyList(record.cards ?? record.layout, isChartDashboardCardKey, DEFAULT_CHART_DASHBOARD_CARDS),
  }
}

function readBooleanSetting(settings: ThemeSettings, key: string, fallback: boolean): boolean {
  const value = settings[key]
  return typeof value === 'boolean' ? value : fallback
}

function readNumberSetting(settings: ThemeSettings, key: string, fallback: number, min: number, max: number): number {
  const value = settings[key]
  if (typeof value !== 'number' || !Number.isFinite(value))
    return fallback

  return Math.min(Math.max(value, min), max)
}

function readStringSetting(settings: ThemeSettings, key: string, fallback = ''): string {
  const value = settings[key]
  return typeof value === 'string' ? value.trim() : fallback
}

function resolveBackgroundSource(value: unknown): string {
  if (typeof value !== 'string')
    return ''

  const source = value.trim()
  if (!source.toLowerCase().startsWith('local:'))
    return source

  const segments = source.slice('local:'.length)
    .replaceAll('\\', '/')
    .split('/')
    .filter(Boolean)

  if (segments.length === 0 || segments.some(segment => segment === '.' || segment === '..'))
    return ''

  return `/themes/user-assets/${segments.map(segment => encodeURIComponent(segment)).join('/')}`
}

function readColorSetting(settings: ThemeSettings, key: string, fallback: string): string {
  const trimmed = readStringSetting(settings, key, fallback)
  return HEX_COLOR_REGEX.test(trimmed) ? trimmed : fallback
}

function readColorValue(value: unknown, fallback: string): string {
  if (typeof value !== 'string')
    return fallback
  const trimmed = value.trim()
  return HEX_COLOR_REGEX.test(trimmed) ? trimmed : fallback
}

function parseGlassCustomColors(settings: ThemeSettings): GlassCustomColors {
  const legacyColors: GlassCustomColors = {
    lightCard: readColorSetting(settings, 'glassLightCardColor', DEFAULT_GLASS_CUSTOM_COLORS.lightCard),
    lightControl: readColorSetting(settings, 'glassLightControlColor', DEFAULT_GLASS_CUSTOM_COLORS.lightControl),
    lightText: readColorSetting(settings, 'glassLightTextColor', DEFAULT_GLASS_CUSTOM_COLORS.lightText),
    lightMutedText: readColorSetting(settings, 'glassLightMutedTextColor', DEFAULT_GLASS_CUSTOM_COLORS.lightMutedText),
    lightBorder: readColorSetting(settings, 'glassLightBorderColor', DEFAULT_GLASS_CUSTOM_COLORS.lightBorder),
    darkCard: readColorSetting(settings, 'glassDarkCardColor', DEFAULT_GLASS_CUSTOM_COLORS.darkCard),
    darkControl: readColorSetting(settings, 'glassDarkControlColor', DEFAULT_GLASS_CUSTOM_COLORS.darkControl),
    darkText: readColorSetting(settings, 'glassDarkTextColor', DEFAULT_GLASS_CUSTOM_COLORS.darkText),
    darkMutedText: readColorSetting(settings, 'glassDarkMutedTextColor', DEFAULT_GLASS_CUSTOM_COLORS.darkMutedText),
    darkBorder: readColorSetting(settings, 'glassDarkBorderColor', DEFAULT_GLASS_CUSTOM_COLORS.darkBorder),
  }

  let rawValue = settings.glassCustomColors
  if (typeof rawValue === 'string') {
    try {
      rawValue = JSON.parse(rawValue) as unknown
    }
    catch {
      return legacyColors
    }
  }

  if (!rawValue || typeof rawValue !== 'object' || Array.isArray(rawValue))
    return legacyColors

  const record = rawValue as Record<keyof GlassCustomColors, unknown>
  return {
    lightCard: readColorValue(record.lightCard, legacyColors.lightCard),
    lightControl: readColorValue(record.lightControl, legacyColors.lightControl),
    lightText: readColorValue(record.lightText, legacyColors.lightText),
    lightMutedText: readColorValue(record.lightMutedText, legacyColors.lightMutedText),
    lightBorder: readColorValue(record.lightBorder, legacyColors.lightBorder),
    darkCard: readColorValue(record.darkCard, legacyColors.darkCard),
    darkControl: readColorValue(record.darkControl, legacyColors.darkControl),
    darkText: readColorValue(record.darkText, legacyColors.darkText),
    darkMutedText: readColorValue(record.darkMutedText, legacyColors.darkMutedText),
    darkBorder: readColorValue(record.darkBorder, legacyColors.darkBorder),
  }
}

function parseGlassColorPreset(value: unknown): GlassColorPreset {
  if (typeof value !== 'string')
    return 'emerald'
  return GLASS_COLOR_PRESET_ALIASES[value.trim()] ?? 'emerald'
}

function parseColorVisionMode(value: unknown): ColorVisionMode {
  if (typeof value !== 'string')
    return 'default'
  return COLOR_VISION_MODE_ALIASES[value.trim()] ?? 'default'
}

const useAppStore = defineStore('app', () => {
  const loading = ref<boolean>(true)

  // 使用 VueUse 的 useStorageAsync 实现自动持久化
  const themeMode = useStorageAsync<ThemeMode>('themeMode', 'auto', localStorage)
  const lang = ref<Lang>('zh-CN')
  const publicSettings = ref<PublicSettings>()
  const nodeSelectedGroup = useStorageAsync<string>('nodeSelectedGroup', 'all', localStorage)
  const favoriteNodeIds = useStorageAsync<string[]>('theme:favorite-nodes:v1', [], localStorage)
  const isLoggedIn = ref<boolean>(getAuthSession().authenticated)
  const authStatus = ref(getAuthSession().status)
  const privateFeaturesAllowed = computed(() => authStatus.value === 'authenticated')
  const connectionError = ref<boolean>(false)
  const homeAdvancedToolsVisible = ref(false)
  const favoriteNodeIdSet = computed(() => new Set(
    (Array.isArray(favoriteNodeIds.value) ? favoriteNodeIds.value : [])
      .filter((id): id is string => typeof id === 'string' && Boolean(id.trim())),
  ))

  function isFavoriteNode(uuid: string): boolean {
    return favoriteNodeIdSet.value.has(uuid)
  }

  function toggleFavoriteNode(uuid: string): void {
    const normalized = uuid.trim()
    if (!normalized)
      return
    favoriteNodeIds.value = favoriteNodeIdSet.value.has(normalized)
      ? [...favoriteNodeIdSet.value].filter(id => id !== normalized)
      : [...favoriteNodeIdSet.value, normalized]
  }

  const themeSettings = computed(() => normalizeThemeSettings(publicSettings.value?.theme_settings))
  const visitorAuditSupported = computed(() => typeof publicSettings.value?.visitor_audit_enabled === 'boolean')
  const visitorAuditEnabled = computed(() => publicSettings.value?.visitor_audit_enabled === true)

  // 首页滚动位置记忆
  const homeScrollPosition = ref<number>(0)

  // 使用 null 表示未设置，等待主题配置加载后决定
  const storedViewMode = useStorageAsync<NodeViewMode | null>('nodeViewMode', null, localStorage)

  const beijingTimeTick = ref(Date.now())
  if (typeof window !== 'undefined') {
    window.setInterval(() => {
      beijingTimeTick.value = Date.now()
    }, 60 * 1000)
  }

  // 计算属性：从主题配置获取默认视图模式
  const defaultViewMode = computed<NodeViewMode>(() => {
    const settings = themeSettings.value
    if (typeof settings.defaultViewMode === 'string') {
      const mode = settings.defaultViewMode
      if (mode === 'card' || mode === 'list') {
        return mode
      }
    }
    return 'card'
  })

  // 校验视图模式是否为合法值
  function isValidViewMode(value: string | null): value is NodeViewMode {
    return value === 'card' || value === 'list'
  }

  function isValidNodeCardSize(value: unknown): value is NodeCardSize {
    return value === 'mini' || value === 'compact' || value === 'comfortable' || value === 'large'
  }

  function isValidEarthRenderer(value: unknown): value is EarthRenderer {
    return value === 'realistic' || value === 'cobe' || value === 'tiled'
  }

  const nodeCardSize = computed<NodeCardSize>(() => {
    const settings = themeSettings.value
    if (isValidNodeCardSize(settings.nodeCardSize))
      return settings.nodeCardSize
    return 'compact'
  })

  // 当前实际使用的视图模式
  const nodeViewMode = computed<NodeViewMode>({
    get: () => {
      // 校验 storedViewMode 是否为合法值，非法值时使用默认值
      if (storedViewMode.value !== null && isValidViewMode(storedViewMode.value)) {
        return storedViewMode.value
      }
      return defaultViewMode.value
    },
    set: (val) => {
      storedViewMode.value = val
    },
  })

  // 计算属性：从主题配置获取 RPC 连接模式
  const rpcTransportMode = computed<RpcTransportMode>(() => {
    const settings = themeSettings.value
    if (typeof settings.rpcTransportMode === 'string') {
      const mode = settings.rpcTransportMode
      if (mode === 'websocket' || mode === 'http') {
        return mode
      }
    }
    return 'http'
  })

  // 字节格式化精度（固定配置）
  const byteDecimals: ByteDecimalsConfig = { ...BYTE_DECIMALS }

  // 计算属性：公告配置
  const alertEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'alertEnabled', false))

  const alertTitle = computed<string>(() => {
    const value = themeSettings.value.alertTitle
    return typeof value === 'string' ? value : ''
  })

  const alertContent = computed<string>(() => {
    const value = themeSettings.value.alertContent
    return typeof value === 'string' ? value : ''
  })

  const dataUpdateInterval = computed<number>(() => readNumberSetting(themeSettings.value, 'dataUpdateInterval', 3, 1, 60))

  const stopEarth = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'stopEarth', false))

  const earthRenderer = computed<EarthRenderer>(() => {
    const value = themeSettings.value.earthRenderer
    return isValidEarthRenderer(value) ? value : 'realistic'
  })

  const hideEarth = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'hideEarth', false))

  const hideGeneralCard = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'hideGeneralCard', false))

  const visitorInfoEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'visitorInfoEnabled', true))

  const generalCardEnabledMap = computed<Record<GeneralCardKey, boolean>>(() => {
    const settings = themeSettings.value
    const enabledMap = { ...DEFAULT_GENERAL_CARD_ENABLED }

    for (const key of ALL_GENERAL_CARD_KEYS) {
      const settingKey = LEGACY_GENERAL_CARD_SETTING_KEYS[key]
      if (!settingKey)
        continue

      const value = settings[settingKey]
      if (typeof value === 'boolean')
        enabledMap[key] = value
    }

    return enabledMap
  })

  const generalCardOrder = computed<GeneralCardKey[]>(() => {
    const settings = themeSettings.value
    const hasNewPreset = typeof settings.generalCardPreset === 'string'
    const preset = parseGeneralCardPreset(settings.generalCardPreset)

    if (hasNewPreset) {
      if (preset === 'custom') {
        const advancedKeys = typeof settings.generalCardKeys === 'string'
          ? settings.generalCardKeys.trim()
          : ''
        if (advancedKeys)
          return parseKeyList(advancedKeys, isGeneralCardKey, DEFAULT_GENERAL_CARD_ORDER)

        const slotKeys = parseGeneralCardSlots(settings)
        return slotKeys.length > 0 ? slotKeys : [...DEFAULT_GENERAL_CARD_ORDER]
      }

      return [...GENERAL_CARD_PRESETS[preset]]
    }

    if (typeof settings.generalCardKeys === 'string')
      return parseKeyList(settings.generalCardKeys, isGeneralCardKey, DEFAULT_GENERAL_CARD_ORDER)

    const orderedKeys = parseKeyList(settings.generalCardOrder, isGeneralCardKey, DEFAULT_GENERAL_CARD_ORDER)
    const orderedKeySet = new Set<GeneralCardKey>(orderedKeys)
    for (const key of ALL_GENERAL_CARD_KEYS) {
      if (orderedKeySet.has(key))
        continue
      orderedKeys.push(key)
      orderedKeySet.add(key)
    }

    return orderedKeys.filter(key => generalCardEnabledMap.value[key])
  })

  const homeToolsEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'homeToolsEnabled', true))

  const glassColorPreset = computed<GlassColorPreset>(() => parseGlassColorPreset(themeSettings.value.glassColorPreset))

  const glassCustomColors = computed<GlassCustomColors>(() => parseGlassCustomColors(themeSettings.value))

  const colorVisionMode = computed<ColorVisionMode>(() => parseColorVisionMode(themeSettings.value.colorVisionMode))

  const colorVisionFriendly = computed<boolean>(() => colorVisionMode.value === 'accessible')

  const homeQuickControlsEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'homeQuickControlsEnabled', true))

  const homeQuickControlOrder = computed<HomeQuickControlKey[]>(() => {
    const settings = themeSettings.value
    const preset = parseHomeQuickControlPreset(settings.homeQuickControlPreset)
    if (preset === 'custom') {
      return normalizeHomeQuickControlOrder(
        parseKeyList(settings.homeQuickControlKeys, isHomeQuickControlKey, DEFAULT_HOME_QUICK_CONTROL_ORDER),
      )
    }

    if (typeof settings.homeQuickControlKeys === 'string' && typeof settings.homeQuickControlPreset !== 'string') {
      return normalizeHomeQuickControlOrder(
        parseKeyList(settings.homeQuickControlKeys, isHomeQuickControlKey, DEFAULT_HOME_QUICK_CONTROL_ORDER),
      )
    }

    return normalizeHomeQuickControlOrder([...HOME_QUICK_CONTROL_PRESETS[preset]])
  })

  const nodeListMetadataEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'nodeListMetadataEnabled', true))

  const nodeListMetadataFields = computed<NodeListMetadataField[]>(() => {
    return parseKeyList(themeSettings.value.nodeListMetadataFields, isNodeListMetadataField, DEFAULT_NODE_LIST_METADATA_FIELDS)
  })

  const nodeListCustomTagsVisible = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'nodeListCustomTagsVisible', true))

  const nodeDetailSectionTabsEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'nodeDetailSectionTabsEnabled', false))

  const gpuChartEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'gpuChartEnabled', false))

  const detailMetricCardOrder = computed<DetailMetricCardKey[]>(() => {
    const settings = themeSettings.value
    const preset = parseDetailMetricCardPreset(settings.detailMetricCardPreset)
    if (preset !== 'custom')
      return [...DETAIL_METRIC_CARD_PRESETS[preset]]

    const advancedKeys = typeof settings.detailMetricCardKeys === 'string'
      ? settings.detailMetricCardKeys.trim()
      : ''
    if (advancedKeys)
      return parseKeyList(advancedKeys, isDetailMetricCardKey, DEFAULT_DETAIL_METRIC_CARD_ORDER)

    const slotKeys = parseDetailMetricCardSlots(settings)
    return slotKeys.length > 0 ? slotKeys : [...DEFAULT_DETAIL_METRIC_CARD_ORDER]
  })

  const offlineNodesLast = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'offlineNodesLast', false))

  const homeHighLoadThreshold = computed<number>(() => readNumberSetting(themeSettings.value, 'homeHighLoadThreshold', 80, 1, 100))

  const homeTrafficWarningThreshold = computed<number>(() => readNumberSetting(themeSettings.value, 'homeTrafficWarningThreshold', 80, 1, 100))

  const homeExpiringDays = computed<number>(() => readNumberSetting(themeSettings.value, 'homeExpiringDays', 30, 1, 3650))

  const diskPredictionEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'diskPredictionEnabled', false))

  const diskPredictionThresholdDays = computed<number>(() => readNumberSetting(themeSettings.value, 'diskPredictionThresholdDays', 30, 1, 3650))

  const chartDashboardTemplate = computed<ChartDashboardTemplate>(() => {
    const settings = themeSettings.value

    // 旧配置没有 preset 字段时继续以原 JSON/key 列表为准。
    if (typeof settings.chartDashboardPreset !== 'string')
      return parseChartDashboardTemplate(settings.chartDashboardTemplate)

    const preset = parseChartDashboardPreset(settings.chartDashboardPreset)
    if (preset === 'advanced')
      return parseChartDashboardTemplate(settings.chartDashboardTemplate)

    if (preset === 'custom') {
      // 旧版托管设置使用 7 个独立卡位，升级后优先保留原有顺序。
      const slotKeys = parseChartDashboardSlots(settings)
      if (slotKeys.length > 0)
        return { cards: slotKeys }

      const customKeys = typeof settings.chartDashboardTemplate === 'string'
        ? settings.chartDashboardTemplate.trim()
        : ''
      if (customKeys)
        return parseChartDashboardTemplate(customKeys)

      return { cards: [...DEFAULT_CHART_DASHBOARD_CARDS] }
    }

    return { cards: [...CHART_DASHBOARD_PRESETS[preset]] }
  })

  const hideAdminEntryWhenLoggedOut = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'hideAdminEntryWhenLoggedOut', false))

  const hidePriceWhenLoggedOut = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'hidePriceWhenLoggedOut', false))

  const providerAliases = computed<string>(() => readStringSetting(themeSettings.value, 'providerAliases'))

  const exportSecondaryPassword = computed<string>(() => readStringSetting(themeSettings.value, 'exportSecondaryPassword'))

  const disablePageAnimation = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'disablePageAnimation', false))

  // 计算属性：自定义背景配置
  const backgroundEnabled = computed<boolean>(() => readBooleanSetting(themeSettings.value, 'backgroundEnabled', false))

  const backgroundType = computed<'image' | 'video'>(() => {
    const settings = themeSettings.value
    if (typeof settings.backgroundType === 'string') {
      const type = settings.backgroundType
      if (type === 'image' || type === 'video') {
        return type
      }
    }
    return 'image'
  })

  const lightBackgroundUrl = computed<string>(() => {
    return resolveBackgroundSource(themeSettings.value.lightBackgroundUrl)
  })

  const darkBackgroundUrl = computed<string>(() => {
    return resolveBackgroundSource(themeSettings.value.darkBackgroundUrl)
  })

  const backgroundBlur = computed<number>(() => readNumberSetting(themeSettings.value, 'backgroundBlur', 0, 0, Number.MAX_SAFE_INTEGER))

  const backgroundOverlay = computed<number>(() => readNumberSetting(themeSettings.value, 'backgroundOverlay', 0, -100, 100))

  // 当 publicSettings 加载后，如果 localStorage 没有保存过视图模式或值为非法值，使用默认值
  watch(publicSettings, (settings) => {
    if (settings && !isValidViewMode(storedViewMode.value)) {
      // 触发 computed setter，会自动保存到 localStorage
      storedViewMode.value = defaultViewMode.value
    }
  }, { immediate: true })

  watch(themeMode, (mode) => {
    if (!isValidThemeMode(mode)) {
      themeMode.value = 'auto'
    }
  }, { immediate: true })

  const managedThemeMode = computed<ManagedThemeMode>(() => {
    const value = themeSettings.value.themeMode
    return isValidManagedThemeMode(value) ? value : 'beijing'
  })

  const isBeijingDaytime = computed<boolean>(() => {
    const hour = getBeijingHour(beijingTimeTick.value)
    return hour >= 7 && hour < 19
  })

  // 计算当前是否为暗色模式。本机按钮选择 auto 时跟随后台托管设置；手动选择浅色/深色时仅覆盖当前浏览器。
  const isDark = computed(() => {
    const localMode = isValidThemeMode(themeMode.value) ? themeMode.value : 'auto'
    if (localMode === 'light')
      return false
    if (localMode === 'dark')
      return true

    if (managedThemeMode.value === 'beijing')
      return !isBeijingDaytime.value

    return managedThemeMode.value === 'dark'
  })

  const resolvedThemeMode = computed<'light' | 'dark'>(() => isDark.value ? 'dark' : 'light')

  // 计算属性：当前主题模式下的背景 URL
  const currentBackgroundUrl = computed<string>(() => {
    if (resolvedThemeMode.value === 'dark') {
      return darkBackgroundUrl.value
    }
    return lightBackgroundUrl.value
  })

  function updateThemeMode(mode?: ThemeMode) {
    if (mode) {
      themeMode.value = isValidThemeMode(mode) ? mode : 'auto'
      return
    }

    const nextMode: Record<ThemeMode, ThemeMode> = {
      auto: 'light',
      light: 'dark',
      dark: 'auto',
    }

    const currentMode = isValidThemeMode(themeMode.value) ? themeMode.value : 'auto'
    themeMode.value = nextMode[currentMode]
  }

  function syncAuthState() {
    const session = getAuthSession()
    isLoggedIn.value = session.authenticated
    authStatus.value = session.status
    return session
  }

  function updateLoginState(loggedIn: boolean, user?: MeInfo | null) {
    setAuthSessionFromLogin(loggedIn, user ?? null)
    syncAuthState()
  }

  async function verifyLoginState(options?: VerifyLoginOptions): Promise<boolean> {
    await verifyLogin(options)
    return syncAuthState().authenticated
  }

  async function requireLoginPermission(permission: PermissionKey, options?: VerifyLoginOptions): Promise<boolean> {
    const result = await requirePermission(permission, options)
    syncAuthState()
    return result.granted
  }

  return {
    loading,
    themeMode,
    managedThemeMode,
    isBeijingDaytime,
    isDark,
    resolvedThemeMode,
    lang,
    nodeSelectedGroup,
    favoriteNodeIds,
    favoriteNodeIdSet,
    isFavoriteNode,
    toggleFavoriteNode,
    nodeViewMode,
    defaultViewMode,
    nodeCardSize,
    rpcTransportMode,
    byteDecimals,
    alertEnabled,
    alertTitle,
    alertContent,
    dataUpdateInterval,
    stopEarth,
    earthRenderer,
    hideEarth,
    hideGeneralCard,
    visitorInfoEnabled,
    generalCardEnabledMap,
    generalCardOrder,
    homeToolsEnabled,
    homeAdvancedToolsVisible,
    glassColorPreset,
    glassCustomColors,
    colorVisionMode,
    colorVisionFriendly,
    visitorAuditSupported,
    visitorAuditEnabled,
    homeQuickControlsEnabled,
    homeQuickControlOrder,
    nodeListMetadataEnabled,
    nodeListMetadataFields,
    nodeListCustomTagsVisible,
    nodeDetailSectionTabsEnabled,
    gpuChartEnabled,
    detailMetricCardOrder,
    offlineNodesLast,
    homeHighLoadThreshold,
    homeTrafficWarningThreshold,
    homeExpiringDays,
    diskPredictionEnabled,
    diskPredictionThresholdDays,
    chartDashboardTemplate,
    hideAdminEntryWhenLoggedOut,
    hidePriceWhenLoggedOut,
    providerAliases,
    exportSecondaryPassword,
    disablePageAnimation,
    backgroundEnabled,
    backgroundType,
    lightBackgroundUrl,
    darkBackgroundUrl,
    currentBackgroundUrl,
    backgroundBlur,
    backgroundOverlay,
    isLoggedIn,
    authStatus,
    privateFeaturesAllowed,
    publicSettings,
    connectionError,
    homeScrollPosition,
    updateThemeMode,
    updateLoginState,
    verifyLoginState,
    requireLoginPermission,
  }
})

export { useAppStore }
