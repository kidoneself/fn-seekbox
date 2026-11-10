import { fileURLToPath, URL } from 'node:url'
import { readFileSync } from 'node:fs'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

// The package manifest is the source of truth for the version shown in the app.
// A standalone frontend checkout has no package manifest and is labelled dev.
function readAppVersion() {
  if (process.env.SEEKBOX_VERSION) {
    if (!/^\d+\.\d+\.\d+$/.test(process.env.SEEKBOX_VERSION)) throw new Error('Invalid SEEKBOX_VERSION')
    return process.env.SEEKBOX_VERSION
  }
  let manifest
  try {
    manifest = readFileSync(new URL('../manifest', import.meta.url), 'utf8')
  } catch (error) {
    if (error.code === 'ENOENT') return 'dev'
    throw error
  }
  const version = manifest.match(/^\s*version\s*=\s*(\S+)\s*$/m)?.[1]
  if (!version) throw new Error('Package manifest is missing its version')
  return version
}

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  // 加载环境变量
  const env = loadEnv(mode, process.cwd(), '')
  
  return {
    // Production is served by the authenticated fnOS unified gateway.
    // Keep the development base at / so the existing Vite proxies still work.
    base: env.VITE_APP_BASE || (mode === 'production' ? '/app/fn-seekbox/' : '/'),
    plugins: [
      { name: 'seekbox-version', generateBundle() { this.emitFile({ type: 'asset', fileName: 'version.json', source: JSON.stringify({ version: readAppVersion() }) }) } },
      vue({
        template: {
          compilerOptions: {
            // 启用运行时编译
            isCustomElement: () => false
          }
        }
      }),
    ],
    define: {
      __APP_VERSION__: JSON.stringify(readAppVersion()),
      // Vue 3 feature flags
      __VUE_OPTIONS_API__: true,
      __VUE_PROD_DEVTOOLS__: false,
      __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: false
    },
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
        // 使用包含编译器的Vue版本
        'vue': 'vue/dist/vue.esm-bundler.js'
      }
    },
    server: {
      host: 'localhost', // 确保使用localhost以支持Web Crypto API
      port: 3000,
      proxy: {
        '/api': {
          target: env.VITE_API_BASE_URL || 'http://localhost:8888',
          changeOrigin: true
        },
        '/qqpd': {
          target: env.VITE_API_BASE_URL || 'http://localhost:8888',
          changeOrigin: true
        },
        '/gying': {
          target: env.VITE_API_BASE_URL || 'http://localhost:8888',
          changeOrigin: true
        },
        '/panlian': {
          target: env.VITE_API_BASE_URL || 'http://localhost:8888',
          changeOrigin: true
        },
        '/weibo': {
          target: env.VITE_API_BASE_URL || 'http://localhost:8888',
          changeOrigin: true
        }
      }
    }
  }
})
