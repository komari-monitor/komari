import type { NodeData } from '@/stores/nodes'
import { isFreeNode } from '@/utils/tagHelper'

export type CurrencyCode = 'CNY' | 'USD' | 'HKD' | 'EUR' | 'GBP' | 'JPY' | 'RUB' | 'CHF' | 'INR' | 'VND' | 'THB' | 'CAD'
export type ExchangeRates = Record<CurrencyCode, number>
export type ExchangeRateSource = 'cache' | 'network' | 'stale-cache' | 'default'

export type MeteredTrafficMode = 'sum' | 'max' | 'up' | 'down'

export interface MeteredEstimateSettings {
  currency: CurrencyCode
  trafficMode: MeteredTrafficMode
  trafficRate: number
  timeRate: number
  manualHours: number
  oneTimeFee: number
}

export interface MeteredEstimate {
  trafficBytes: number
  trafficTiB: number
  runtimeHours: number
  trafficCost: number
  timeCost: number
  oneTimeCost: number
  totalCost: number
}

interface ExchangeRatesCache {
  base: 'CNY'
  date: string
  fetchedAt: number
  rates: Partial<Record<CurrencyCode, number>>
}

const CACHE_KEY = 'komari_finance_exchange_rates_cny_v1'
const OVERRIDES_KEY = 'komari_finance_exchange_rate_overrides_v1'
const FINANCE_CURRENCY_KEY = 'fin_currency'
const EXCLUDE_FREE_KEY = 'fin_exclude_free'
const METERED_SETTINGS_KEY_PREFIX = 'theme:usage-estimator:v1:'
export const MAX_ESTIMATE_INPUT = 1e12
export const SUPPORTED_CURRENCIES: CurrencyCode[] = ['CNY', 'USD', 'HKD', 'EUR', 'GBP', 'JPY', 'RUB', 'CHF', 'INR', 'VND', 'THB', 'CAD']
const MS_PER_DAY = 24 * 60 * 60 * 1000
const LONG_TERM_YEARS = 100
let exchangeRatesInflight: Promise<{ rates: ExchangeRates, source: ExchangeRateSource, updatedAt: number | null }> | null = null

export const DEFAULT_EXCHANGE_RATES: ExchangeRates = {
  CNY: 1,
  USD: 0.142536,
  HKD: 1.108377,
  EUR: 0.12102,
  GBP: 0.105581,
  JPY: 22.231552,
  RUB: 13.5,
  CHF: 0.12,
  INR: 11.8,
  VND: 3500,
  THB: 5.0,
  CAD: 0.19,
}

export const CURRENCY_SYMBOLS: Record<CurrencyCode, string> = {
  CNY: '¥',
  USD: '$',
  HKD: 'HK$',
  EUR: '€',
  GBP: '£',
  JPY: '¥',
  RUB: '₽',
  CHF: '₣',
  INR: '₹',
  VND: '₫',
  THB: '฿',
  CAD: 'CA$',
}

const EXCHANGE_RATE_APIS = [
  {
    url: 'https://open.er-api.com/v6/latest/CNY',
    parse: (data: unknown) => (data as { rates?: unknown }).rates,
  },
  {
    url: 'https://api.frankfurter.app/latest?from=CNY',
    parse: (data: unknown) => (data as { rates?: unknown }).rates,
  },
] as const

async function safeJson(response: Response): Promise<unknown> {
  try {
    return await response.json()
  }
  catch {
    return null
  }
}

export function normalizeCurrency(currency: string | null | undefined): CurrencyCode {
  const value = String(currency || 'CNY').trim().toUpperCase()

  if (value === 'USD' || value === '$')
    return 'USD'
  if (value === 'HKD' || value === 'HK$')
    return 'HKD'
  if (value === 'EUR' || value === '€')
    return 'EUR'
  if (value === 'GBP' || value === '£')
    return 'GBP'
  if (value === 'JPY')
    return 'JPY'
  if (value === 'RUB' || value === '₽')
    return 'RUB'
  if (value === 'CHF' || value === '₣')
    return 'CHF'
  if (value === 'INR' || value === '₹')
    return 'INR'
  if (value === 'VND' || value === '₫')
    return 'VND'
  if (value === 'THB' || value === '฿')
    return 'THB'
  if (value === 'CAD' || value === 'CA$' || value === 'C$' || value === 'CAD$')
    return 'CAD'

  return 'CNY'
}

export function getTodayDateKey(date = new Date()): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function shouldExcludeFreeNodes(): boolean {
  const value = getLocalStorageItem(EXCLUDE_FREE_KEY)
  return value === null ? true : value === 'true'
}

export function getStoredFinanceCurrency(): CurrencyCode {
  return normalizeCurrency(getLocalStorageItem(FINANCE_CURRENCY_KEY) || 'CNY')
}

export function setStoredFinanceCurrency(currency: CurrencyCode): void {
  setLocalStorageItem(FINANCE_CURRENCY_KEY, currency)
}

export function setExcludeFreeNodes(exclude: boolean): void {
  setLocalStorageItem(EXCLUDE_FREE_KEY, String(exclude))
}

export function getStoredMeteredEstimateSettings(nodeUuid: string): MeteredEstimateSettings {
  const defaults: MeteredEstimateSettings = {
    currency: 'USD',
    trafficMode: 'sum',
    trafficRate: 0,
    timeRate: 0,
    manualHours: 0,
    oneTimeFee: 0,
  }

  try {
    const rawValue = getLocalStorageItem(`${METERED_SETTINGS_KEY_PREFIX}${nodeUuid}`)
    if (!rawValue)
      return defaults

    const value = JSON.parse(rawValue) as Partial<Record<keyof MeteredEstimateSettings, unknown>>
    const trafficMode = value.trafficMode === 'max' || value.trafficMode === 'up' || value.trafficMode === 'down'
      ? value.trafficMode
      : 'sum'

    return {
      currency: normalizeCurrency(typeof value.currency === 'string' ? value.currency : defaults.currency),
      trafficMode,
      trafficRate: normalizeOptionalCost(value.trafficRate),
      timeRate: normalizeOptionalCost(value.timeRate),
      manualHours: normalizeOptionalCost(value.manualHours),
      oneTimeFee: normalizeOptionalCost(value.oneTimeFee),
    }
  }
  catch {
    return defaults
  }
}

export function setStoredMeteredEstimateSettings(nodeUuid: string, settings: MeteredEstimateSettings): void {
  if (!nodeUuid.trim())
    return

  setLocalStorageItem(`${METERED_SETTINGS_KEY_PREFIX}${nodeUuid}`, JSON.stringify({
    currency: normalizeCurrency(settings.currency),
    trafficMode: settings.trafficMode,
    trafficRate: normalizeOptionalCost(settings.trafficRate),
    timeRate: normalizeOptionalCost(settings.timeRate),
    manualHours: normalizeOptionalCost(settings.manualHours),
    oneTimeFee: normalizeOptionalCost(settings.oneTimeFee),
  }))
}

export function clearStoredMeteredEstimateSettings(nodeUuid: string): void {
  if (nodeUuid.trim())
    removeLocalStorageItem(`${METERED_SETTINGS_KEY_PREFIX}${nodeUuid}`)
}

export function calculateMeteredEstimate(node: NodeData, settings: MeteredEstimateSettings): MeteredEstimate {
  const normalizeTrafficCounter = (value: unknown) => {
    const numericValue = Number(value)
    if (!Number.isFinite(numericValue) || numericValue <= 0)
      return 0
    return Math.min(numericValue, Number.MAX_SAFE_INTEGER)
  }
  const up = normalizeTrafficCounter(node.net_total_up)
  const down = normalizeTrafficCounter(node.net_total_down)
  let trafficBytes = up + down

  if (settings.trafficMode === 'max')
    trafficBytes = Math.max(up, down)
  else if (settings.trafficMode === 'up')
    trafficBytes = up
  else if (settings.trafficMode === 'down')
    trafficBytes = down

  const trafficTiB = trafficBytes / 1024 ** 4
  const runtimeHours = normalizeOptionalCost(settings.manualHours)
  const trafficCost = trafficTiB * normalizeOptionalCost(settings.trafficRate)
  const timeCost = runtimeHours * normalizeOptionalCost(settings.timeRate)
  const oneTimeCost = normalizeOptionalCost(settings.oneTimeFee)

  return {
    trafficBytes,
    trafficTiB,
    runtimeHours,
    trafficCost,
    timeCost,
    oneTimeCost,
    totalCost: trafficCost + timeCost + oneTimeCost,
  }
}

export function applyExchangeRateOverrides(rates: ExchangeRates): ExchangeRates {
  const overrides = readExchangeRateOverrides()
  return { ...rates, ...overrides, CNY: 1 }
}

export function setExchangeRateOverride(currency: CurrencyCode, value: number): void {
  if (currency === 'CNY' || !Number.isFinite(value) || value <= 0)
    return
  const overrides = readExchangeRateOverrides()
  overrides[currency] = value
  setLocalStorageItem(OVERRIDES_KEY, JSON.stringify(overrides))
}

export function clearExchangeRateOverrides(): void {
  removeLocalStorageItem(OVERRIDES_KEY)
}

export function calculateTotalRemainingValueCNY(
  nodes: NodeData[],
  exchangeRates: ExchangeRates,
  excludeFreeTags = true,
  now = new Date(),
): number {
  return nodes.reduce((sum, node) => {
    if (excludeFreeTags && isFreeNode(node))
      return sum

    return sum + calculateRemainingValueCNY(node, exchangeRates, now)
  }, 0)
}

export function calculateTotalValueCNY(
  nodes: NodeData[],
  exchangeRates: ExchangeRates,
  excludeFreeTags = true,
): number {
  return nodes.reduce((sum, node) => {
    if (excludeFreeTags && isFreeNode(node))
      return sum

    return sum + getPriceCNY(node, exchangeRates)
  }, 0)
}

export function calculateMonthlyCostCNY(
  node: NodeData,
  exchangeRates: ExchangeRates,
): number {
  const priceCNY = getPriceCNY(node, exchangeRates)
  if (priceCNY <= 0)
    return 0

  const billingCycle = Number(node.billing_cycle)
  if (!Number.isFinite(billingCycle) || billingCycle <= 0)
    return priceCNY

  return priceCNY / billingCycle * 30
}

export function calculateTotalMonthlyCostCNY(
  nodes: NodeData[],
  exchangeRates: ExchangeRates,
  excludeFreeTags = true,
): number {
  return nodes.reduce((sum, node) => {
    if (excludeFreeTags && isFreeNode(node))
      return sum

    return sum + calculateMonthlyCostCNY(node, exchangeRates)
  }, 0)
}

export function calculatePeriodCostCNY(
  node: NodeData,
  exchangeRates: ExchangeRates,
  periodDays: number,
): number {
  const monthlyCostCNY = calculateMonthlyCostCNY(node, exchangeRates)
  if (monthlyCostCNY <= 0)
    return 0

  return monthlyCostCNY / 30 * periodDays
}

export function calculateRemainingValueCNY(
  node: NodeData,
  exchangeRates: ExchangeRates,
  now = new Date(),
): number {
  if (!node.expired_at)
    return 0

  const priceCNY = getPriceCNY(node, exchangeRates)
  if (priceCNY <= 0)
    return 0

  const expiredAt = new Date(node.expired_at).getTime()
  if (!Number.isFinite(expiredAt))
    return 0

  const diffMs = expiredAt - now.getTime()
  if (diffMs <= 0)
    return 0

  const diffYears = diffMs / (MS_PER_DAY * 365)
  if (diffYears > LONG_TERM_YEARS)
    return priceCNY

  const billingCycle = Number(node.billing_cycle)
  if (!Number.isFinite(billingCycle) || billingCycle <= 0)
    return priceCNY

  const remainingDays = Math.ceil(diffMs / MS_PER_DAY)
  const fraction = remainingDays / billingCycle
  return priceCNY * fraction
}

export function formatFinanceAmount(amount: number, currency: CurrencyCode): {
  currency: CurrencyCode
  symbol: string
  value: string
} {
  const safeAmount = Number.isFinite(amount) ? amount : 0
  const value = new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: 2,
    minimumFractionDigits: Math.abs(safeAmount) < 100000 ? 2 : 0,
    notation: Math.abs(safeAmount) >= 100000 ? 'compact' : 'standard',
  }).format(safeAmount)

  return {
    currency,
    symbol: CURRENCY_SYMBOLS[currency],
    value,
  }
}

export async function getDailyExchangeRates(): Promise<{
  rates: ExchangeRates
  source: ExchangeRateSource
  updatedAt: number | null
}> {
  const today = getTodayDateKey()
  const cached = readCachedExchangeRates()

  if (cached && cached.date === today) {
    return {
      rates: cached.rates,
      source: 'cache',
      updatedAt: cached.fetchedAt,
    }
  }

  if (!exchangeRatesInflight) {
    exchangeRatesInflight = (async () => {
      const fetchedRates = await fetchExchangeRates()
      if (fetchedRates) {
        const updatedAt = Date.now()
        writeCachedExchangeRates(fetchedRates, today, updatedAt)
        return {
          rates: fetchedRates,
          source: 'network' as const,
          updatedAt,
        }
      }

      if (cached) {
        return {
          rates: cached.rates,
          source: 'stale-cache' as const,
          updatedAt: cached.fetchedAt,
        }
      }

      return {
        rates: DEFAULT_EXCHANGE_RATES,
        source: 'default' as const,
        updatedAt: null,
      }
    })().finally(() => {
      exchangeRatesInflight = null
    })
  }

  return exchangeRatesInflight
}

export function getPriceCNY(node: NodeData, exchangeRates: ExchangeRates): number {
  const price = Number(node.price)
  if (!Number.isFinite(price) || price <= 0)
    return 0

  const currency = normalizeCurrency(node.currency)
  if (currency === 'CNY')
    return price

  return price / exchangeRates[currency]
}

async function fetchExchangeRates(): Promise<ExchangeRates | null> {
  for (const api of EXCHANGE_RATE_APIS) {
    try {
      const response = await fetchWithTimeout(api.url)
      if (!response.ok)
        continue

      const data = await safeJson(response)
      const rates = sanitizeExchangeRates(api.parse(data))
      if (rates)
        return rates
    }
    catch (error) {
      console.warn(`获取汇率失败: ${api.url}`, error)
    }
  }

  return null
}

async function fetchWithTimeout(url: string, timeoutMs = 5000): Promise<Response> {
  const controller = new AbortController()
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs)

  try {
    return await fetch(url, { signal: controller.signal })
  }
  finally {
    window.clearTimeout(timeoutId)
  }
}

function readCachedExchangeRates(): { date: string, rates: ExchangeRates, fetchedAt: number } | null {
  try {
    const rawValue = getLocalStorageItem(CACHE_KEY)
    if (!rawValue)
      return null

    const cache = JSON.parse(rawValue) as ExchangeRatesCache
    const rates = sanitizeExchangeRates(cache.rates)
    if (cache.base !== 'CNY' || !cache.date || !rates)
      return null

    return {
      date: cache.date,
      rates,
      fetchedAt: Number.isFinite(cache.fetchedAt) ? cache.fetchedAt : 0,
    }
  }
  catch {
    return null
  }
}

function writeCachedExchangeRates(rates: ExchangeRates, date: string, fetchedAt: number): void {
  const cache: ExchangeRatesCache = {
    base: 'CNY',
    date,
    fetchedAt,
    rates,
  }
  setLocalStorageItem(CACHE_KEY, JSON.stringify(cache))
}

function sanitizeExchangeRates(rates: unknown): ExchangeRates | null {
  if (!rates || typeof rates !== 'object')
    return null

  const record = rates as Record<string, unknown>
  const result = { CNY: 1 } as ExchangeRates

  for (const currency of SUPPORTED_CURRENCIES) {
    if (currency === 'CNY')
      continue

    const value = Number(record[currency])
    if (!Number.isFinite(value) || value <= 0)
      return null

    result[currency] = value
  }

  return result
}

function readExchangeRateOverrides(): Partial<ExchangeRates> {
  try {
    const rawValue = getLocalStorageItem(OVERRIDES_KEY)
    if (!rawValue)
      return {}
    const record = JSON.parse(rawValue) as Record<string, unknown>
    const result: Partial<ExchangeRates> = {}
    for (const currency of SUPPORTED_CURRENCIES) {
      if (currency === 'CNY')
        continue
      const value = Number(record[currency])
      if (Number.isFinite(value) && value > 0)
        result[currency] = value
    }
    return result
  }
  catch {
    return {}
  }
}

function normalizeOptionalCost(value: unknown): number {
  const amount = Number(value)
  return Number.isFinite(amount) && amount >= 0 && amount <= MAX_ESTIMATE_INPUT ? amount : 0
}

function getLocalStorageItem(key: string): string | null {
  try {
    return localStorage.getItem(key)
  }
  catch {
    return null
  }
}

function setLocalStorageItem(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  }
  catch {
    // 缓存失败不应阻断价值计算，下一次刷新会重新尝试获取汇率。
  }
}

function removeLocalStorageItem(key: string): void {
  try {
    localStorage.removeItem(key)
  }
  catch {
    // 本地覆盖清理失败时继续使用当前内存值。
  }
}
