import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const backend = process.env.VITE_PROXY_TARGET || ''

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 5174,
    watch: { usePolling: true },
    // When running inside Docker Compose, proxy API requests to the ultrabase
    // container so the browser never makes cross-origin calls.
    ...(backend && {
      proxy: {
        '/auth': { target: backend, changeOrigin: true },
        '/rest': { target: backend, changeOrigin: true },
      },
    }),
  },
})
