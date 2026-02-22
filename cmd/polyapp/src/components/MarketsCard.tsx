import type { useMarkets } from '../hooks/useApi'

interface Props {
  markets: ReturnType<typeof useMarkets>
}

export function MarketsCard({ markets }: Props) {
  return (
    <div className="card">
      <h3>Markets</h3>
      {markets.loading && <span className="loading">Loading…</span>}
      {markets.error && <span className="error">{markets.error}</span>}
      {!markets.loading && !markets.error && (
        <div>
          <strong>{markets.data.length}</strong> markets
        </div>
      )}
    </div>
  )
}
