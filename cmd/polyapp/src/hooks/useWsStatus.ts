import { useEffect, useState } from 'react'

const WS_BASE = import.meta.env.VITE_API_URL
  ? (import.meta.env.VITE_API_URL.replace(/^http/, 'ws'))
  : (typeof location !== 'undefined' ? `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}` : 'ws://localhost:5173')

export interface BotStatus {
  mode: string
  ws_connected: boolean
  uptime_seconds: number
  open_positions: number
  open_orders: number
  strategy_name: string
}

export function useWsStatus() {
  const [status, setStatus] = useState<BotStatus | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const url = `${WS_BASE}/ws`
    const ws = new WebSocket(url)

    ws.onopen = () => {
      setConnected(true)
      setError(null)
      ws.send(JSON.stringify({ subscribe: ['ch:status'] }))
    }

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data)
        if (msg.type === 'bot_status' && msg.payload) {
          setStatus(msg.payload as BotStatus)
        }
      } catch {
        // ignore non-JSON or other message types
      }
    }

    ws.onclose = () => {
      setConnected(false)
      if (!status) setError('WebSocket closed')
    }

    ws.onerror = () => {
      setError('WebSocket error')
    }

    return () => ws.close()
  }, [])

  return { status, connected, error }
}
