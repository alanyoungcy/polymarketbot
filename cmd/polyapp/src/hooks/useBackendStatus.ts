import { useEffect, useState } from 'react'
import { api } from '../api/client'

/** REST fallback for backend mode and strategy when WS status hasn't arrived yet. */
export function useBackendStatus() {
  const [rest, setRest] = useState<{ mode: string; strategy_name: string } | null>(null)

  useEffect(() => {
    api
      .status()
      .then((s) => setRest({ mode: s.mode ?? '', strategy_name: s.strategy_name ?? '' }))
      .catch(() => {})
  }, [])

  return rest
}
