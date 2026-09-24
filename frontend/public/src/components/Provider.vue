<script setup lang="ts">
import { provide, ref, watch } from 'vue'
import { BackTop } from '@/components/ui/back-top'
import { useAppStore } from '@/stores/app'
import { buildGlassThemeTokens } from '@/utils/glassTheme'

const appStore = useAppStore()

const isScrolled = ref(false)
provide('isScrolled', isScrolled)

watch(
  () => appStore.isDark,
  (dark) => {
    const root = document.documentElement
    if (dark)
      root.classList.add('dark')
    else root.classList.remove('dark')
    root.style.colorScheme = dark ? 'dark' : 'light'
  },
  { immediate: true },
)

watch(
  () => [appStore.glassColorPreset, appStore.glassCustomColors] as const,
  ([preset, customColors]) => {
    const tokens = buildGlassThemeTokens(preset, customColors)
    const root = document.documentElement
    root.style.setProperty('--glass-light-card', tokens.lightCard)
    root.style.setProperty('--glass-light-card-hover', tokens.lightCardHover)
    root.style.setProperty('--glass-light-control', tokens.lightControl)
    root.style.setProperty('--glass-light-header', tokens.lightHeader)
    root.style.setProperty('--glass-light-text', tokens.lightText)
    root.style.setProperty('--glass-light-muted-text', tokens.lightMutedText)
    root.style.setProperty('--glass-light-border', tokens.lightBorder)
    root.style.setProperty('--glass-light-shadow', tokens.lightShadow)
    root.style.setProperty('--glass-dark-card', tokens.darkCard)
    root.style.setProperty('--glass-dark-card-hover', tokens.darkCardHover)
    root.style.setProperty('--glass-dark-control', tokens.darkControl)
    root.style.setProperty('--glass-dark-header', tokens.darkHeader)
    root.style.setProperty('--glass-dark-text', tokens.darkText)
    root.style.setProperty('--glass-dark-muted-text', tokens.darkMutedText)
    root.style.setProperty('--glass-dark-border', tokens.darkBorder)
    root.style.setProperty('--glass-dark-shadow', tokens.darkShadow)
  },
  { immediate: true, deep: true },
)

watch(
  () => appStore.colorVisionFriendly,
  (enabled) => {
    document.documentElement.classList.toggle('color-vision-friendly', enabled)
  },
  { immediate: true },
)
</script>

<template>
  <slot />
  <BackTop :visibility-height="320" @scrolled="isScrolled = $event" />
</template>
