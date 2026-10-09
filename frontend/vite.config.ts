import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'
import path from 'node:path'

const backendUrl = process.env.VITE_BACKEND_URL ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': backendUrl,
      '/healthz': backendUrl,
      '/readyz': backendUrl,
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
  },
})
