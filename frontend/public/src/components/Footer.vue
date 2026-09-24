<script setup lang="ts">
import type { VersionInfo } from '@/utils/api'
import { computed, onMounted, ref } from 'vue'
import { DataTooltip } from '@/components/ui/data-tooltip'
import { getSharedApi } from '@/utils/api'

const api = getSharedApi()

const serverVersion = ref<VersionInfo | null>(null)

onMounted(async () => {
  try {
    serverVersion.value = await api.getVersion()
  }
  catch {
    // 静默失败
  }
})

const formattedServerVersion = computed(() => serverVersion.value?.version ?? '')
</script>

<template>
  <footer class="w-full max-w-[1280px] mx-auto p-4">
    <div class="flex w-full flex-row gap-4 text-xs text-muted-foreground">
      <div class="flex gap-1 items-center">
        Powered by
        <DataTooltip
          as="span"
          placement="top"
          :content="formattedServerVersion"
        >
          <a
            href="https://github.com/SIXOSN/komari" target="_blank" rel="noopener noreferrer"
            class="transition-opacity hover:opacity-80"
          >
            <span class="font-medium text-foreground">SIXOSN Komari</span>
          </a>
        </DataTooltip>
      </div>
    </div>
  </footer>
</template>
