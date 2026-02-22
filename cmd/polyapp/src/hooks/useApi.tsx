import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ArbExecution, ArbOpportunity, Health, Market, StrategyCandidatesResponse } from '../api/client'
import { api } from '../api/client'

export const REFRESH_MS = 30_000

// Stable empty refs so useCallback(run, [..., initial, ...]) doesn't change every render and trigger refetch loops.
const EMPTY_MARKETS: Market[] = []
const EMPTY_OPPORTUNITIES: ArbOpportunity[] = []
const EMPTY_EXECUTIONS: ArbExecution[] = []
const EMPTY_CANDIDATES: StrategyCandidatesResponse = {
  active_strategy: '',
  auto_execute: true,
  candidates: [],
}

type RefreshContextValue = { tick: number; bump: () => void }
const RefreshContext = createContext<RefreshContextValue | null>(null)

export function RefreshProvider({ children }: { children: React.ReactNode }) {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), REFRESH_MS)
    return () => clearInterval(id)
  }, [])
  const bump = useCallback(() => setTick((t) => t + 1), [])
  const value = useMemo(() => ({ tick, bump }), [tick, bump])
  return <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>
}

function useRefreshTick() {
  const ctx = useContext(RefreshContext)
  if (!ctx) return { tick: 0, bump: () => {} }
  return ctx
}

function useApi<T>(
  fetchFn: () => Promise<T>,
  initial: T,
  isArray = false
): {
  data: T
  error: string | null
  loading: boolean
  refetch: () => void
} {
  const { tick, bump } = useRefreshTick()
  const [data, setData] = useState<T>(initial)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const run = useCallback((showLoading = true) => {
    if (showLoading) setLoading(true)
    fetchFn()
      .then((result) => {
        setData(isArray && !Array.isArray(result) ? ([] as T) : result)
        setError(null)
      })
      .catch((e) => {
        setError(e.message)
        setData(initial)
      })
      .finally(() => setLoading(false))
  }, [fetchFn, initial, isArray])

  useEffect(() => {
    run(tick === 0)
  }, [tick, run])

  const refetch = useCallback(() => bump(), [bump])

  return { data, error, loading, refetch }
}

export function useHealth() {
  const fetchFn = useCallback(() => api.health(), [])
  return useApi(fetchFn, null as Health | null)
}

export function useMarkets() {
  const fetchFn = useCallback(() => api.markets(100), [])
  return useApi(fetchFn, EMPTY_MARKETS, true)
}

export function useArbRecent() {
  const fetchFn = useCallback(() => api.arbRecent(50), [])
  return useApi(fetchFn, EMPTY_OPPORTUNITIES, true)
}

export function useArbProfit(days: number = 7) {
  const since = useMemo(() => {
    const d = new Date()
    d.setDate(d.getDate() - days)
    return d.toISOString().slice(0, 10)
  }, [days])
  const fetchFn = useCallback(() => api.arbProfit(since), [since])
  return useApi(fetchFn, null as { since: string; total_pnl_usd: number } | null)
}

export function useArbExecutions() {
  const fetchFn = useCallback(() => api.arbExecutions(50), [])
  return useApi(fetchFn, EMPTY_EXECUTIONS, true)
}

export function useStrategyCandidates(limit: number = 20, source?: string) {
  const fetchFn = useCallback(() => api.strategyCandidates(limit, source), [limit, source])
  return useApi(fetchFn, EMPTY_CANDIDATES)
}
