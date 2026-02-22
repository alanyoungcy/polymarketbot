import { useCallback, useEffect, useState } from 'react'
import { api } from '../api/client'

const REFRESH_MS = 10_000

/** Strategy runtime API is only available in trade/full mode; 501 in arbitrage-only. */
export function useStrategyRuntime() {
  const [strategies, setStrategies] = useState<string[]>([])
  const [active, setActive] = useState<string | null>(null)
  const [available, setAvailable] = useState<boolean | null>(null) // null = loading, true = can select, false = 501
  const [error, setError] = useState<string | null>(null)
  const [setting, setSetting] = useState(false)

  const fetchRuntime = useCallback(async () => {
    try {
      const base = (import.meta.env.VITE_API_URL ?? '').toString()
      const [listRes, activeRes] = await Promise.all([
        fetch(`${base}/api/strategy/list`, { headers: { Accept: 'application/json' } }),
        fetch(`${base}/api/strategy/active`, { headers: { Accept: 'application/json' } }),
      ])
      if (listRes.status === 501 || activeRes.status === 501) {
        setAvailable(false)
        setStrategies([])
        setActive(null)
        setError(null)
        return
      }
      if (!listRes.ok || !activeRes.ok) {
        setAvailable(false)
        setError(listRes.ok ? await activeRes.text() : await listRes.text())
        return
      }
      const list = (await listRes.json()) as { strategies?: string[] }
      const act = (await activeRes.json()) as { active?: string }
      setAvailable(true)
      setStrategies(list.strategies ?? [])
      setActive(act.active ?? null)
      setError(null)
    } catch (e) {
      setAvailable(false)
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])

  useEffect(() => {
    fetchRuntime()
    const id = setInterval(fetchRuntime, REFRESH_MS)
    return () => clearInterval(id)
  }, [fetchRuntime])

  const setActiveStrategy = useCallback(async (name: string) => {
    setSetting(true)
    setError(null)
    try {
      const result = await api.strategySetActive(name)
      setActive(result.active ?? name)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSetting(false)
    }
  }, [])

  return { strategies, active, available, error, setting, setActiveStrategy, refetch: fetchRuntime }
}
