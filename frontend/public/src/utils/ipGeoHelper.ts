/**
 * 根据 IP 在线查询地理坐标与城市，用于地球的城市级定位。
 *
 * 多个免费 HTTPS 服务依次回退（ip.sb → ipinfo.io → ipwho.is → ipapi.co），任一返回经纬度即采用；
 * 结果按 IP 缓存到 localStorage（带版本与过期时间），避免重复请求与频率限制。
 * 全部失败时写入短期负缓存，调用方应回落到国家级定位。
 */

export interface IpGeo {
  lat: number
  lng: number
  city?: string
  countryCode?: string
  /** ASN 组织 / ISP 名称（用于识别厂商） */
  org?: string
  /** AS 号，如 "AS401115" */
  asn?: string
}

const CACHE_PREFIX = 'komari-theme-emerald:ipgeo'
const CACHE_VERSION = 3
const CACHE_TTL_MS = 30 * 24 * 60 * 60 * 1000 // 30 天
const NEGATIVE_CACHE_TTL_MS = 6 * 60 * 60 * 1000 // 6 小时
const IP_GEO_TIMEOUT_MS = 5000
const PROVIDER_BACKOFF_BASE_MS = 60 * 1000
const PROVIDER_BACKOFF_MAX_MS = 30 * 60 * 1000
const ASN_ORG_PREFIX_REGEX = /^AS\d+/

interface CacheEntry {
  version: number
  updatedAt: number
  geo: IpGeo | null
  negative?: boolean
}

interface ProviderState {
  failures: number
  blockedUntil: number
}

type Provider = (ip: string) => Promise<IpGeo | null>

interface ProviderEntry {
  id: string
  lookup: Provider
}

function cacheKey(ip: string): string {
  return `${CACHE_PREFIX}:${ip}`
}

function isValidGeo(geo: unknown): geo is IpGeo {
  if (!geo || typeof geo !== 'object')
    return false
  const g = geo as Record<string, unknown>
  return typeof g.lat === 'number' && Number.isFinite(g.lat) && g.lat >= -90 && g.lat <= 90
    && typeof g.lng === 'number' && Number.isFinite(g.lng) && g.lng >= -180 && g.lng <= 180
}

function readCache(ip: string): { geo: IpGeo | null, negative: boolean } | null {
  if (typeof window === 'undefined')
    return null
  try {
    const raw = window.localStorage.getItem(cacheKey(ip))
    if (!raw)
      return null
    const parsed = JSON.parse(raw) as CacheEntry
    if (parsed.version !== CACHE_VERSION)
      return null

    const ttl = parsed.negative ? NEGATIVE_CACHE_TTL_MS : CACHE_TTL_MS
    if (Date.now() - parsed.updatedAt > ttl)
      return null

    if (parsed.negative)
      return { geo: null, negative: true }
    if (!isValidGeo(parsed.geo))
      return null
    return { geo: parsed.geo, negative: false }
  }
  catch {
    return null
  }
}

function writeCache(ip: string, geo: IpGeo): void {
  writeCacheEntry(ip, { version: CACHE_VERSION, updatedAt: Date.now(), geo })
}

function writeNegativeCache(ip: string): void {
  writeCacheEntry(ip, { version: CACHE_VERSION, updatedAt: Date.now(), geo: null, negative: true })
}

function writeCacheEntry(ip: string, entry: CacheEntry): void {
  if (typeof window === 'undefined')
    return
  try {
    window.localStorage.setItem(cacheKey(ip), JSON.stringify(entry))
  }
  catch {
    // 忽略写盘失败（隐私模式 / 配额已满）
  }
}

function toFinite(value: unknown): number | null {
  const n = typeof value === 'string' ? Number.parseFloat(value) : (value as number)
  return typeof n === 'number' && Number.isFinite(n) ? n : null
}

function pickString(...values: unknown[]): string | undefined {
  for (const v of values) {
    if (typeof v === 'string' && v.trim())
      return v.trim()
  }
  return undefined
}

async function safeJson(response: Response): Promise<Record<string, unknown> | null> {
  try {
    const data = await response.json()
    return data && typeof data === 'object' ? data as Record<string, unknown> : null
  }
  catch {
    return null
  }
}

async function fetchWithTimeout(url: string, timeoutMs = IP_GEO_TIMEOUT_MS): Promise<Response> {
  const controller = new AbortController()
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, { signal: controller.signal })
  }
  finally {
    window.clearTimeout(timeoutId)
  }
}

function normalizeIpPath(ip: string): string {
  return encodeURIComponent(ip.trim())
}

/** ip.sb：返回 latitude / longitude / city / country_code */
const fromIpSb: Provider = async (ip) => {
  const res = await fetchWithTimeout(`https://api.ip.sb/geoip/${normalizeIpPath(ip)}`)
  if (!res.ok)
    return null
  const d = await safeJson(res)
  if (!d)
    return null
  const lat = toFinite(d.latitude)
  const lng = toFinite(d.longitude)
  if (lat === null || lng === null)
    return null
  return {
    lat,
    lng,
    city: typeof d.city === 'string' ? d.city : undefined,
    countryCode: typeof d.country_code === 'string' ? d.country_code : undefined,
    org: pickString(d.organization, d.asn_organization, d.isp),
    asn: typeof d.asn === 'number' ? `AS${d.asn}` : pickString(d.asn),
  }
}

/** ipinfo.io：loc = "lat,lng"，city，country，org = "AS#### 组织名" */
const fromIpinfo: Provider = async (ip) => {
  const res = await fetchWithTimeout(`https://ipinfo.io/${normalizeIpPath(ip)}/json`)
  if (!res.ok)
    return null
  const d = await safeJson(res)
  if (!d || typeof d.loc !== 'string')
    return null
  const [latStr, lngStr] = d.loc.split(',')
  const lat = toFinite(latStr)
  const lng = toFinite(lngStr)
  if (lat === null || lng === null)
    return null
  const org = pickString(d.org)
  return {
    lat,
    lng,
    city: typeof d.city === 'string' ? d.city : undefined,
    countryCode: typeof d.country === 'string' ? d.country : undefined,
    org,
    asn: org?.match(ASN_ORG_PREFIX_REGEX)?.[0],
  }
}

/** ipapi.co：latitude / longitude / city / country_code */
const fromIpapiCo: Provider = async (ip) => {
  const res = await fetchWithTimeout(`https://ipapi.co/${normalizeIpPath(ip)}/json/`)
  if (!res.ok)
    return null
  const d = await safeJson(res)
  if (!d)
    return null
  const lat = toFinite(d.latitude)
  const lng = toFinite(d.longitude)
  if (lat === null || lng === null)
    return null
  return {
    lat,
    lng,
    city: typeof d.city === 'string' ? d.city : undefined,
    countryCode: typeof d.country_code === 'string' ? d.country_code : undefined,
    org: pickString(d.org),
    asn: pickString(d.asn),
  }
}

/** ipwho.is：latitude/longitude/city/country_code，connection.{org,isp,asn} */
const fromIpwhois: Provider = async (ip) => {
  const res = await fetchWithTimeout(`https://ipwho.is/${normalizeIpPath(ip)}`)
  if (!res.ok)
    return null
  const d = await safeJson(res)
  if (!d || d.success === false)
    return null
  const lat = toFinite(d.latitude)
  const lng = toFinite(d.longitude)
  if (lat === null || lng === null)
    return null
  const conn = (d.connection ?? {}) as Record<string, unknown>
  return {
    lat,
    lng,
    city: typeof d.city === 'string' ? d.city : undefined,
    countryCode: typeof d.country_code === 'string' ? d.country_code : undefined,
    org: pickString(conn.org, conn.isp),
    asn: typeof conn.asn === 'number' ? `AS${conn.asn}` : pickString(conn.asn),
  }
}

const PROVIDERS: ProviderEntry[] = [
  { id: 'ip.sb', lookup: fromIpSb },
  { id: 'ipinfo.io', lookup: fromIpinfo },
  { id: 'ipwho.is', lookup: fromIpwhois },
  { id: 'ipapi.co', lookup: fromIpapiCo },
]

const providerStates = new Map<string, ProviderState>()

// 轮询起始服务：每次查询从不同站点开始，避免所有请求都打到同一站点导致频控后整体失败
let providerCursor = 0
function orderedProviders(): ProviderEntry[] {
  const n = PROVIDERS.length
  const start = providerCursor % n
  providerCursor = (providerCursor + 1) % n
  const providers = Array.from({ length: n }, (_, i) => PROVIDERS[(start + i) % n]!)
  const now = Date.now()
  const available = providers.filter(provider => (providerStates.get(provider.id)?.blockedUntil ?? 0) <= now)
  return available.length > 0 ? available : providers
}

function markProviderSuccess(id: string): void {
  providerStates.delete(id)
}

function markProviderFailure(id: string): void {
  const current = providerStates.get(id) ?? { failures: 0, blockedUntil: 0 }
  const failures = current.failures + 1
  const backoff = Math.min(PROVIDER_BACKOFF_BASE_MS * 2 ** Math.min(failures - 1, 5), PROVIDER_BACKOFF_MAX_MS)
  providerStates.set(id, { failures, blockedUntil: Date.now() + backoff })
}

// 同一 IP 的并发查询去重
const inflight = new Map<string, Promise<IpGeo | null>>()

/**
 * 查询某个 IP 的地理坐标（含缓存与多服务回退）。失败返回 null。
 */
export async function lookupIpGeo(ip: string): Promise<IpGeo | null> {
  const normalizedIp = ip.trim()
  if (!normalizedIp)
    return null

  const cached = readCache(normalizedIp)
  if (cached)
    return cached.geo

  const existing = inflight.get(normalizedIp)
  if (existing)
    return existing

  const task = (async () => {
    // 从轮询选出的起始站点开始，依次回退，分摊请求压力
    for (const provider of orderedProviders()) {
      try {
        const geo = await provider.lookup(normalizedIp)
        if (geo && isValidGeo(geo)) {
          markProviderSuccess(provider.id)
          writeCache(normalizedIp, geo)
          return geo
        }
        markProviderFailure(provider.id)
      }
      catch {
        markProviderFailure(provider.id)
      }
    }
    writeNegativeCache(normalizedIp)
    return null
  })()

  inflight.set(normalizedIp, task)
  try {
    return await task
  }
  finally {
    inflight.delete(normalizedIp)
  }
}
