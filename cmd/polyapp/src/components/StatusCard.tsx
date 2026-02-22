import type { useHealth } from '../hooks/useApi'

interface Props {
  health: ReturnType<typeof useHealth>
}

export function StatusCard({ health }: Props) {
  return (
    <div className="card">
      <h3>Backend</h3>
      {health.loading && <span className="loading">Checking…</span>}
      {health.error && <span className="error">Offline: {health.error}</span>}
      {health.data && (
        <div>
          <span style={{ color: '#4ade80' }}>●</span> {health.data.status}
          {health.data.version != null && (
            <span style={{ color: '#94a3b8', marginLeft: '0.5rem' }}>v{health.data.version}</span>
          )}
        </div>
      )}
    </div>
  )
}
