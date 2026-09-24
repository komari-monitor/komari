<script setup lang="ts">
import { Icon } from '@iconify/vue'
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { useTrafficCycle } from '@/composables/useTrafficCycle'
import { useVisitorPageAudit } from '@/composables/useVisitorAudit'
import { useAppStore } from '@/stores/app'
import { destroyInitManager, initApp, retryInitApp } from '@/utils/init'
import Background from './components/Background.vue'
import Footer from './components/Footer.vue'
import Header from './components/Header.vue'
import LoadingCover from './components/LoadingCover.vue'
import Provider from './components/Provider.vue'

const appStore = useAppStore()
useVisitorPageAudit()
useTrafficCycle()

const isReady = ref(false)
const isRetryingConnection = ref(false)

async function retryConnection(): Promise<void> {
  if (isRetryingConnection.value)
    return

  isRetryingConnection.value = true
  try {
    const recovered = await retryInitApp()
    if (recovered)
      window.$message?.success('连接已恢复。')
    else
      window.$message?.error('仍无法连接服务器，请稍后再试。')
  }
  catch (error) {
    console.error('[App] Connection retry failed:', error)
    window.$message?.error('重试失败，请稍后再试。')
  }
  finally {
    isRetryingConnection.value = false
  }
}

onMounted(async () => {
  try {
    await initApp()
    await nextTick()
    isReady.value = true
  }
  catch (error) {
    console.error('[App] Initialization failed:', error)
    isReady.value = true
  }
})

onUnmounted(() => {
  destroyInitManager()
})
</script>

<template>
  <Provider>
    <Background />
    <Transition
      :css="!appStore.disablePageAnimation"
      enter-active-class="transition-opacity duration-200 ease-out" enter-from-class="opacity-0"
      enter-to-class="opacity-100" leave-active-class="transition-opacity duration-300 ease-in"
      leave-from-class="opacity-100" leave-to-class="opacity-0"
    >
      <LoadingCover v-if="appStore.loading" />
    </Transition>
    <Header />
    <Transition
      :css="!appStore.disablePageAnimation"
      enter-active-class="transition-all duration-300 ease-out"
      enter-from-class="opacity-0 translate-y-2"
      enter-to-class="opacity-100 translate-y-0"
    >
      <div v-if="!appStore.loading" class="app-shell">
        <main class="min-h-screen overflow-hidden">
          <div v-if="appStore.connectionError" class="relative z-10 mx-auto max-w-[1280px] px-4 pt-4">
            <Alert variant="destructive" class="!pr-28 border-none bg-destructive/10 backdrop-blur-xs rounded-md">
              <Icon icon="tabler:plug-connected-x" />
              <AlertTitle>RPC 服务错误</AlertTitle>
              <AlertDescription>连接服务器失败，请检查网络后重试。</AlertDescription>
              <AlertAction class="top-1/2 -translate-y-1/2">
                <Button size="sm" variant="outline" :disabled="isRetryingConnection" @click="retryConnection">
                  <Icon :icon="isRetryingConnection ? 'tabler:loader-2' : 'tabler:refresh'" :class="isRetryingConnection && 'animate-spin'" />
                  {{ isRetryingConnection ? '重试中' : '重试' }}
                </Button>
              </AlertAction>
            </Alert>
          </div>
          <div class="max-w-[1280px] mx-auto">
            <RouterView v-slot="{ Component }">
              <Transition
                :css="!appStore.disablePageAnimation"
                enter-active-class="transition-all duration-300 ease-out"
                enter-from-class="opacity-0 translate-y-2" enter-to-class="opacity-100 translate-y-0"
                leave-active-class="transition-opacity duration-150 ease-in" leave-from-class="opacity-100"
                leave-to-class="opacity-0"
                :mode="appStore.disablePageAnimation ? 'default' : 'out-in'"
              >
                <KeepAlive :include="['HomeView']">
                  <component :is="Component" />
                </KeepAlive>
              </Transition>
            </RouterView>
          </div>
        </main>
        <Footer />
      </div>
    </Transition>
    <Toaster rich-colors close-button position="top-center" />
  </Provider>
</template>
