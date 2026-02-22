import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from 'recharts'
import type { useArbProfit, useArbExecutions } from '../hooks/useApi'

interface Props {
  arbProfit: ReturnType<typeof useArbProfit>
  arbExecutions: ReturnType<typeof useArbExecutions>
  /** Label for the period (e.g. "last 7 days"). */
  periodLabel?: string
}

function byDay(executions: { CompletedAt?: string; NetPnLUSD?: number }[]) {
  const byDate: Record<string, number> = {}
  for (const e of executions) {
    const d = e.CompletedAt ? e.CompletedAt.slice(0, 10) : null
    if (!d) continue
    byDate[d] = (byDate[d] ?? 0) + (e.NetPnLUSD ?? 0)
  }
  const sorted = Object.entries(byDate).sort(([a], [b]) => a.localeCompare(b))
  return sorted.map(([date, pnl]) => ({ date, pnl: Math.round(pnl * 100) / 100 }))
}

export function PnLChart({ arbProfit, arbExecutions, periodLabel = 'last 7 days' }: Props) {
  if (arbProfit.loading && arbExecutions.loading) return <span className="loading">Loading…</span>
  if (arbProfit.error && arbExecutions.error) return <span className="error">Failed to load</span>

  const fromExecutions = byDay(arbExecutions.data)
  const totalFromApi = arbProfit.data?.total_pnl_usd ?? 0 // API returns snake_case

  return (
    <div>
      <p style={{ margin: '0 0 0.5rem', fontSize: '1.1rem' }}>
        Total PnL ({periodLabel}): <span className={totalFromApi >= 0 ? 'pnl-positive' : 'pnl-negative'}>${totalFromApi.toFixed(2)}</span>
      </p>
      {fromExecutions.length === 0 ? (
        <p style={{ color: '#94a3b8', margin: 0 }}>No execution data by day yet.</p>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <BarChart data={fromExecutions} margin={{ top: 8, right: 8, left: 8, bottom: 8 }}>
            <XAxis dataKey="date" tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
            <Tooltip
              contentStyle={{ background: '#1e293b', border: '1px solid #334155', borderRadius: 6 }}
              labelStyle={{ color: '#e2e8f0' }}
              formatter={(value: number) => [`$${value.toFixed(2)}`, 'PnL']}
            />
            <Bar dataKey="pnl" radius={4}>
              {fromExecutions.map((entry, i) => (
                <Cell key={i} fill={entry.pnl >= 0 ? '#4ade80' : '#f87171'} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
