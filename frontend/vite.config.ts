import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1600,
    rollupOptions: { output: { manualChunks: { charts: ['echarts'], ui: ['element-plus', '@element-plus/icons-vue'] } } }
  },
  server: { proxy: { '/api': 'http://127.0.0.1:17888' } }
})
