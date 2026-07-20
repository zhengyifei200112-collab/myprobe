import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:25775',
        changeOrigin: true,
        rewriteWsOrigin: true,
        ws: true,
      },
      '/healthz': {
        target: 'http://127.0.0.1:25775',
      },
    },
  },
})
