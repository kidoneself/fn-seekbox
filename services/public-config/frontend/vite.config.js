import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: '/seekbox-config/',
  plugins: [vue()],
  server: {
    host: '127.0.0.1',
    proxy: { '/seekbox-config/api': 'http://127.0.0.1:8899' },
  },
})
