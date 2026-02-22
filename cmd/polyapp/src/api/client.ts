const API_BASE = import.meta.env.VITE_API_URL ?? ''

async function fetchJson<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers: { Accept: 'application/json', ...options?.headers },
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(res.status === 404 ? 'Not found' : text || `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

export interface Health {
  status: string
  version?: string
}

export interface Market {
  ID: string
  Question: string
  Slug: string
  Volume: number
  Status: string
  TokenIDs?: string[]
}

export interface ArbOpportunity {
  ID: string
  PolyMarketID: string
  PolyTokenID: string
  PolyPrice: number
  KalshiMarketID?: string
  KalshiPrice?: number
  GrossEdgeBps: number
  Direction: string
  MaxAmount: number
  NetEdgeBps: number
  ExpectedPnLUSD: number
  DetectedAt: string
  Duration?: number
  Executed: boolean
}

export interface ArbLeg {
  OrderID: string
  MarketID: string
  TokenID: string
  Side: string
  ExpectedPrice: number
  FilledPrice: number
  Size: number
  FeeUSD: number
  SlippageBps: number
  Status: string
}

export interface ArbExecution {
  ID: string
  OpportunityID: string
  ArbType: string
  LegGroupID: string
  Legs: ArbLeg[]
  GrossEdgeBps: number
  TotalFees: number
  TotalSlippage: number
  NetPnLUSD: number
  Status: string
  StartedAt: string
  CompletedAt?: string
}

export interface StrategyCandidate {
  signal_id: string
  strategy: string
  market_id: string
  market_question?: string
  token_id: string
  side: 'buy' | 'sell'
  price: number
  size: number
  urgency: number
  reason: string
  created_at: string
  expires_at?: string
  score: number
}

export interface StrategyCandidatesResponse {
  active_strategy: string
  auto_execute: boolean
  candidates: StrategyCandidate[]
  best?: StrategyCandidate
}

export interface TradeSignalInput {
  ID: string
  Source: string
  MarketID: string
  TokenID: string
  Side: 'buy' | 'sell'
  PriceTicks: number
  SizeUnits: number
  Urgency: number
  Reason: string
  CreatedAt: string
  ExpiresAt: string
  Metadata?: Record<string, string>
}

export interface OrderResult {
  Success: boolean
  OrderID: string
  Status: string
  Message: string
  ShouldRetry: boolean
  FilledPrice: number
  FeeUSD: number
}

export interface BackendStatus {
  mode: string
  strategy_name: string
}

export const api = {
  health: () => fetchJson<Health>('/api/health'),
  /** Backend mode and strategy (REST fallback when WS status not yet received). */
  status: () => fetchJson<BackendStatus>('/api/status'),
  markets: (limit = 50) => fetchJson<{ markets?: Market[] }>(`/api/markets?limit=${limit}`).then((r) => r.markets ?? []),
  market: (id: string) => fetchJson<Market>(`/api/markets/${encodeURIComponent(id)}`),
  arbRecent: (limit = 30) =>
    fetchJson<{ opportunities: ArbOpportunity[] }>(`/api/arbitrage/recent?limit=${limit}`).then((r) => r.opportunities ?? []),
  arbProfit: (since?: string, type?: string) => {
    const params = new URLSearchParams()
    if (since) params.set('since', since)
    if (type) params.set('type', type)
    return fetchJson<{ since: string; total_pnl_usd: number }>(`/api/arbitrage/profit?${params}`)
  },
  arbExecutions: (limit = 50) =>
    fetchJson<{ executions: ArbExecution[] }>(`/api/arbitrage/executions?limit=${limit}`).then((r) => r.executions ?? []),
  arbExecution: (id: string) => fetchJson<ArbExecution>(`/api/arbitrage/executions/${encodeURIComponent(id)}`),

  /** Current active strategy (501 in arbitrage-only mode). */
  strategyActive: () => fetchJson<{ active: string }>('/api/strategy/active'),
  /** List of registered strategy names (501 in arbitrage-only mode). */
  strategyList: () =>
    fetchJson<{ strategies: string[] }>('/api/strategy/list').then((r) => r.strategies ?? []),
  /** Set active strategy (POST). Returns 501 if runtime not available. */
  strategySetActive: async (name: string) => {
    const res = await fetch(`${API_BASE}/api/strategy/active`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (!res.ok) {
      const text = await res.text()
      throw new Error(res.status === 501 ? 'Strategy switching not available' : text || `HTTP ${res.status}`)
    }
    return res.json() as Promise<{ active: string }>
  },
  strategyCandidates: (limit = 20, source?: string) => {
    const params = new URLSearchParams()
    params.set('limit', String(limit))
    if (source) params.set('source', source)
    return fetchJson<StrategyCandidatesResponse>(`/api/strategy/candidates?${params.toString()}`)
  },
  placeOrder: (signal: TradeSignalInput) =>
    fetchJson<OrderResult>('/api/orders', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(signal),
    }),
}
