import { useWsStatus } from '../hooks/useWsStatus'
import { useBackendStatus } from '../hooks/useBackendStatus'
import { useStrategyRuntime } from '../hooks/useStrategyRuntime'

const selectStyle: React.CSSProperties = {
  padding: '0.4rem 0.6rem',
  background: '#1e293b',
  border: '1px solid #475569',
  borderRadius: 6,
  color: '#e2e8f0',
  fontSize: '0.9rem',
  cursor: 'pointer',
  marginTop: '0.25rem',
  width: '100%',
  maxWidth: 220,
}

export function StrategyCard() {
  const { status, connected, error: wsError } = useWsStatus()
  const restStatus = useBackendStatus()
  const { strategies, active, available, error: runtimeError, setting, setActiveStrategy } = useStrategyRuntime()

  const displayMode = status?.mode || restStatus?.mode || '—'
  const displayStrategy = status?.strategy_name ?? restStatus?.strategy_name ?? active ?? '—'

  return (
    <div className="card">
      <h3>Strategy &amp; mode</h3>
      {(wsError || runtimeError) && (
        <p style={{ margin: '0 0 0.5rem', color: '#f87171', fontSize: '0.85rem' }}>
          {wsError ?? runtimeError}
        </p>
      )}
      {(connected || restStatus) && (
        <div>
          <p style={{ margin: 0 }}>
            <span style={{ color: '#94a3b8' }}>Mode:</span>{' '}
            <strong>{displayMode}</strong>
          </p>
          {available === true && strategies.length > 0 ? (
            <div style={{ marginTop: '0.5rem' }}>
              <label htmlFor="strategy-select" style={{ color: '#94a3b8', fontSize: '0.9rem' }}>
                Strategy:
              </label>
              <select
                id="strategy-select"
                value={active ?? ''}
                onChange={(e) => {
                  const v = e.target.value
                  if (v) setActiveStrategy(v)
                }}
                disabled={setting}
                style={selectStyle}
                aria-label="Active strategy"
              >
                {strategies.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
              {setting && <span style={{ marginLeft: '0.5rem', color: '#94a3b8', fontSize: '0.85rem' }}>Updating…</span>}
            </div>
          ) : (
            <p style={{ margin: '0.5rem 0 0' }}>
              <span style={{ color: '#94a3b8' }}>Strategy:</span>{' '}
              <strong>{displayStrategy}</strong>
            </p>
          )}
          {status?.uptime_seconds != null && (
            <p style={{ margin: '0.5rem 0 0', color: '#64748b', fontSize: '0.85rem' }}>
              Uptime {Math.floor((status.uptime_seconds ?? 0) / 60)}m
            </p>
          )}
          {available === false && (
            <p style={{ margin: '0.75rem 0 0', color: '#64748b', fontSize: '0.8rem' }}>
              Strategy is fixed in this mode (arbitrage/scrape). Run in trade or full mode to switch from the dashboard.
            </p>
          )}
        </div>
      )}
      {!connected && !restStatus && !wsError && <span className="loading">Connecting…</span>}
    </div>
  )
}
