import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发期把 /api 代理到 Go 后端(默认 :8088)，避免跨域烦恼。
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8088',
        changeOrigin: true
      }
    }
  },
  // build 产物默认输出到 dist/，Go 端可用 -static web/dist 托管。
  build: {
    outDir: 'dist'
  }
})
