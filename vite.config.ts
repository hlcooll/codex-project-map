import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const backend = 'http://127.0.0.1:4399'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        target: backend,
        changeOrigin: true,
        configure(proxy) {
          proxy.on('proxyReq', (upstream, request) => {
            const host = request.headers.host
            if (host?.startsWith('127.0.0.1:') && request.headers.origin === `http://${host}`) {
              upstream.setHeader('Origin', backend)
            }
          })
        },
      },
    },
  },
})
