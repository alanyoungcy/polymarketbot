import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts'
import type { useArbExecutions } from '../hooks/useApi'

interface Props {
  arbExecutions: ReturnType<typeof useArbExecutions>
}

function countByDay(executions: { CompletedAt?: string }[]) {
  const byDate: Record<string, number> = {}
  for (const e of executions) {
    const d = e.CompletedAt ? e.CompletedAt.slice(0, 10) : null
    if (!d) continue
    byDate[d] = (byDate[d] ?? 0) + 1
  }
  return Object.entries(byDate)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([date, count]) => ({ date, count }))
}

export function ExecutionsChart({ arbExecutions }: Props) {
  if (arbExecutions.loading) return <span className="loading">Loading…</span>
  if (arbExecutions.error) return <span className="error">{arbExecutions.error}</span>

  const data = countByDay(arbExecutions.data)
  if (data.length === 0) {
    return <p style={{ color: '#94a3b8', margin: 0 }}>No executions yet.</p>
  }

  return (
    <ResponsiveContainer width="100%" height={220}>
      <BarChart data={data} margin={{ top: 8, right: 8, left: 8, bottom: 8 }}>
        <XAxis dataKey="date" tick={{ fill: '#94a3b8', fontSize: 11 }} />
        <YAxis tick={{ fill: '#94a3b8', fontSize: 11 }} />
        <Tooltip
          contentStyle={{ background: '#1e293b', border: '1px solid #334155', borderRadius: 6 }}
          formatter={(value: number) => [value, 'Executions']}
        />
        <Bar dataKey="count" fill="#6366f1" radius={4} />
      </BarChart>
    </ResponsiveContainer>
  )
}
