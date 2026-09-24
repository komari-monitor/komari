<script setup lang="ts">
import { computed } from 'vue'
import { useAppStore } from '@/stores/app'

const appStore = useAppStore()

const hasCustomBackground = computed(() => appStore.backgroundEnabled && Boolean(appStore.currentBackgroundUrl))
</script>

<template>
  <div
    class="loading-cover flex items-center inset-0 justify-center fixed z-20"
    :class="hasCustomBackground ? 'loading-cover--custom-background' : ''"
  >
    <div
      class="loading-cover__indicator flex flex-col items-center gap-2"
      :class="hasCustomBackground ? 'text-white/75 dark:text-white/75' : 'text-foreground'"
    >
      <span
        class="inline-block animate-spin rounded-full border-2"
        :class="hasCustomBackground ? 'size-5' : 'size-7'"
        style="border-color: color-mix(in srgb, currentColor 18%, transparent); border-top-color: currentColor;"
      />
      <span v-if="!hasCustomBackground" class="text-sm text-muted-foreground">Loading...</span>
    </div>
  </div>
</template>

<style scoped>
.loading-cover {
  background: radial-gradient(circle at 50% 20%, rgb(148 163 184 / 0.12), transparent 42%), rgb(15 23 42 / 0.72);
}

:root:not(.dark) .loading-cover {
  background:
    radial-gradient(circle at 50% 16%, rgb(255 255 255 / 0.12), transparent 34%),
    linear-gradient(180deg, rgb(148 163 184 / 0.34), rgb(100 116 139 / 0.42));
}

.loading-cover__indicator {
  transition:
    opacity 240ms ease,
    transform 280ms cubic-bezier(0.22, 1, 0.36, 1);
}

.loading-cover--custom-background {
  background-color: rgb(15 23 42 / 0.08);
}

:root:not(.dark) .loading-cover--custom-background {
  background-color: rgb(15 23 42 / 0.1);
}
</style>
