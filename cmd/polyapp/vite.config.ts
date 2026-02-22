import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8000',
        changeOrigin: true,
      },
      '/ws': {
        target: 'ws://localhost:8000',
        ws: true,
        configure: (proxy) => {
          // EPIPE/ECONNRESET are normal when client or backend closes the socket first (e.g. refresh, restart)
          const ignore = (err: NodeJS.ErrnoException) => {
            if (err?.code === 'EPIPE' || err?.code === 'ECONNRESET') return
            console.error('[vite] ws proxy error:', err)
          }
          proxy.on('error', ignore)
          // Client socket: ignore expected errors
          proxy.on('proxyReqWs', (_proxyReq, _req, socket) => {
            socket.on('error', ignore)
          })
          // Target (backend) socket: ignore EPIPE/ECONNRESET so they don't surface as "ws proxy socket error"
          proxy.on('proxyResWs', (_proxyRes, _req, _socket, _head) => {
            const res = _proxyRes as NodeJS.ReadableStream & { socket?: NodeJS.EventEmitter }
            res.socket?.on?.('error', ignore)
          })
        },
      },
    },
  },
})
