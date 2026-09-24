<script setup lang="ts">
import { useNodePingDisplay } from '@/composables/useNodePingDisplay'

const props = defineProps<{
  uuid: string
  online: boolean
  enabled: boolean
}>()

const emit = defineEmits<{
  click: []
}>()

const {
  latencyRenderBars,
  lossRenderBars,
} = useNodePingDisplay(() => props.uuid, { enabled: () => props.enabled })
</script>

<template>
  <button
    type="button"
    class="group flex w-full flex-col gap-[1px] pr-4 text-left"
    aria-label="打开延迟和丢包监测"
    @click.stop="emit('click')"
  >
    <div class="group/panel relative items-center gap-1 opacity-80 hover:opacity-100">
      <div
        class="grid h-1 cursor-auto items-end gap-[1px] transition-all hover:h-2.5"
        :style="{ gridTemplateColumns: `repeat(${latencyRenderBars.length}, minmax(0, 1fr))` }"
      >
        <span
          v-for="bar in latencyRenderBars"
          :key="bar.key"
          :title="bar.tooltip"
          :aria-label="bar.tooltip"
          class="h-full w-full"
        >
          <span class="block h-full w-full rounded-[1px] transition-all group-hover:opacity-50 hover:scale-y-160 hover:opacity-100" :class="bar.className" />
        </span>
      </div>
    </div>
    <div class="group/panel relative items-center gap-1 opacity-80 hover:opacity-100">
      <div
        class="grid h-1 cursor-auto items-end gap-[1px] transition-all hover:h-2.5"
        :style="{ gridTemplateColumns: `repeat(${lossRenderBars.length}, minmax(0, 1fr))` }"
      >
        <span
          v-for="bar in lossRenderBars"
          :key="bar.key"
          :title="bar.tooltip"
          :aria-label="bar.tooltip"
          class="h-full w-full"
        >
          <span class="block h-full w-full rounded-[1px] transition-all group-hover:opacity-50 hover:scale-y-160 hover:opacity-100" :class="bar.className" />
        </span>
      </div>
    </div>
  </button>
</template>
