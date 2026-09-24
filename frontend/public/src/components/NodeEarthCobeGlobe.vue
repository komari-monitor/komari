<script setup lang="ts">
import type { COBEOptions, Globe, Marker } from 'cobe'
import type { ComponentPublicInstance } from 'vue'
import type { NodeData } from '@/stores/nodes'
import {
  useDocumentVisibility,
  useElementSize,
  useElementVisibility,
  useRafFn,
} from '@vueuse/core'
import createGlobe from 'cobe'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useNodeGeoClusters } from '@/composables/useNodeGeoClusters'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  nodes?: NodeData[]
}>()
const appStore = useAppStore()

const containerRef = ref<HTMLDivElement>()
const canvasRef = ref<HTMLCanvasElement>()
const { width: containerWidth, height: containerHeight } = useElementSize(containerRef)

const documentVisibility = useDocumentVisibility()
const elementVisible = useElementVisibility(containerRef)
const shouldRender = computed(() => documentVisibility.value === 'visible' && elementVisible.value)
const shouldAutoRotate = computed(() => !appStore.stopEarth)

let globe: Globe | null = null
const INITIAL_THETA = 0.22
const MIN_THETA = -0.65
const MAX_THETA = 0.65
const CHINA_COORD: [number, number] = [35.8617, 104.1954]
const DEFAULT_PHI = normalizePhi(-Math.PI / 2 - CHINA_COORD[1] * Math.PI / 180)
let phi = DEFAULT_PHI
let targetPhi = phi
let theta = INITIAL_THETA
let targetTheta = INITIAL_THETA
let isPointerDown = false
let lastPointerX = 0
let lastPointerY = 0
let staticRedrawUntil = 0
const labelElements = new Map<string, HTMLElement>()

function normalizePhi(value: number): number {
  const circle = Math.PI * 2
  let next = value % circle
  if (next <= -Math.PI)
    next += circle
  if (next > Math.PI)
    next -= circle
  return next
}

function clampTheta(value: number): number {
  return Math.min(Math.max(value, MIN_THETA), MAX_THETA)
}

function resetStoppedView() {
  phi = DEFAULT_PHI
  targetPhi = DEFAULT_PHI
  theta = INITIAL_THETA
  targetTheta = INITIAL_THETA
}

function triggerStaticRedrawWindow(duration = 1500) {
  if (typeof performance === 'undefined') {
    staticRedrawUntil = Date.now() + duration
    return
  }
  staticRedrawUntil = performance.now() + duration
}

function shouldKeepStaticRedraw(): boolean {
  const now = typeof performance === 'undefined' ? Date.now() : performance.now()
  return now < staticRedrawUntil
}

const {
  regionClusters,
  totalServers,
  onlineServers,
  offlineServers,
  clusterKey,
} = useNodeGeoClusters({ nodes: () => props.nodes })

function markerId(code: string): string {
  return `cdn-${code.toLowerCase()}`
}

const markers = computed<Marker[]>(() => {
  return regionClusters.value.map(cluster => ({
    id: markerId(cluster.id),
    location: cluster.coord,
    size: 0,
  }))
})

function getClusterStyle(coord: [number, number]): { transform: string, opacity: string, filter: string } {
  const [lat, lng] = coord
  const lambda = lng * Math.PI / 180
  const beta = lat * Math.PI / 180
  const radius = Math.min(containerWidth.value || 320, containerHeight.value || 320) * 0.5
  const center = radius
  const rotated = lambda + phi + Math.PI / 2
  const x = Math.cos(beta) * Math.sin(rotated)
  const y = Math.sin(beta) * Math.cos(theta) - Math.cos(beta) * Math.cos(rotated) * Math.sin(theta)
  const z = Math.sin(beta) * Math.sin(theta) + Math.cos(beta) * Math.cos(rotated) * Math.cos(theta)
  const visible = z > -0.08
  const nextX = (center + x * radius * 0.84).toFixed(1)
  const nextY = (center - y * radius * 0.84).toFixed(1)

  return {
    transform: `translate3d(${nextX}px, ${nextY}px, 0) translate(-50%, -50%)`,
    opacity: visible ? '1' : '0',
    filter: visible ? 'blur(0)' : 'blur(12px)',
  }
}

function applyLabelStyles() {
  for (const cluster of regionClusters.value) {
    const element = labelElements.get(cluster.id)
    if (!element)
      continue
    const style = getClusterStyle(cluster.coord)
    element.style.transform = style.transform
    element.style.opacity = style.opacity
    element.style.filter = style.filter
  }
}

function setLabelRef(id: string, element: Element | ComponentPublicInstance | null) {
  if (element instanceof HTMLElement) {
    labelElements.set(id, element)
    const cluster = regionClusters.value.find(item => item.id === id)
    if (cluster) {
      const style = getClusterStyle(cluster.coord)
      element.style.transform = style.transform
      element.style.opacity = style.opacity
      element.style.filter = style.filter
    }
    return
  }

  labelElements.delete(id)
}

function bindLabelRef(id: string) {
  return (element: Element | ComponentPublicInstance | null) => setLabelRef(id, element)
}

const cobeLabels = computed(() => regionClusters.value.map(cluster => ({
  id: cluster.id,
  code: cluster.code,
})))

const themeColors = computed(() => {
  if (appStore.isDark) {
    return {
      dark: 1,
      mapBrightness: 8,
      baseColor: [0.95, 0.95, 0.98] as [number, number, number],
      markerColor: [0.18, 0.78, 1.0] as [number, number, number],
      glowColor: [0.78, 0.90, 1.0] as [number, number, number],
    }
  }
  return {
    dark: 0,
    mapBrightness: 10,
    baseColor: [0.98, 0.98, 0.99] as [number, number, number],
    markerColor: [0.05, 0.35, 0.90] as [number, number, number],
    glowColor: [0.80, 0.90, 1.0] as [number, number, number],
  }
})

function getRenderSize() {
  const width = containerWidth.value || canvasRef.value?.clientWidth || 320
  const height = containerHeight.value || canvasRef.value?.clientHeight || width
  return { width, height }
}

function getDevicePixelRatio(): number {
  if (typeof window === 'undefined')
    return 1

  return Math.min(window.devicePixelRatio || 1, 2)
}

function buildInitialOptions(): COBEOptions {
  const colors = themeColors.value
  const { width, height } = getRenderSize()
  return {
    devicePixelRatio: getDevicePixelRatio(),
    width,
    height,
    phi,
    theta,
    dark: colors.dark,
    diffuse: 0.5, // 从 2.2 降到 1.0，减少白色溢光
    mapSamples: 16000, // 适中采样：点阵更稀疏，旋转时摩尔纹更轻（过高采样会加剧像素干涉）
    mapBrightness: colors.mapBrightness,
    baseColor: colors.baseColor,
    markerColor: colors.markerColor,
    glowColor: colors.glowColor,
    markers: markers.value,
    markerElevation: 0,

  }
}

function updateGlobeFrame() {
  if (!globe)
    return
  const { width, height } = getRenderSize()
  globe.update({ phi, theta, width, height })
}

const ORIENTATION_IDLE_EPSILON = 1e-5
const { pause: pauseRaf, resume: resumeRaf } = useRafFn(
  () => {
    if (!globe)
      return
    const prevPhi = phi
    const prevTheta = theta
    if (!isPointerDown && shouldAutoRotate.value)
      targetPhi += 0.0010
    phi += (targetPhi - phi) * 1
    theta += (targetTheta - theta) * 1
    if (
      Math.abs(phi - prevPhi) < ORIENTATION_IDLE_EPSILON
      && Math.abs(theta - prevTheta) < ORIENTATION_IDLE_EPSILON
    ) {
      if (!shouldAutoRotate.value && shouldKeepStaticRedraw()) {
        updateGlobeFrame()
        applyLabelStyles()
      }
      return
    }
    updateGlobeFrame()
    applyLabelStyles()
  },
  { immediate: false },
)

function startGlobe() {
  if (!canvasRef.value)
    return
  if (appStore.stopEarth) {
    resetStoppedView()
    triggerStaticRedrawWindow()
  }
  globe = createGlobe(canvasRef.value, buildInitialOptions())
  requestAnimationFrame(() => {
    updateGlobeFrame()
    applyLabelStyles()
  })
  if (documentVisibility.value === 'visible')
    resumeRaf()
}

async function stopGlobe() {
  pauseRaf()
  await nextTick()
  globe?.destroy()
  globe = null
  if (canvasRef.value && containerRef.value) {
    const cobeWrapper = canvasRef.value.parentElement
    if (cobeWrapper && cobeWrapper !== containerRef.value) {
      containerRef.value.appendChild(canvasRef.value)
      cobeWrapper.remove()
    }
  }
}

async function rebuildGlobe() {
  await stopGlobe()
  startGlobe()
}

onMounted(() => {
  startGlobe()
})

onBeforeUnmount(() => {
  pauseRaf()
  globe?.destroy()
  globe = null
})

watch(() => appStore.isDark, async () => {
  await rebuildGlobe()
})

watch(
  [containerWidth, containerHeight],
  ([width, height]) => {
    if (!globe || width <= 0 || height <= 0)
      return
    if (!shouldAutoRotate.value)
      triggerStaticRedrawWindow(600)
    updateGlobeFrame()
    applyLabelStyles()
  },
)

watch(
  () => appStore.stopEarth,
  (stopped) => {
    if (stopped)
      resetStoppedView()
    triggerStaticRedrawWindow()
    updateGlobeFrame()
    applyLabelStyles()
  },
)

watch(
  () => regionClusters.value.map(clusterKey).join(','),
  () => {
    if (!globe)
      return
    globe.update({ markers: markers.value })
    applyLabelStyles()
    if (!shouldAutoRotate.value)
      triggerStaticRedrawWindow(600)
  },
)

watch(shouldRender, (visible) => {
  if (!globe)
    return
  if (visible) {
    if (!shouldAutoRotate.value)
      triggerStaticRedrawWindow()
    resumeRaf()
  }
  else {
    pauseRaf()
  }
})

function onPointerDown(e: PointerEvent) {
  isPointerDown = true
  lastPointerX = e.clientX
  lastPointerY = e.clientY
  const target = e.currentTarget as HTMLElement
  target.setPointerCapture(e.pointerId)
}
function onPointerMove(e: PointerEvent) {
  if (!isPointerDown)
    return
  const deltaX = e.clientX - lastPointerX
  const deltaY = e.clientY - lastPointerY
  lastPointerX = e.clientX
  lastPointerY = e.clientY
  targetPhi += deltaX / 200
  targetTheta = clampTheta(targetTheta + deltaY / 300)
}
function onPointerUp(e: PointerEvent) {
  isPointerDown = false
  const target = e.currentTarget as HTMLElement
  if (target.hasPointerCapture(e.pointerId))
    target.releasePointerCapture(e.pointerId)
}
</script>

<template>
  <div ref="containerRef" class="relative aspect-square w-full max-w-md mx-auto -translate-y-6 md:-translate-y-12">
    <canvas
      ref="canvasRef"
      class="earth-globe-canvas absolute inset-0 w-full h-full select-none touch-none cursor-grab active:cursor-grabbing"
      @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="onPointerUp" @pointercancel="onPointerUp"
    />

    <div
      v-for="label in cobeLabels"
      :key="label.id"
      :ref="bindLabelRef(label.id)"
      class="absolute left-0 top-0 z-3 rounded-[0.18rem] transition-[opacity,filter] duration-300"
    >
      <img
        :src="`/images/flags/${label.code}.svg`" :alt="label.code"
        class="block size-5 rounded-[0.18rem] shadow-[0_8px_20px_rgb(15_23_42/0.24)]"
      >
    </div>

    <div
      v-if="totalServers > 0"
      class="absolute top-6 md:top-12 left-0 text-[10px] text-muted-foreground pointer-events-none flex gap-2 items-center bg-background/85 shadow-sm ring-1 ring-border/60 rounded px-2 py-0.5"
    >
      <div v-if="onlineServers > 0" class="flex items-center gap-1">
        <span class="inline-block size-1.5 rounded-full bg-green-600 animate-pulse" />
        <span class="text-green-600">{{ onlineServers }}</span>
      </div>
      <div v-if="offlineServers > 0" class="flex items-center gap-1">
        <span class="inline-block size-1.5 rounded-full bg-yellow-600 animate-pulse" />
        <span class="text-yellow-600">{{ offlineServers }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.earth-globe-canvas {
  contain: layout paint;
  filter: blur(0.5px); /* 轻微模糊柔化点阵，进一步抑制旋转时的摩尔纹 */
}
</style>
