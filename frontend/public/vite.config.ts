import { execSync } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import process from 'node:process'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

import vueDevTools from 'vite-plugin-vue-devtools'

interface ThemeManifest {
  preview?: unknown
  version?: unknown
}

const configDir = fileURLToPath(new URL('.', import.meta.url))
const themeJsonPath = resolve(configDir, 'komari-theme.json')
const devApiTarget = process.env.VITE_API_TARGET || 'http://127.0.0.1:25774'

function readThemeManifest(): ThemeManifest {
  if (!existsSync(themeJsonPath))
    throw new Error('komari-theme.json not found')

  return JSON.parse(readFileSync(themeJsonPath, 'utf-8')) as ThemeManifest
}

function getThemeVersion(): string {
  const version = readThemeManifest().version

  if (typeof version !== 'string' || !version.trim())
    throw new TypeError('komari-theme.json does not contain a top-level string version field')

  return version.trim()
}

function getCommitHash(): string {
  try {
    return execSync('git rev-parse --short HEAD', { encoding: 'utf-8' }).trim()
  }
  catch {
    return 'unknown'
  }
}

export default defineConfig({
  define: {
    __BUILD_VERSION__: JSON.stringify(getThemeVersion()),
    __BUILD_GIT_HASH__: JSON.stringify(getCommitHash()),
  },
  plugins: [
    vue(),
    vueDevTools(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: devApiTarget,
        changeOrigin: true,
        headers: { Origin: devApiTarget },
        rewriteWsOrigin: true,
        ws: true,
      },
      '/themes': {
        target: devApiTarget,
        changeOrigin: true,
        headers: { Origin: devApiTarget },
      },
    },
  },
  build: {
    target: ['es2018', 'safari15.4'],
    cssTarget: 'safari15.4',
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks: {
          'vue-vendor': ['vue', 'vue-router', 'pinia'],
          'echarts': ['echarts', 'vue-echarts'],
          'globe': ['globe.gl', 'three'],
          'reka-ui': ['reka-ui'],
          'vueuse': ['@vueuse/core'],
          'v3-services': [
            './src/services/history.service.ts',
            './src/services/metrics.service.ts',
            './src/services/request.service.ts',
            './src/services/cache.service.ts',
            './src/utils/osImageHelper.ts',
            './src/utils/metricSeries.ts',
            './src/composables/useNodePingDisplay.ts',
          ],
        },
      },
    },
  },
})
