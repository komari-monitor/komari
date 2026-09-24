export interface ProviderInfo {
  name: string
  icon: string
}

export type ProviderMatchSource = 'custom-alias' | 'metadata' | 'asn' | 'org' | 'fallback-org'

export interface ProviderMatch extends ProviderInfo {
  source: ProviderMatchSource
  matched?: string
}

export interface ProviderResolveResult {
  primary: ProviderInfo
  seller?: ProviderMatch
  network?: ProviderMatch
  displayName: string
  tooltipLines: string[]
}

interface ProviderEntry extends ProviderInfo {
  keywords: string[]
}

interface ProviderResolveInput {
  metadata?: string
  org?: string
  asn?: string
  customAliases?: string
}

const COMPANY_SUFFIX_REGEX = /\b(?:llc|ltd|limited|inc|incorporated|gmbh|sas|bv|bhd|co|corp|corporation|company|pte|plc|sa|srl|sro|oy|ab|ag|kg|pte\s*ltd|co\s*ltd)\b/g
const ASN_PREFIX_REGEX = /^AS\d+\s*/i
const NON_ALNUM_REGEX = /[^a-z0-9\p{Script=Han}]+/gu
const SPACE_REGEX = /\s+/g
const ASN_DIGITS_REGEX = /\d+/
const CUSTOM_ALIAS_GROUP_SEPARATOR_REGEX = /[;\n]+/

const PROVIDER_DB: ProviderEntry[] = [
  { keywords: ['vultr', 'choopa', 'constant'], name: 'Vultr', icon: 'simple-icons:vultr' },
  { keywords: ['linode', 'akamai'], name: 'Akamai (Linode)', icon: 'simple-icons:linode' },
  { keywords: ['digitalocean', 'digital ocean'], name: 'DigitalOcean', icon: 'simple-icons:digitalocean' },
  { keywords: ['amazon', 'aws', 'amazon web services'], name: 'Amazon AWS', icon: 'simple-icons:amazonaws' },
  { keywords: ['google cloud', 'google', 'gcp'], name: 'Google Cloud', icon: 'simple-icons:googlecloud' },
  { keywords: ['microsoft', 'azure'], name: 'Microsoft Azure', icon: 'simple-icons:microsoftazure' },
  { keywords: ['cloudflare'], name: 'Cloudflare', icon: 'simple-icons:cloudflare' },
  { keywords: ['hetzner'], name: 'Hetzner', icon: 'simple-icons:hetzner' },
  { keywords: ['ovh', 'ovhcloud'], name: 'OVHcloud', icon: 'simple-icons:ovh' },
  { keywords: ['contabo'], name: 'Contabo', icon: 'tabler:server' },
  { keywords: ['oracle'], name: 'Oracle Cloud', icon: 'simple-icons:oracle' },
  { keywords: ['ibm', 'softlayer'], name: 'IBM Cloud', icon: 'simple-icons:ibmcloud' },
  { keywords: ['scaleway', 'iliad', 'online sas'], name: 'Scaleway', icon: 'simple-icons:scaleway' },
  { keywords: ['tencent', 'qcloud', '腾讯云'], name: '腾讯云', icon: 'simple-icons:tencentqq' },
  { keywords: ['alibaba', 'aliyun', '阿里云'], name: '阿里云', icon: 'simple-icons:alibabacloud' },
  { keywords: ['huawei', '华为云'], name: '华为云', icon: 'simple-icons:huawei' },
  { keywords: ['china mobile', 'cmi'], name: 'China Mobile (CMI)', icon: 'tabler:server' },
  { keywords: ['china unicom', 'unicom', 'cuii'], name: 'China Unicom', icon: 'tabler:server' },
  { keywords: ['china telecom', 'chinanet', 'ctg'], name: 'China Telecom', icon: 'tabler:server' },

  { keywords: ['racknerd', 'rack nerd'], name: 'RackNerd', icon: 'tabler:server' },
  { keywords: ['greencloud', 'green cloud', 'greencloudvps'], name: 'GreenCloudVPS', icon: 'tabler:server' },
  { keywords: ['hosthatch', 'host hatch'], name: 'HostHatch', icon: 'tabler:server' },
  { keywords: ['hostdare', 'host dare'], name: 'HostDare', icon: 'tabler:server' },
  { keywords: ['virmach', 'vir mach'], name: 'VirMach', icon: 'tabler:server' },
  { keywords: ['servarica'], name: 'Servarica', icon: 'tabler:server' },
  { keywords: ['bytevirt', 'byte virt'], name: 'ByteVirt', icon: 'tabler:server' },
  { keywords: ['spartanhost', 'spartan host'], name: 'SpartanHost', icon: 'tabler:server' },
  { keywords: ['lightnode', 'light node'], name: 'LightNode', icon: 'tabler:server' },
  { keywords: ['netcup'], name: 'Netcup', icon: 'tabler:server' },
  { keywords: ['time4vps', 'time 4 vps'], name: 'Time4VPS', icon: 'tabler:server' },
  { keywords: ['aeza'], name: 'Aeza', icon: 'tabler:server' },
  { keywords: ['pq.hosting', 'pqhosting', 'pq hosting'], name: 'PQ.Hosting', icon: 'tabler:server' },
  { keywords: ['xtom', 'x tom', 'v.ps', 'vps.hosting'], name: 'V.PS (xTom)', icon: 'tabler:server' },
  { keywords: ['dmit'], name: 'DMIT', icon: 'tabler:server' },
  { keywords: ['akile'], name: 'Akile', icon: 'tabler:server' },
  { keywords: ['misaka'], name: 'Misaka', icon: 'tabler:server' },
  { keywords: ['cloudie'], name: 'Cloudie', icon: 'tabler:server' },
  { keywords: ['gigsgigscloud', 'gigsgigs', 'gigs gigs cloud'], name: 'GigsGigsCloud', icon: 'tabler:server' },
  { keywords: ['evolution host', 'evolutionhost'], name: 'Evolution Host', icon: 'tabler:server' },
  { keywords: ['nexusbytes', 'nexus bytes'], name: 'NexusBytes', icon: 'tabler:server' },
  { keywords: ['hostslick', 'host slick'], name: 'HostSlick', icon: 'tabler:server' },
  { keywords: ['frantech', 'buyvm', 'buy vm'], name: 'BuyVM', icon: 'tabler:server' },
  { keywords: ['bandwagonhost', 'bandwagon host', 'it7'], name: 'BandwagonHost', icon: 'tabler:server' },
  { keywords: ['colocrossing', 'colo crossing'], name: 'ColoCrossing', icon: 'tabler:server' },
  { keywords: ['path.net', 'pathnet'], name: 'Path.net', icon: 'tabler:server' },
  { keywords: ['alice networks', 'alice'], name: 'Alice Networks', icon: 'tabler:server' },
  { keywords: ['psychz'], name: 'Psychz Networks', icon: 'tabler:server' },
  { keywords: ['m247'], name: 'M247', icon: 'tabler:server' },
  { keywords: ['zenlayer'], name: 'Zenlayer', icon: 'tabler:server' },
  { keywords: ['leaseweb'], name: 'Leaseweb', icon: 'tabler:server' },
  { keywords: ['g-core', 'gcore'], name: 'Gcore', icon: 'tabler:server' },
  { keywords: ['kamatera'], name: 'Kamatera', icon: 'tabler:server' },
  { keywords: ['clouvider'], name: 'Clouvider', icon: 'tabler:server' },
  { keywords: ['interserver', 'inter server'], name: 'InterServer', icon: 'tabler:server' },
  { keywords: ['cloudcone', 'cloud cone'], name: 'CloudCone', icon: 'tabler:server' },
  { keywords: ['crunchbits', 'crunch bits'], name: 'Crunchbits', icon: 'tabler:server' },
  { keywords: ['alphavps', 'alpha vps'], name: 'AlphaVPS', icon: 'tabler:server' },
  { keywords: ['webhorizon', 'web horizon'], name: 'WebHorizon', icon: 'tabler:server' },
  { keywords: ['terrahost', 'terra host'], name: 'TerraHost', icon: 'tabler:server' },
  { keywords: ['advin', 'advin servers'], name: 'Advin Servers', icon: 'tabler:server' },
  { keywords: ['datapacket', 'data packet'], name: 'DataPacket', icon: 'tabler:server' },
  { keywords: ['hivelocity', 'hive velocity'], name: 'Hivelocity', icon: 'tabler:server' },
  { keywords: ['worldstream', 'world stream'], name: 'WorldStream', icon: 'tabler:server' },
  { keywords: ['hostpapa'], name: 'HostPapa', icon: 'tabler:server' },
  { keywords: ['hytron', 'hinet'], name: 'Hytron', icon: 'tabler:server' },
  { keywords: ['hurricane electric', 'he.net'], name: 'Hurricane Electric', icon: 'tabler:server' },
]

const ASN_PROVIDER_DB: Record<string, ProviderInfo> = {
  AS36352: { name: 'ColoCrossing', icon: 'tabler:server' },
  AS53667: { name: 'FranTech (BuyVM)', icon: 'tabler:server' },
  AS20473: { name: 'Vultr (Choopa)', icon: 'simple-icons:vultr' },
  AS14061: { name: 'DigitalOcean', icon: 'simple-icons:digitalocean' },
  AS24940: { name: 'Hetzner', icon: 'simple-icons:hetzner' },
  AS16276: { name: 'OVHcloud', icon: 'simple-icons:ovh' },
  AS63949: { name: 'Akamai (Linode)', icon: 'simple-icons:linode' },
  AS13335: { name: 'Cloudflare', icon: 'simple-icons:cloudflare' },
  AS51167: { name: 'Contabo', icon: 'tabler:server' },
  AS9009: { name: 'M247', icon: 'tabler:server' },
  AS8100: { name: 'ColoCrossing', icon: 'tabler:server' },
  AS35916: { name: 'Multacom', icon: 'tabler:server' },
  AS60068: { name: 'Datacamp', icon: 'tabler:server' },
  AS62240: { name: 'Clouvider', icon: 'tabler:server' },
  AS47890: { name: 'UNMANAGED LTD', icon: 'tabler:server' },
  AS212027: { name: 'YxVM', icon: 'tabler:server' },
}

const SOURCE_LABELS: Record<ProviderMatchSource, string> = {
  'custom-alias': '自定义别名',
  'metadata': '节点名称 / 备注 / 标签',
  'asn': 'ASN 映射',
  'org': 'IP 组织名',
  'fallback-org': '原始组织名',
}

interface NormalizedText {
  spaced: string
  compact: string
}

const NORMALIZED_TEXT_CACHE_LIMIT = 1000
const RESOLVE_CACHE_LIMIT = 800
const normalizedTextCache = new Map<string, NormalizedText>()
const customAliasCache = new Map<string, ProviderEntry[]>()
const resolveCache = new Map<string, ProviderResolveResult | null>()

function setBoundedCacheValue<K, V>(cache: Map<K, V>, key: K, value: V, limit: number): void {
  if (cache.size >= limit) {
    const firstKey = cache.keys().next().value as K | undefined
    if (firstKey !== undefined)
      cache.delete(firstKey)
  }
  cache.set(key, value)
}

function normalizeText(value: string): NormalizedText {
  const cached = normalizedTextCache.get(value)
  if (cached)
    return cached

  const spaced = value
    .normalize('NFKC')
    .replace(ASN_PREFIX_REGEX, '')
    .toLowerCase()
    .replace(NON_ALNUM_REGEX, ' ')
    .replace(COMPANY_SUFFIX_REGEX, ' ')
    .replace(SPACE_REGEX, ' ')
    .trim()
  const normalized = { spaced, compact: spaced.replace(SPACE_REGEX, '') }
  setBoundedCacheValue(normalizedTextCache, value, normalized, NORMALIZED_TEXT_CACHE_LIMIT)
  return normalized
}

function matchesKeyword(text: NormalizedText, keyword: string): boolean {
  const normalizedKeyword = normalizeText(keyword)
  return Boolean(
    (normalizedKeyword.spaced && text.spaced.includes(normalizedKeyword.spaced))
    || (normalizedKeyword.compact && text.compact.includes(normalizedKeyword.compact)),
  )
}

function normalizeAsn(asn?: string): string {
  const digits = asn?.match(ASN_DIGITS_REGEX)?.[0]
  return digits ? `AS${digits}` : ''
}

function isSameProvider(a?: ProviderInfo | null, b?: ProviderInfo | null): boolean {
  if (!a || !b)
    return false
  return normalizeText(a.name).compact === normalizeText(b.name).compact
}

function providerFromName(name: string): ProviderInfo {
  const normalizedName = normalizeText(name)
  const matched = PROVIDER_DB.find(provider => normalizeText(provider.name).compact === normalizedName.compact)
  return matched ? { name: matched.name, icon: matched.icon } : { name: name.trim(), icon: 'tabler:server' }
}

function parseCustomAliases(config?: string): ProviderEntry[] {
  const key = config?.trim() ?? ''
  if (!key)
    return []

  const cached = customAliasCache.get(key)
  if (cached)
    return cached

  const entries = key
    .split(CUSTOM_ALIAS_GROUP_SEPARATOR_REGEX)
    .map((group) => {
      const [rawName, rawAliases] = group.split(':')
      const name = rawName?.trim()
      if (!name)
        return null
      const provider = providerFromName(name)
      const aliases = rawAliases?.split(',').map(alias => alias.trim()).filter(Boolean) ?? []
      return { ...provider, keywords: [name, ...aliases] }
    })
    .filter((entry): entry is ProviderEntry => Boolean(entry))

  customAliasCache.set(key, entries)
  return entries
}

function detectProviderInEntries(text: string, entries: ProviderEntry[], source: ProviderMatchSource): ProviderMatch | null {
  if (!text.trim())
    return null

  const normalized = normalizeText(text)
  if (!normalized.spaced)
    return null

  for (const provider of entries) {
    const matched = provider.keywords.find(keyword => matchesKeyword(normalized, keyword))
    if (matched)
      return { name: provider.name, icon: provider.icon, source, matched }
  }

  return null
}

export function detectProvider(text: string): ProviderMatch | null {
  return detectProviderInEntries(text, PROVIDER_DB, 'metadata')
}

export function detectProviderByAsn(asn?: string): ProviderMatch | null {
  const key = normalizeAsn(asn)
  const provider = key ? ASN_PROVIDER_DB[key] : null
  return provider ? { ...provider, source: 'asn', matched: key } : null
}

export function cleanProviderOrg(org: string): string {
  return org.replace(ASN_PREFIX_REGEX, '').trim()
}

export function providerSourceLabel(source: ProviderMatchSource): string {
  return SOURCE_LABELS[source]
}

export function resolveProviderInfo(input: ProviderResolveInput): ProviderResolveResult | null {
  const cacheKey = [input.metadata ?? '', input.org ?? '', input.asn ?? '', input.customAliases ?? ''].join('')
  if (resolveCache.has(cacheKey))
    return resolveCache.get(cacheKey) ?? null

  const customEntries = parseCustomAliases(input.customAliases)
  const seller = input.metadata
    ? detectProviderInEntries(input.metadata, customEntries, 'custom-alias') ?? detectProvider(input.metadata)
    : null
  const byAsn = detectProviderByAsn(input.asn)
  const byOrg = input.org ? detectProviderInEntries(input.org, PROVIDER_DB, 'org') : null
  const fallbackOrg = input.org
    ? (() => {
        const orgName = cleanProviderOrg(input.org ?? '')
        return orgName ? { name: orgName, icon: 'tabler:server', source: 'fallback-org' as const } : null
      })()
    : null
  const network = byAsn ?? byOrg ?? fallbackOrg
  const primary = seller ?? network

  if (!primary) {
    setBoundedCacheValue(resolveCache, cacheKey, null, RESOLVE_CACHE_LIMIT)
    return null
  }

  const shouldShowNetwork = seller && network && !isSameProvider(seller, network)
  const displayName = shouldShowNetwork ? `${seller.name} / ${network.name}` : primary.name
  const tooltipLines: string[] = []

  if (seller) {
    tooltipLines.push(`商家：${seller.name}`)
    tooltipLines.push(`来源：${providerSourceLabel(seller.source)}${seller.matched ? `（${seller.matched}）` : ''}`)
  }
  if (network && (!seller || shouldShowNetwork))
    tooltipLines.push(`网络：${network.name}`)
  if (input.asn)
    tooltipLines.push(`ASN：${input.asn}`)
  if (input.org)
    tooltipLines.push(`Org：${cleanProviderOrg(input.org)}`)

  const result = {
    primary,
    seller: seller ?? undefined,
    network: network ?? undefined,
    displayName,
    tooltipLines,
  }
  setBoundedCacheValue(resolveCache, cacheKey, result, RESOLVE_CACHE_LIMIT)
  return result
}
