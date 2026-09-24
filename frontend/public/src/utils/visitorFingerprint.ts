export interface VisitorSecurityProfile {
  fingerprint_id: string
  platform: string
  vendor: string
  browser_brands: string[]
  mobile: boolean
  languages: string[]
  timezone: string
  timezone_offset: number
  screen: string
  viewport: string
  pixel_ratio: number
  color_depth: number
  hardware_concurrency: number
  device_memory?: number
  touch_points: number
  webdriver: boolean
  cookie_enabled: boolean
  pdf_viewer_enabled?: boolean
  do_not_track: string
  color_scheme: 'dark' | 'light'
  reduced_motion: boolean
  connection?: {
    effective_type?: string
    downlink?: number
    rtt?: number
    save_data?: boolean
  }
  webgl?: {
    vendor: string
    renderer: string
  }
  webrtc: {
    supported: boolean
    candidate_count: number
    candidate_types: string[]
    protocols: string[]
    address_kinds: string[]
    fingerprint_id: string
  }
}

interface NavigatorWithHints extends Navigator {
  deviceMemory?: number
  connection?: {
    effectiveType?: string
    downlink?: number
    rtt?: number
    saveData?: boolean
  }
  userAgentData?: {
    brands?: Array<{ brand: string, version: string }>
    mobile?: boolean
    platform?: string
  }
}

const PROFILE_STRING_LIMIT = 160
const WEBRTC_GATHER_TIMEOUT_MS = 900
const IPV4_ADDRESS_REGEX = /^\d{1,3}(?:\.\d{1,3}){3}$/
const ICE_TYPE_REGEX = / typ ([a-z]+)/i
const ICE_PROTOCOL_REGEX = /candidate:\S+ \d+ (\S+)/i
let profilePromise: Promise<VisitorSecurityProfile> | null = null

function trimProfileString(value: unknown): string {
  return typeof value === 'string' ? value.trim().slice(0, PROFILE_STRING_LIMIT) : ''
}

async function sha256(value: string): Promise<string> {
  if (!globalThis.crypto?.subtle)
    return ''

  const digest = await globalThis.crypto.subtle.digest('SHA-256', new TextEncoder().encode(value))
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')
}

function getAddressKind(address: string): string {
  const normalized = address.toLowerCase()
  if (normalized.endsWith('.local'))
    return 'mdns'
  if (normalized.includes(':'))
    return 'ipv6'
  if (IPV4_ADDRESS_REGEX.test(normalized))
    return 'ipv4'
  return normalized ? 'opaque' : 'unknown'
}

async function collectWebRtcProfile(siteScope: string): Promise<VisitorSecurityProfile['webrtc']> {
  if (typeof RTCPeerConnection === 'undefined') {
    return {
      supported: false,
      candidate_count: 0,
      candidate_types: [],
      protocols: [],
      address_kinds: [],
      fingerprint_id: '',
    }
  }

  const candidateTypes = new Set<string>()
  const protocols = new Set<string>()
  const addressKinds = new Set<string>()
  const candidateSignatures = new Set<string>()
  let peer: RTCPeerConnection | null = null

  try {
    peer = new RTCPeerConnection({ iceServers: [] })
    peer.createDataChannel('visitor-audit')

    const gathered = new Promise<void>((resolve) => {
      if (!peer) {
        resolve()
        return
      }

      peer.onicecandidate = (event) => {
        const candidate = event.candidate
        if (!candidate) {
          resolve()
          return
        }

        const raw = trimProfileString(candidate.candidate)
        const type = trimProfileString(candidate.type || ICE_TYPE_REGEX.exec(raw)?.[1]).toLowerCase()
        const protocol = trimProfileString(candidate.protocol || ICE_PROTOCOL_REGEX.exec(raw)?.[1]).toLowerCase()
        const address = trimProfileString(candidate.address || raw.split(' ')[4])

        if (type)
          candidateTypes.add(type)
        if (protocol)
          protocols.add(protocol)
        addressKinds.add(getAddressKind(address))
        if (raw)
          candidateSignatures.add(`${type || 'unknown'}|${protocol || 'unknown'}|${address || 'unknown'}`)
      }
    })

    const offer = await peer.createOffer()
    await peer.setLocalDescription(offer)
    await Promise.race([
      gathered,
      new Promise<void>(resolve => window.setTimeout(resolve, WEBRTC_GATHER_TIMEOUT_MS)),
    ])
  }
  catch {
  }
  finally {
    peer?.close()
  }

  const fingerprintSource = [...candidateSignatures].sort().join('|')
  return {
    supported: true,
    candidate_count: candidateSignatures.size,
    candidate_types: [...candidateTypes].sort(),
    protocols: [...protocols].sort(),
    address_kinds: [...addressKinds].sort(),
    fingerprint_id: fingerprintSource ? await sha256(`${siteScope}|webrtc|${fingerprintSource}`) : '',
  }
}

function collectWebGlProfile(): VisitorSecurityProfile['webgl'] | undefined {
  try {
    const canvas = document.createElement('canvas')
    const context = canvas.getContext('webgl')
    if (!context)
      return undefined

    const extension = context.getExtension('WEBGL_debug_renderer_info') as {
      UNMASKED_VENDOR_WEBGL: number
      UNMASKED_RENDERER_WEBGL: number
    } | null
    const vendor = trimProfileString(extension
      ? context.getParameter(extension.UNMASKED_VENDOR_WEBGL)
      : context.getParameter(context.VENDOR))
    const renderer = trimProfileString(extension
      ? context.getParameter(extension.UNMASKED_RENDERER_WEBGL)
      : context.getParameter(context.RENDERER))
    return vendor || renderer ? { vendor, renderer } : undefined
  }
  catch {
    return undefined
  }
}

async function buildVisitorSecurityProfile(): Promise<VisitorSecurityProfile> {
  const navigatorWithHints = navigator as NavigatorWithHints
  const siteScope = window.location.origin
  const platform = trimProfileString(navigatorWithHints.userAgentData?.platform || navigator.platform)
  const vendor = trimProfileString(navigator.vendor)
  const browserBrands = (navigatorWithHints.userAgentData?.brands ?? [])
    .map(item => `${trimProfileString(item.brand)} ${trimProfileString(item.version)}`.trim())
    .filter(Boolean)
    .slice(0, 6)
  const languages = (navigator.languages?.length ? navigator.languages : [navigator.language])
    .map(trimProfileString)
    .filter(Boolean)
    .slice(0, 6)
  const timezone = trimProfileString(Intl.DateTimeFormat().resolvedOptions().timeZone)
  const connection = navigatorWithHints.connection
  const webgl = collectWebGlProfile()
  const webrtc = await collectWebRtcProfile(siteScope)
  const fingerprintSource = JSON.stringify({
    siteScope,
    userAgent: navigator.userAgent,
    platform,
    vendor,
    browserBrands,
    mobile: navigatorWithHints.userAgentData?.mobile === true,
    languages,
    timezone,
    timezoneOffset: new Date().getTimezoneOffset(),
    screen: [window.screen.width, window.screen.height, window.screen.colorDepth],
    pixelRatio: window.devicePixelRatio,
    hardwareConcurrency: navigator.hardwareConcurrency,
    deviceMemory: navigatorWithHints.deviceMemory,
    touchPoints: navigator.maxTouchPoints,
    webgl,
  })

  return {
    fingerprint_id: await sha256(fingerprintSource),
    platform,
    vendor,
    browser_brands: browserBrands,
    mobile: navigatorWithHints.userAgentData?.mobile === true,
    languages,
    timezone,
    timezone_offset: new Date().getTimezoneOffset(),
    screen: `${window.screen.width}x${window.screen.height}`,
    viewport: `${window.innerWidth}x${window.innerHeight}`,
    pixel_ratio: Number(window.devicePixelRatio.toFixed(2)),
    color_depth: window.screen.colorDepth,
    hardware_concurrency: navigator.hardwareConcurrency || 0,
    device_memory: navigatorWithHints.deviceMemory,
    touch_points: navigator.maxTouchPoints || 0,
    webdriver: navigator.webdriver === true,
    cookie_enabled: navigator.cookieEnabled,
    pdf_viewer_enabled: navigator.pdfViewerEnabled,
    do_not_track: trimProfileString(navigator.doNotTrack),
    color_scheme: window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light',
    reduced_motion: window.matchMedia('(prefers-reduced-motion: reduce)').matches,
    connection: connection
      ? {
          effective_type: trimProfileString(connection.effectiveType),
          downlink: connection.downlink,
          rtt: connection.rtt,
          save_data: connection.saveData,
        }
      : undefined,
    webgl,
    webrtc,
  }
}

export function getVisitorSecurityProfile(): Promise<VisitorSecurityProfile> {
  profilePromise ??= buildVisitorSecurityProfile()
  return profilePromise
}
