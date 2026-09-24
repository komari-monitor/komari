import dayjs from 'dayjs'

/** 计费周期类型 */
export type BillingCycleType = 'monthly' | 'quarterly' | 'semi_annual' | 'annual' | 'biennial' | 'triennial' | 'quinquennial' | 'once' | 'custom'

/** 过期状态类型 */
export type ExpireStatus = 'unknown' | 'expired' | 'critical' | 'warning' | 'normal' | 'long_term'

/** 支持的标签颜色 */
export type TagColor
  = | 'ruby'
    | 'gray'
    | 'gold'
    | 'bronze'
    | 'brown'
    | 'yellow'
    | 'amber'
    | 'orange'
    | 'tomato'
    | 'red'
    | 'crimson'
    | 'pink'
    | 'plum'
    | 'purple'
    | 'violet'
    | 'iris'
    | 'indigo'
    | 'blue'
    | 'cyan'
    | 'teal'
    | 'jade'
    | 'green'
    | 'grass'
    | 'lime'
    | 'mint'
    | 'sky'

/** 所有支持的标签颜色列表 */
export const TAG_COLORS = [
  'ruby',
  'gray',
  'gold',
  'bronze',
  'brown',
  'yellow',
  'amber',
  'orange',
  'tomato',
  'red',
  'crimson',
  'pink',
  'plum',
  'purple',
  'violet',
  'iris',
  'indigo',
  'blue',
  'cyan',
  'teal',
  'jade',
  'green',
  'grass',
  'lime',
  'mint',
  'sky',
] as const

/** Radix Themes 颜色到 HEX 的映射（基于 light 模式的 9 色阶） */
export const TAG_COLOR_HEX_MAP: Record<TagColor, string> = {
  ruby: '#E5484D',
  gray: '#8D8D8D',
  gold: '#E5C00D',
  bronze: '#C2853C',
  brown: '#AA6A38',
  yellow: '#F9D400',
  amber: '#F5B21A',
  orange: '#F97316',
  tomato: '#E54D2E',
  red: '#E5484D',
  crimson: '#E93D82',
  pink: '#E24D8C',
  plum: '#A855C2',
  purple: '#8E4EC6',
  violet: '#7C5DFA',
  iris: '#5B5BD6',
  indigo: '#6366F1',
  blue: '#0090FF',
  cyan: '#00A2C7',
  teal: '#12A594',
  jade: '#29A383',
  green: '#30A46C',
  grass: '#46A358',
  lime: '#84CC16',
  mint: '#4FD1C5',
  sky: '#00A6ED',
}

/** 计费周期范围配置（天） */
const BILLING_CYCLE_RANGES: Array<{ type: BillingCycleType, min: number, max: number }> = [
  { type: 'monthly', min: 27, max: 32 },
  { type: 'quarterly', min: 87, max: 95 },
  { type: 'semi_annual', min: 175, max: 185 },
  { type: 'annual', min: 360, max: 370 },
  { type: 'biennial', min: 720, max: 750 },
  { type: 'triennial', min: 1080, max: 1150 },
  { type: 'quinquennial', min: 1800, max: 1850 },
]

/** 过期状态阈值配置（天） */
const EXPIRE_THRESHOLDS = {
  critical: 5, // 5天内过期显示红色
  warning: 10, // 10天内过期显示黄色
  long_term: 36500, // 约100年视为长期
} as const

const TAG_COLOR_SUFFIX_REGEX = /<(\w+)>$/
const TAG_COLOR_SUFFIX_REMOVE_REGEX = /<\w+>$/

/**
 * 解析计费周期类型
 * @param billingCycle 计费周期（天）
 * @returns 计费周期类型
 */
export function parseBillingCycleType(billingCycle: number): BillingCycleType {
  if (billingCycle === -1)
    return 'once'

  for (const range of BILLING_CYCLE_RANGES) {
    if (billingCycle >= range.min && billingCycle <= range.max) {
      return range.type
    }
  }

  return 'custom'
}

/**
 * 获取计费周期的显示文本
 * @param billingCycle 计费周期（天）
 * @param lang 语言
 * @returns 显示文本
 */
export function getBillingCycleText(billingCycle: number, lang: 'zh-CN' | 'en-US' = 'zh-CN'): string {
  const type = parseBillingCycleType(billingCycle)

  const texts: Record<BillingCycleType, Record<'zh-CN' | 'en-US', string>> = {
    monthly: { 'zh-CN': '月', 'en-US': 'Month' },
    quarterly: { 'zh-CN': '季', 'en-US': 'Quarter' },
    semi_annual: { 'zh-CN': '半年', 'en-US': 'Semi-Annual' },
    annual: { 'zh-CN': '年', 'en-US': 'Year' },
    biennial: { 'zh-CN': '两年', 'en-US': 'Biennial' },
    triennial: { 'zh-CN': '三年', 'en-US': 'Triennial' },
    quinquennial: { 'zh-CN': '五年', 'en-US': 'Quinquennial' },
    once: { 'zh-CN': '一次性', 'en-US': 'Once' },
    custom: { 'zh-CN': `${billingCycle} 天`, 'en-US': `${billingCycle} Days` },
  }

  return texts[type][lang]
}

const MS_PER_DAY = 24 * 60 * 60 * 1000

function getExpiryDiffMs(expiredAt: string | number | undefined, now = Date.now()): number | null {
  if (!expiredAt)
    return null

  const expiredDate = dayjs(expiredAt)
  if (!expiredDate.isValid())
    return null

  return expiredDate.valueOf() - now
}

/**
 * 计算距离过期的天数
 * @param expiredAt 过期时间（字符串或时间戳）
 * @returns 距离过期的天数；无效时间返回 0，未来不足一天返回 1
 */
export function getDaysUntilExpired(expiredAt: string | number | undefined): number {
  const diffMs = getExpiryDiffMs(expiredAt)
  if (diffMs === null)
    return 0
  if (diffMs <= 0)
    return Math.floor(diffMs / MS_PER_DAY)

  return Math.ceil(diffMs / MS_PER_DAY)
}

/**
 * 获取过期状态
 * @param expiredAt 过期时间
 * @returns 过期状态
 */
export function getExpireStatus(expiredAt: string | number | undefined): ExpireStatus {
  const diffMs = getExpiryDiffMs(expiredAt)
  if (diffMs === null)
    return 'unknown'
  if (diffMs <= 0)
    return 'expired'

  const days = Math.ceil(diffMs / MS_PER_DAY)
  if (days <= EXPIRE_THRESHOLDS.critical)
    return 'critical'
  if (days <= EXPIRE_THRESHOLDS.warning)
    return 'warning'
  if (days > EXPIRE_THRESHOLDS.long_term)
    return 'long_term'
  return 'normal'
}

/**
 * 获取过期状态的显示颜色（Naive UI 颜色类型）
 * @param status 过期状态
 * @returns Naive UI 颜色类型
 */
export function getExpireStatusColor(status: ExpireStatus): 'error' | 'warning' | 'success' | 'default' {
  switch (status) {
    case 'expired':
    case 'critical':
      return 'error'
    case 'warning':
      return 'warning'
    case 'normal':
    case 'long_term':
      return 'success'
    case 'unknown':
    default:
      return 'default'
  }
}

/**
 * 获取过期状态的 HEX 颜色值
 * @param status 过期状态
 * @returns HEX 颜色值
 */
export function getExpireStatusHexColor(status: ExpireStatus): string {
  switch (status) {
    case 'expired':
    case 'critical':
      return TAG_COLOR_HEX_MAP.tomato
    case 'warning':
      return TAG_COLOR_HEX_MAP.orange
    case 'normal':
      return TAG_COLOR_HEX_MAP.green
    case 'long_term':
    case 'unknown':
    default:
      return TAG_COLOR_HEX_MAP.gray
  }
}

/**
 * 获取过期时间的显示文本
 * @param expiredAt 过期时间
 * @param lang 语言
 * @returns 显示文本
 */
export function getExpireText(expiredAt: string | number | undefined, lang: 'zh-CN' | 'en-US' = 'zh-CN'): string {
  const days = getDaysUntilExpired(expiredAt)
  const status = getExpireStatus(expiredAt)

  if (status === 'unknown') {
    return '-'
  }

  if (status === 'expired') {
    return lang === 'zh-CN' ? '已过期' : 'Expired'
  }

  if (status === 'long_term') {
    return lang === 'zh-CN' ? '长期' : 'Long-term'
  }

  if (lang === 'zh-CN') {
    return `${days} 天`
  }
  return `${days} days`
}

/**
 * 解析带颜色的标签
 * @param tag 标签字符串，支持格式 "文本<颜色>"
 * @returns 解析后的标签对象
 */
export function parseTagWithColor(tag: string): { text: string, color: TagColor | null } {
  const normalizedTag = tag.trim()
  const colorMatch = normalizedTag.match(TAG_COLOR_SUFFIX_REGEX)
  if (colorMatch && colorMatch[1]) {
    const colorCandidate = colorMatch[1].toLowerCase()
    const text = normalizedTag.replace(TAG_COLOR_SUFFIX_REMOVE_REGEX, '').trim()
    if ((TAG_COLORS as readonly string[]).includes(colorCandidate)) {
      return { text, color: colorCandidate as TagColor }
    }
  }
  return { text: normalizedTag, color: null }
}

/**
 * 获取标签颜色对应的 HEX 值
 * @param color 标签颜色
 * @returns HEX 颜色值
 */
export function getTagColorHex(color: TagColor): string {
  return TAG_COLOR_HEX_MAP[color]
}

/**
 * 解析标签字符串为标签列表
 * @param tags 标签字符串，用分号分隔
 * @returns 标签数组
 */
export function parseTags(tags: string | undefined): Array<{ text: string, color: TagColor, hex: string }> {
  if (!tags || tags.trim() === '')
    return []

  const tagList = tags.split(';').map(tag => tag.trim()).filter(Boolean)

  return tagList.map((tag, index) => {
    const { text, color } = parseTagWithColor(tag)
    const defaultColor = TAG_COLORS[index % TAG_COLORS.length] ?? 'blue'
    const resolvedColor = color ?? defaultColor
    return {
      text,
      color: resolvedColor,
      hex: getTagColorHex(resolvedColor),
    }
  })
}

export function isFreePrice(price: number): boolean {
  return price === -1
}

export function hasFreeNodeTag(tags: string | undefined): boolean {
  return parseTags(tags).some(tag => tag.text === '白嫖中')
}

export function isFreeNode(node: { price: number, tags?: string }): boolean {
  return isFreePrice(node.price) || hasFreeNodeTag(node.tags)
}

/**
 * 格式化价格显示
 * @param price 价格
 * @param currency 货币符号
 * @param lang 语言
 * @returns 价格显示文本
 */
export function formatPrice(price: number, currency: string = '￥', lang: 'zh-CN' | 'en-US' = 'zh-CN'): string {
  if (price === 0 || isFreePrice(price))
    return lang === 'zh-CN' ? '免费' : 'Free'
  return `${currency}${price}`
}

/**
 * 格式化价格和计费周期
 * @param price 价格
 * @param billingCycle 计费周期（天）
 * @param currency 货币符号
 * @param lang 语言
 * @returns 完整的价格显示文本
 */
export function formatPriceWithCycle(
  price: number,
  billingCycle: number,
  currency: string = '￥',
  lang: 'zh-CN' | 'en-US' = 'zh-CN',
): string {
  if (isFreePrice(price))
    return formatPrice(price, currency, lang)

  const priceText = formatPrice(price, currency, lang)
  const cycleText = getBillingCycleText(billingCycle, lang)
  return `${priceText} / ${cycleText}`
}

const TRAILING_ZERO_REGEX = /\.?0+$/

/**
 * 计算剩余价值（按剩余天数占计费周期的比例折算）
 * - 价格 <= 0：返回 0
 * - 已过期：返回 0
 * - 一次性 / 未知周期（billingCycle <= 0）：返回全额价格
 * - 提前续费覆盖多个周期时，按全部剩余天数累计价值
 * @param price 价格
 * @param billingCycle 计费周期（天）
 * @param expiredAt 过期时间
 * @returns 剩余价值金额
 */
export function getRemainingValue(
  price: number,
  billingCycle: number,
  expiredAt: string | number | undefined,
): number {
  if (!price || price <= 0)
    return 0

  const status = getExpireStatus(expiredAt)
  if (status === 'unknown' || status === 'expired')
    return 0
  if (status === 'long_term')
    return price

  const days = getDaysUntilExpired(expiredAt)
  if (billingCycle <= 0)
    return price

  const fraction = days / billingCycle
  return price * fraction
}

/**
 * 格式化金额（用于剩余价值显示），自动去除多余的小数 0
 * @param value 金额
 * @param currency 货币符号
 * @returns 金额显示文本，如 "$29.79"、"$0"
 */
export function formatCurrencyValue(value: number, currency: string = '￥'): string {
  const rounded = Math.round(value * 100) / 100
  const text = rounded.toFixed(2).replace(TRAILING_ZERO_REGEX, '')
  return `${currency}${text || '0'}`
}

/**
 * 检查是否有 IPv4
 */
export function hasIPv4(ipv4: string | undefined | null): boolean {
  return !!ipv4 && ipv4.trim() !== ''
}

/**
 * 检查是否有 IPv6
 */
export function hasIPv6(ipv6: string | undefined | null): boolean {
  return !!ipv6 && ipv6.trim() !== ''
}
