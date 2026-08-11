import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接输出到 dist/，由 Go 通过 embed 托管在 /admin 路径下。
export default defineConfig({
  plugins: [vue()],
  base: '/admin/',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets'
  },
  server: {
    port: 5174,
    proxy: {
      // 开发态代理到 Go 服务，避免跨域
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true
      }
    }
  }
})
