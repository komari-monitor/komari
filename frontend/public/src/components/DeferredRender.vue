<script setup lang="ts">
import { nextTick, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{
  enabled?: boolean
  idleDelay?: number
  minHeight?: number
}>(), {
  enabled: false,
  idleDelay: 0,
  minHeight: 260,
})

const root = ref<HTMLElement | null>(null)
const rendered = ref(!props.enabled)
let observer: IntersectionObserver | null = null
let fallbackFrame: number | null = null
let fallbackTimer: number | null = null
let idleTimer: number | null = null

const VIEWPORT_MARGIN = 700

function isNearViewport(): boolean {
  if (!root.value || typeof window === 'undefined')
    return true
  const rect = root.value.getBoundingClientRect()
  return rect.bottom >= -VIEWPORT_MARGIN && rect.top <= window.innerHeight + VIEWPORT_MARGIN
}

function revealWhenNear(): void {
  fallbackFrame = null
  fallbackTimer = null
  if (!props.enabled || rendered.value || !isNearViewport())
    return
  rendered.value = true
  stopObserving()
}

function revealDeferred(): void {
  if (!props.enabled || rendered.value)
    return
  rendered.value = true
  stopObserving()
}

function scheduleIdleReveal(): void {
  if (idleTimer != null || props.idleDelay <= 0 || typeof window === 'undefined')
    return
  idleTimer = window.setTimeout(revealDeferred, props.idleDelay)
}

function scheduleFallbackCheck(): void {
  if (fallbackFrame != null || fallbackTimer != null || typeof window === 'undefined')
    return
  if (typeof window.requestAnimationFrame === 'function')
    fallbackFrame = window.requestAnimationFrame(revealWhenNear)
  else
    fallbackTimer = window.setTimeout(revealWhenNear, 16)
}

function stopObserving(): void {
  observer?.disconnect()
  observer = null
  if (typeof window !== 'undefined') {
    window.removeEventListener('scroll', scheduleFallbackCheck)
    window.removeEventListener('resize', scheduleFallbackCheck)
    if (fallbackFrame != null)
      window.cancelAnimationFrame(fallbackFrame)
    if (fallbackTimer != null)
      window.clearTimeout(fallbackTimer)
    if (idleTimer != null)
      window.clearTimeout(idleTimer)
  }
  fallbackFrame = null
  fallbackTimer = null
  idleTimer = null
}

function startObserving(): void {
  stopObserving()
  if (rendered.value || !props.enabled || !root.value)
    return

  if (isNearViewport()) {
    rendered.value = true
    return
  }

  scheduleIdleReveal()

  if (typeof IntersectionObserver === 'undefined') {
    window.addEventListener('scroll', scheduleFallbackCheck, { passive: true })
    window.addEventListener('resize', scheduleFallbackCheck, { passive: true })
    scheduleFallbackCheck()
    return
  }

  observer = new IntersectionObserver((entries) => {
    if (!entries.some(entry => entry.isIntersecting))
      return
    rendered.value = true
    stopObserving()
  }, { rootMargin: `${VIEWPORT_MARGIN}px 0px` })
  observer.observe(root.value)
}

watch(() => props.enabled, async (enabled) => {
  stopObserving()
  if (!enabled) {
    rendered.value = true
    return
  }

  await nextTick()
  rendered.value = isNearViewport()
  if (!rendered.value)
    await nextTick()
  startObserving()
})

onMounted(startObserving)
onActivated(startObserving)
onDeactivated(stopObserving)
onBeforeUnmount(stopObserving)
</script>

<template>
  <div
    ref="root"
    class="min-w-0"
    :style="rendered ? undefined : { minHeight: `${minHeight}px` }"
  >
    <slot v-if="rendered" />
  </div>
</template>
