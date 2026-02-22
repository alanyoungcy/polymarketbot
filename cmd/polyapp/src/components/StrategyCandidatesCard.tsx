import { useEffect, useMemo, useState } from 'react'
import { api, type StrategyCandidate, type TradeSignalInput } from '../api/client'
import type { useStrategyCandidates } from '../hooks/useApi'

interface Props {
  candidates: ReturnType<typeof useStrategyCandidates>
  onOrderPlaced?: () => void
}

function toTradeSignal(candidate: StrategyCandidate): TradeSignalInput {
  const now = new Date()
  const expiry = candidate.expires_at ? new Date(candidate.expires_at) : new Date(now.getTime() + 30_000)
  return {
    ID: `manual-${candidate.signal_id}-${Date.now()}`,
    Source: `manual_ui:${candidate.strategy}`,
    MarketID: candidate.market_id,
    TokenID: candidate.token_id,
    Side: candidate.side,
    PriceTicks: Math.round(candidate.price * 1_000_000),
    SizeUnits: Math.round(candidate.size * 1_000_000),
    Urgency: candidate.urgency,
    Reason: `manual bet from candidate ${candidate.signal_id}: ${candidate.reason}`,
    CreatedAt: now.toISOString(),
    ExpiresAt: expiry.toISOString(),
    Metadata: {
      candidate_signal_id: candidate.signal_id,
      candidate_strategy: candidate.strategy,
      candidate_score: String(candidate.score),
    },
  }
}

export function StrategyCandidatesCard({ candidates, onOrderPlaced }: Props) {
  const [selectedId, setSelectedId] = useState<string>('')
  const [placing, setPlacing] = useState(false)
  const [result, setResult] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (candidates.data.candidates.length === 0) {
      setSelectedId('')
      return
    }
    if (!candidates.data.candidates.find((c) => c.signal_id === selectedId)) {
      setSelectedId(candidates.data.candidates[0].signal_id)
    }
  }, [candidates.data.candidates, selectedId])

  const selected = useMemo(
    () => candidates.data.candidates.find((c) => c.signal_id === selectedId) ?? null,
    [candidates.data.candidates, selectedId]
  )

  const placeSelected = async () => {
    if (!selected) return
    setPlacing(true)
    setError(null)
    setResult(null)
    try {
      const signal = toTradeSignal(selected)
      const res = await api.placeOrder(signal)
      setResult(res.Success ? `Order placed: ${res.OrderID}` : `Order rejected: ${res.Message || res.Status}`)
      if (res.Success && onOrderPlaced) onOrderPlaced()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPlacing(false)
    }
  }

  return (
    <div className="card">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '0.75rem', flexWrap: 'wrap' }}>
        <h3 style={{ margin: 0 }}>Strategy candidates</h3>
        <span style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
          Active: <strong>{candidates.data.active_strategy || '—'}</strong>
        </span>
      </div>
      {candidates.loading && <span className="loading">Loading…</span>}
      {candidates.error && <span className="error">{candidates.error}</span>}
      {error && <p className="error" style={{ marginTop: '0.5rem' }}>{error}</p>}
      {result && <p style={{ marginTop: '0.5rem', color: '#4ade80' }}>{result}</p>}

      {!candidates.loading && !candidates.error && (
        <>
          {!candidates.data.auto_execute && (
            <p style={{ margin: '0.5rem 0', color: '#94a3b8', fontSize: '0.85rem' }}>
              Auto-execute is off. Backend keeps scanning and waits for your bet.
            </p>
          )}
          {candidates.data.auto_execute && (
            <p style={{ margin: '0.5rem 0', color: '#fbbf24', fontSize: '0.85rem' }}>
              Auto-execute is on. Manual bets can overlap with bot executions.
            </p>
          )}
          {candidates.data.candidates.length === 0 ? (
            <p style={{ margin: '0.5rem 0', color: '#94a3b8' }}>
              No live candidates yet. The backend continues scanning.
            </p>
          ) : (
            <div style={{ marginTop: '0.5rem', display: 'grid', gap: '0.5rem' }}>
              {candidates.data.candidates.slice(0, 8).map((c) => {
                const isSelected = c.signal_id === selectedId
                return (
                  <button
                    key={c.signal_id}
                    type="button"
                    onClick={() => setSelectedId(c.signal_id)}
                    style={{
                      textAlign: 'left',
                      background: isSelected ? '#1e293b' : '#0f172a',
                      border: isSelected ? '1px solid #38bdf8' : '1px solid #334155',
                      borderRadius: 8,
                      color: '#e2e8f0',
                      padding: '0.6rem',
                      cursor: 'pointer',
                    }}
                  >
                    <div style={{ fontWeight: 600, marginBottom: '0.2rem' }}>{c.market_question || c.market_id}</div>
                    <div style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
                      {c.strategy} · {c.side.toUpperCase()} · price {c.price.toFixed(3)} · size {c.size.toFixed(2)}
                    </div>
                    <div style={{ color: '#64748b', fontSize: '0.8rem', marginTop: '0.2rem' }}>{c.reason}</div>
                  </button>
                )
              })}
            </div>
          )}
          <div style={{ marginTop: '0.75rem', display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
            <button
              type="button"
              onClick={placeSelected}
              disabled={!selected || placing}
              style={{
                padding: '0.5rem 0.9rem',
                background: '#0891b2',
                border: '1px solid #22d3ee',
                borderRadius: 6,
                color: '#ecfeff',
                cursor: selected && !placing ? 'pointer' : 'not-allowed',
                opacity: selected && !placing ? 1 : 0.65,
              }}
            >
              {placing ? 'Placing…' : 'Place bet'}
            </button>
            {selected && (
              <span style={{ color: '#94a3b8', fontSize: '0.85rem' }}>
                Selected: {selected.side.toUpperCase()} {selected.size.toFixed(2)} @ {selected.price.toFixed(3)}
              </span>
            )}
          </div>
        </>
      )}
    </div>
  )
}
