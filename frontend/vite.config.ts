import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  // 保留浏览器 Host，使转发后的 Origin 与 Go 同源校验保持一致。
  server: { proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } } },
})
