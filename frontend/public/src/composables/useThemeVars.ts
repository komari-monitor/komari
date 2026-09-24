import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useAppStore } from '@/stores/app'

/**
 * 读取主题颜色 token（基于当前 CSS 变量）。
 */
export function useThemeVars() {
  const appStore = useAppStore()
  const revision = ref(0)
  let observer: MutationObserver | null = null

  function read(name: string, fallback: string): string {
    if (typeof window === 'undefined')
      return fallback
    const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
    return v || fallback
  }

  function refresh() {
    revision.value += 1
  }

  onMounted(() => {
    observer = new MutationObserver(refresh)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'style'] })
  })

  onUnmounted(() => {
    observer?.disconnect()
    observer = null
  })

  watch(() => appStore.resolvedThemeMode, refresh)

  return computed(() => {
    void revision.value
    return {
      successColor: read('--success', 'oklch(0.696 0.17 162.48)'),
      successColorHover: read('--success', 'oklch(0.696 0.17 162.48)'),
      successColorPressed: read('--success', 'oklch(0.696 0.17 162.48)'),
      infoColor: read('--info', 'oklch(0.6 0.118 230)'),
      infoColorHover: read('--info', 'oklch(0.6 0.118 230)'),
      infoColorPressed: read('--info', 'oklch(0.6 0.118 230)'),
      warningColor: read('--warning', 'oklch(0.768 0.155 70)'),
      warningColorHover: read('--warning', 'oklch(0.768 0.155 70)'),
      warningColorPressed: read('--warning', 'oklch(0.768 0.155 70)'),
      errorColor: read('--destructive', 'oklch(0.577 0.245 27.325)'),
      errorColorHover: read('--destructive', 'oklch(0.577 0.245 27.325)'),
      errorColorPressed: read('--destructive', 'oklch(0.577 0.245 27.325)'),
      primaryColor: read('--primary', 'oklch(0.21 0.006 285.885)'),
      primaryColorHover: read('--primary', 'oklch(0.21 0.006 285.885)'),
      primaryColorPressed: read('--primary', 'oklch(0.21 0.006 285.885)'),
      textColor1: read('--foreground', 'oklch(0.141 0.005 285.823)'),
      textColor2: read('--foreground', 'oklch(0.141 0.005 285.823)'),
      textColor3: read('--muted-foreground', 'oklch(0.552 0.016 285.938)'),
      borderColor: read('--border', 'oklch(0.92 0.004 286.32)'),
      bodyColor: read('--background', 'oklch(1 0 0)'),
      cardColor: read('--card', 'oklch(1 0 0)'),
      progressRailColor: read('--muted', 'oklch(0.967 0.001 286.375)'),
    }
  })
}
