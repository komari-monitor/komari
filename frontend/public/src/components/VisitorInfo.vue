<script setup lang="ts">
import { Icon } from '@iconify/vue'
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useAppStore } from '@/stores/app'

interface VisitorData {
  ip: string
  city: string
  region: string
  country: string
  org: string
}

interface VisitorProvider {
  url: string
  normalize: (data: unknown) => VisitorData | null
}

type JsonRecord = Record<string, unknown>

const visitorFetchTimeout = 8000
const mobileScrollIdleDelay = 700
const mobileViewportQuery = '(max-width: 767px)'
const appStore = useAppStore()

const show = ref(false)
const dismissed = ref(false)
const visitorLoading = ref(true)
const visitorFailed = ref(false)
const mobileScrolling = ref(false)
const visitor = ref<VisitorData>({
  ip: '',
  city: '',
  region: '',
  country: '',
  org: '',
})

const visitorProviders: VisitorProvider[] = [
  {
    url: 'https://ipwho.is/',
    normalize: normalizeIpwhoData,
  },
  {
    url: 'https://ipapi.co/json/',
    normalize: normalizeIpapiData,
  },
  {
    url: 'https://api.ip.sb/geoip',
    normalize: normalizeIpSbData,
  },
]

const compactLocation = computed(() => {
  const parts = [visitor.value.city, visitor.value.region].filter(Boolean)
  return parts.join(', ') || visitor.value.country || (visitorLoading.value ? '定位中' : '未知位置')
})

const displayIp = computed(() => visitor.value.ip || (visitorLoading.value ? '获取中' : '未获取到'))
const displayCountry = computed(() => visitor.value.country || (visitorLoading.value ? '定位中' : '未知地区'))
const displayOrg = computed(() => visitor.value.org || (visitorLoading.value ? '正在获取网络信息' : '运营商未知'))
const welcomeLocation = computed(() => visitor.value.city || visitor.value.country || (visitorLoading.value ? 'your network' : 'unknown location'))
const visitorStatusText = computed(() => {
  if (visitorLoading.value)
    return '正在获取访客信息'
  if (visitorFailed.value)
    return '已显示本地设备信息，公网定位暂未返回'
  return `Welcome from ${welcomeLocation.value}!`
})

const windowsPattern = /Windows/i
const macPattern = /Mac/i
const androidPattern = /Android/i
const iosPattern = /iPhone|iPad/i
const edgPattern = /Edg/i
const chromePattern = /Chrome/i
const firefoxPattern = /Firefox/i
const safariPattern = /Safari/i
const macOsPattern = /Mac OS X/i
const linuxPattern = /Linux/i
let mobileViewport: MediaQueryList | null = null
let scrollIdleTimer: number | undefined

function handleScroll(): void {
  if (!mobileViewport?.matches)
    return

  mobileScrolling.value = true
  if (scrollIdleTimer !== undefined)
    window.clearTimeout(scrollIdleTimer)
  scrollIdleTimer = window.setTimeout(() => {
    mobileScrolling.value = false
    scrollIdleTimer = undefined
  }, mobileScrollIdleDelay)
}

onMounted(async () => {
  mobileViewport = window.matchMedia(mobileViewportQuery)
  window.addEventListener('scroll', handleScroll, { passive: true })

  window.setTimeout(() => {
    show.value = true
  }, 600)

  try {
    const data = await fetchVisitorData()
    if (data) {
      visitor.value = data
      return
    }

    visitorFailed.value = true
  }
  finally {
    visitorLoading.value = false
  }
})

onUnmounted(() => {
  window.removeEventListener('scroll', handleScroll)
  if (scrollIdleTimer !== undefined)
    window.clearTimeout(scrollIdleTimer)
})

function dismiss() {
  dismissed.value = true
}

async function fetchVisitorData(): Promise<VisitorData | null> {
  for (const provider of visitorProviders) {
    const data = await fetchProviderData(provider)
    if (data)
      return data
  }

  return null
}

async function fetchProviderData(provider: VisitorProvider): Promise<VisitorData | null> {
  try {
    const data = await fetchJsonWithTimeout(provider.url)
    return provider.normalize(data)
  }
  catch {
    return null
  }
}

async function fetchJsonWithTimeout(url: string): Promise<unknown> {
  const controller = new AbortController()
  const timeoutId = window.setTimeout(() => controller.abort(), visitorFetchTimeout)

  try {
    const response = await fetch(url, {
      signal: controller.signal,
      headers: {
        Accept: 'application/json',
      },
    })

    if (!response.ok)
      throw new Error(`Visitor info request failed: ${response.status}`)

    return await response.json()
  }
  finally {
    window.clearTimeout(timeoutId)
  }
}

function normalizeIpwhoData(data: unknown): VisitorData | null {
  if (!isRecord(data) || data.success === false)
    return null

  const connection = isRecord(data.connection) ? data.connection : {}

  return createVisitorData({
    ip: data.ip,
    city: data.city,
    region: data.region,
    country: data.country,
    org: pickString(connection.org, connection.isp, connection.domain),
  })
}

function normalizeIpapiData(data: unknown): VisitorData | null {
  if (!isRecord(data) || data.error === true)
    return null

  return createVisitorData({
    ip: data.ip,
    city: data.city,
    region: data.region,
    country: data.country_name,
    org: data.org,
  })
}

function normalizeIpSbData(data: unknown): VisitorData | null {
  if (!isRecord(data))
    return null

  return createVisitorData({
    ip: data.ip,
    city: data.city,
    region: data.region,
    country: data.country,
    org: pickString(data.organization, data.isp, data.asn_organization),
  })
}

function createVisitorData(data: Record<keyof VisitorData, unknown>): VisitorData | null {
  const ip = readString(data.ip)
  if (!ip)
    return null

  return {
    ip,
    city: readString(data.city),
    region: readString(data.region),
    country: readString(data.country),
    org: readString(data.org),
  }
}

function isRecord(data: unknown): data is JsonRecord {
  return typeof data === 'object' && data !== null
}

function pickString(...values: unknown[]): string {
  for (const value of values) {
    const text = readString(value)
    if (text)
      return text
  }

  return ''
}

function readString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function getOsIcon(): string {
  const ua = navigator.userAgent
  if (windowsPattern.test(ua))
    return 'icon-park-outline:windows'
  if (macPattern.test(ua))
    return 'icon-park-outline:mac'
  if (androidPattern.test(ua))
    return 'icon-park-outline:android'
  if (iosPattern.test(ua))
    return 'icon-park-outline:apple'
  return 'icon-park-outline:laptop'
}

function getBrowserName(): string {
  const ua = navigator.userAgent
  if (edgPattern.test(ua))
    return 'Edge Browser'
  if (chromePattern.test(ua))
    return 'Chrome Browser'
  if (firefoxPattern.test(ua))
    return 'Firefox Browser'
  if (safariPattern.test(ua))
    return 'Safari Browser'
  return 'Unknown Browser'
}

function getOsName(): string {
  const ua = navigator.userAgent
  if (windowsPattern.test(ua))
    return 'Windows'
  if (macOsPattern.test(ua))
    return 'macOS'
  if (androidPattern.test(ua))
    return 'Android'
  if (iosPattern.test(ua))
    return 'iOS'
  if (linuxPattern.test(ua))
    return 'Linux'
  return 'Unknown OS'
}

function formatDate(): string {
  return new Date().toLocaleDateString('zh-CN', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  })
}

const siteName = computed(() => appStore.privateFeaturesAllowed ? '尊敬的管理员' : '访客')
</script>

<template>
  <!-- 底部居中 IP 条（桌面+手机都显示） -->
  <Transition name="slide-up">
    <div
      v-if="show && !dismissed && !mobileScrolling"
      class="fixed bottom-3 left-1/2 z-50 flex w-max max-w-[calc(100vw-1.5rem)] -translate-x-1/2
             items-center gap-1.5 rounded-full px-3 py-1.5 md:bottom-4 md:gap-2 md:px-4
             bg-white/55 dark:bg-black/50
             backdrop-blur-md
             border border-white/40 dark:border-white/10
             shadow-lg text-[12px] md:text-[13px] select-none whitespace-nowrap"
    >
      <Icon icon="icon-park-outline:earth" :width="14" :height="14" class="text-blue-500 shrink-0" />
      <span class="hidden text-muted-foreground sm:inline">Your IP:</span>
      <span class="min-w-0 truncate font-semibold text-foreground">{{ displayIp }}</span>
      <span class="text-muted-foreground/40 shrink-0">|</span>
      <span class="max-w-20 shrink-0 truncate text-muted-foreground sm:max-w-none">{{ displayCountry }}</span>
      <span class="hidden sm:inline text-muted-foreground/40 shrink-0">|</span>
      <span class="hidden sm:inline text-muted-foreground truncate max-w-[140px] md:max-w-[220px]">{{ displayOrg }}</span>
    </div>
  </Transition>

  <!-- 左下角详情卡片 — 模仿图二样式 -->
  <Transition name="slide-left">
    <div
      v-if="show && !dismissed"
      class="fixed bottom-16 left-3 z-50 hidden w-56 max-w-[calc(100vw-1.5rem)] overflow-hidden rounded-2xl 2xl:block
             bg-white/70 dark:bg-neutral-900/70
             backdrop-blur-xl
             border border-white/40 dark:border-white/10
             shadow-2xl"
    >
      <!-- 顶部：头像 + 名字 + 关闭 -->
      <div class="flex items-center justify-between px-4 pt-4 pb-1">
        <div class="flex items-center gap-2.5">
          <!-- 渐变头像圆 -->
          <div class="size-9 rounded-full bg-gradient-to-br from-violet-400 via-blue-400 to-cyan-400 flex items-center justify-center shrink-0 shadow-md">
            <Icon icon="icon-park-outline:user" :width="18" :height="18" class="text-white" />
          </div>
          <div class="flex flex-col leading-tight">
            <span class="text-[14px] font-bold text-violet-500 dark:text-violet-400">{{ siteName }}</span>
            <span class="text-[11px] text-muted-foreground truncate max-w-[12rem] sm:max-w-32">{{ compactLocation }}</span>
          </div>
        </div>
        <button
          type="button"
          class="size-6 rounded-full flex items-center justify-center
                 hover:bg-black/8 dark:hover:bg-white/10 transition-colors"
          aria-label="关闭访客信息"
          @click="dismiss"
        >
          <Icon icon="icon-park-outline:close" :width="13" :height="13" class="text-muted-foreground" />
        </button>
      </div>

      <!-- Welcome 文字 -->
      <div class="px-4 pb-2">
        <p class="text-[12px] text-foreground/70">
          {{ visitorStatusText }}
        </p>
      </div>

      <!-- 分割线 -->
      <div class="mx-4 border-t border-black/6 dark:border-white/8 mb-2" />

      <!-- 信息行 -->
      <div class="px-4 pb-4 flex flex-col gap-2">
        <div class="flex items-center gap-2.5 text-[12px] text-foreground/75">
          <Icon :icon="getOsIcon()" :width="14" :height="14" class="text-muted-foreground shrink-0" />
          <span>{{ getOsName() }}</span>
        </div>
        <div class="flex items-center gap-2.5 text-[12px] text-foreground/75">
          <Icon icon="icon-park-outline:browser-chrome" :width="14" :height="14" class="text-muted-foreground shrink-0" />
          <span>{{ getBrowserName() }}</span>
        </div>
        <div class="flex items-center gap-2.5 text-[12px] text-foreground/75">
          <Icon icon="icon-park-outline:local" :width="14" :height="14" class="text-blue-500 shrink-0" />
          <span class="font-mono truncate">{{ displayIp }}</span>
        </div>
        <div class="flex items-center gap-2.5 text-[12px] text-foreground/75">
          <Icon icon="icon-park-outline:protect" :width="14" :height="14" class="text-muted-foreground shrink-0" />
          <span class="truncate">{{ displayOrg }}</span>
        </div>
        <div class="flex items-center gap-2.5 text-[12px] text-foreground/75">
          <Icon icon="icon-park-outline:time" :width="14" :height="14" class="text-muted-foreground shrink-0" />
          <span>{{ formatDate() }}</span>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.slide-up-enter-active,
.slide-up-leave-active {
  transition: all 0.4s cubic-bezier(0.34, 1.56, 0.64, 1);
}
.slide-up-enter-from,
.slide-up-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(20px);
}

.slide-left-enter-active,
.slide-left-leave-active {
  transition: all 0.4s cubic-bezier(0.34, 1.56, 0.64, 1);
}
.slide-left-enter-from,
.slide-left-leave-to {
  opacity: 0;
  transform: translateX(-20px);
}
</style>
