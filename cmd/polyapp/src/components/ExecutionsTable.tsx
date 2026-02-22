import type { useArbExecutions } from '../hooks/useApi'
import type { ArbExecution } from '../api/client'

interface Props {
  arbExecutions: ReturnType<typeof useArbExecutions>
  /** Max rows to show (default 15). */
  rowLimit?: number
}

function formatDate(s: string | undefined) {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString()
  } catch {
    return s
  }
}

export function ExecutionsTable({ arbExecutions, rowLimit = 15 }: Props) {
  if (arbExecutions.loading) return <span className="loading">Loading…</span>
  if (arbExecutions.error) return <span className="error">{arbExecutions.error}</span>

  const rows = arbExecutions.data.slice(0, rowLimit)

  if (rows.length === 0) {
    return <p style={{ color: '#94a3b8', margin: 0 }}>No executions yet.</p>
  }

  return (
    <div style={{ overflowX: 'auto' }}>
      <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
        <thead>
          <tr style={{ borderBottom: '1px solid #334155', textAlign: 'left' }}>
            <th style={{ padding: '0.5rem 0.75rem', color: '#94a3b8', fontWeight: 500 }}>ID</th>
            <th style={{ padding: '0.5rem 0.75rem', color: '#94a3b8', fontWeight: 500 }}>Type</th>
            <th style={{ padding: '0.5rem 0.75rem', color: '#94a3b8', fontWeight: 500 }}>Status</th>
            <th style={{ padding: '0.5rem 0.75rem', color: '#94a3b8', fontWeight: 500 }}>Net PnL</th>
            <th style={{ padding: '0.5rem 0.75rem', color: '#94a3b8', fontWeight: 500 }}>Completed</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((e: ArbExecution) => (
            <tr key={e.ID} style={{ borderBottom: '1px solid #334155' }}>
              <td style={{ padding: '0.5rem 0.75rem', fontFamily: 'monospace', fontSize: '0.85rem' }}>{e.ID.slice(0, 8)}…</td>
              <td style={{ padding: '0.5rem 0.75rem' }}>{e.ArbType ?? '—'}</td>
              <td style={{ padding: '0.5rem 0.75rem' }}>{e.Status ?? '—'}</td>
              <td style={{ padding: '0.5rem 0.75rem' }} className={(e.NetPnLUSD ?? 0) >= 0 ? 'pnl-positive' : 'pnl-negative'}>
                ${(e.NetPnLUSD ?? 0).toFixed(2)}
              </td>
              <td style={{ padding: '0.5rem 0.75rem', color: '#94a3b8' }}>{formatDate(e.CompletedAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
