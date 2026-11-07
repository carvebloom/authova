import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// https://vite.dev/config/
export default defineConfig({
  // 产物由 Go BFF 以静态文件托管，挂载路径未定，使用相对路径保证可用。
  base: './',
  plugins: [react()],
  server: {
    proxy: {
      '/bff': {
        target: 'http://127.0.0.1:18090',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
    css: false,
  },
})
