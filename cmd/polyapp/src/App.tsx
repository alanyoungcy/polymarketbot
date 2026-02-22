import { useState } from 'react'
import { useHealth, useMarkets, useArbRecent, useArbProfit, useArbExecutions, useStrategyCandidates } from './hooks/useApi'
import { PnLChart } from './components/PnLChart'
import { ExecutionsChart } from './components/ExecutionsChart'
import { StatusCard } from './components/StatusCard'
import { MarketsCard } from './components/MarketsCard'
import { ExecutionsTable } from './components/ExecutionsTable'
import { StrategyCard } from './components/StrategyCard'
import { StrategyCandidatesCard } from './components/StrategyCandidatesCard'

const selectStyle: React.CSSProperties = {
  padding: '0.4rem 0.6rem',
  background: '#1e293b',
  border: '1px solid #475569',
  borderRadius: 6,
  color: '#e2e8f0',
  fontSize: '0.9rem',
  cursor: 'pointer',
}

function App() {
  const health = useHealth()
  const markets = useMarkets()
  const arbRecent = useArbRecent()
  const [profitDays, setProfitDays] = useState(7)
  const arbProfit = useArbProfit(profitDays)
  const arbExecutions = useArbExecutions()
  const strategyCandidates = useStrategyCandidates(20)
  const [executionRows, setExecutionRows] = useState(15)
  const [lastUpdated, setLastUpdated] = useState<number | null>(null)

  const refreshAll = () => {
    health.refetch()
    markets.refetch()
    arbRecent.refetch()
    arbProfit.refetch()
    arbExecutions.refetch()
    strategyCandidates.refetch()
    setLastUpdated(Date.now())
  }

  return (
    <div style={{ padding: '1.5rem', maxWidth: 1400, margin: '0 auto' }}>
      <header style={{ marginBottom: '1.5rem', display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: '0.75rem' }}>
        <div>
          <h1 style={{ margin: 0, fontSize: '1.75rem', fontWeight: 600 }}>Polybot Dashboard</h1>
          <p style={{ margin: '0.25rem 0 0', color: '#94a3b8', fontSize: '0.95rem' }}>
            Arbitrage opportunities, PnL, and markets · auto-refresh every 30s
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
          {lastUpdated != null && (
            <span style={{ color: '#64748b', fontSize: '0.85rem' }}>
              Last updated {new Date(lastUpdated).toLocaleTimeString()}
            </span>
          )}
          <button
            type="button"
            onClick={refreshAll}
            style={{
              padding: '0.5rem 1rem',
              background: '#334155',
              border: '1px solid #475569',
              borderRadius: 6,
              color: '#e2e8f0',
              cursor: 'pointer',
              fontSize: '0.9rem',
            }}
          >
            Refresh
          </button>
        </div>
      </header>

      <div className="card" style={{ marginBottom: '1.5rem' }}>
        <h3 style={{ marginTop: 0 }}>About this dashboard</h3>
        <p style={{ margin: '0 0 0.5rem', color: '#94a3b8', fontSize: '0.9rem' }}>
          This dashboard monitors your Polybot backend and can place manual bets from strategy candidates. Data refreshes every 30 seconds; use <strong>Refresh</strong> to fetch now.
        </p>
        <p style={{ margin: '0.5rem 0 0', color: '#94a3b8', fontSize: '0.9rem' }}>
          <strong>When does data show up?</strong> <strong>Markets</strong> appear once the backend has loaded or scraped markets. <strong>Recent arb opportunities</strong> appear when the backend is running in a mode that runs the arbitrage strategy (e.g. trade/full or arbitrage) and has detected opportunities. <strong>Recent executions</strong> and <strong>Profit</strong> appear only after the bot has actually executed at least one arbitrage trade (and execution recording is enabled).
        </p>
        <p style={{ margin: '0.5rem 0 0', color: '#94a3b8', fontSize: '0.9rem' }}>
          <strong>Why is everything empty?</strong> Markets are <strong>Polymarket</strong> prediction markets (scraped from Polymarket’s Gamma API into your Postgres DB). In <code>full</code> or <code>scrape</code> mode the backend runs a market scraper on startup and then every 5 minutes. If you see no markets: (1) Check backend logs for <code>market scrape failed</code> or <code>synced market batch</code> — the first run can take a minute or two. (2) Ensure Postgres and pipeline dependencies (e.g. S3 for blob) are configured so the pipeline starts. (3) You can trigger one scrape now with <code>POST /api/pipeline/trigger</code>. For <strong>arb opportunities</strong> to appear, set <code>arbitrage.enabled = true</code> in <code>config.toml</code> (and use a mode that runs the arb detector). <strong>Executions</strong> stay empty until the bot has executed at least one arbitrage trade.
        </p>
        <p style={{ margin: '0.5rem 0 0', color: '#94a3b8', fontSize: '0.9rem' }}>
          <strong>Manual bet flow:</strong> set <code>strategy.auto_execute = false</code> to keep scanning without auto-ordering. Then use <strong>Strategy candidates</strong> to select and place bets manually.
        </p>
        <ul style={{ margin: '0.5rem 0 0', paddingLeft: '1.25rem', color: '#94a3b8', fontSize: '0.9rem', lineHeight: 1.6 }}>
          <li><strong>You can select:</strong> profit period (7 or 30 days), how many execution rows to show, <strong>and the active strategy</strong> (in trade or full mode — in arbitrage/scrape mode strategy is fixed).</li>
          <li><strong>Strategy &amp; mode</strong> come from the backend. In trade/full mode use the strategy dropdown to switch; in other modes the backend does not support runtime switching.</li>
          <li>Charts and tables show live data from the API; no row or chart segment selection is implemented yet.</li>
        </ul>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: '1rem', marginBottom: '1.5rem' }}>
        <StatusCard health={health} />
        <StrategyCard />
        <MarketsCard markets={markets} />
        <StrategyCandidatesCard candidates={strategyCandidates} onOrderPlaced={refreshAll} />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem', marginBottom: '1.5rem' }}>
        <div className="card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: '0.5rem', marginBottom: '0.5rem' }}>
            <h3 style={{ margin: 0 }}>Profit</h3>
            <select
              value={profitDays}
              onChange={(e) => setProfitDays(Number(e.target.value))}
              style={selectStyle}
              aria-label="Profit period"
            >
              <option value={7}>Last 7 days</option>
              <option value={30}>Last 30 days</option>
            </select>
          </div>
          <PnLChart arbProfit={arbProfit} arbExecutions={arbExecutions} periodLabel={profitDays === 7 ? 'last 7 days' : 'last 30 days'} />
        </div>
        <div className="card">
          <h3>Executions over time</h3>
          <ExecutionsChart arbExecutions={arbExecutions} />
        </div>
      </div>

      <div style={{ display: 'grid', gap: '1rem' }}>
        <div className="card">
          <h3>Recent arb opportunities</h3>
          {arbRecent.loading && <span className="loading">Loading…</span>}
          {arbRecent.error && <span className="error">{arbRecent.error}</span>}
          {!arbRecent.loading && !arbRecent.error && (
            <p style={{ margin: 0, color: '#94a3b8' }}>
              {arbRecent.data.length} opportunities (last 7 days). Executed: {arbRecent.data.filter((o) => o.Executed).length}.
            </p>
          )}
        </div>
        <div className="card">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: '0.5rem', marginBottom: '0.5rem' }}>
            <h3 style={{ margin: 0 }}>Recent executions</h3>
            <select
              value={executionRows}
              onChange={(e) => setExecutionRows(Number(e.target.value))}
              style={selectStyle}
              aria-label="Number of rows"
            >
              <option value={10}>Show 10</option>
              <option value={15}>Show 15</option>
              <option value={25}>Show 25</option>
              <option value={50}>Show 50</option>
            </select>
          </div>
          <ExecutionsTable arbExecutions={arbExecutions} rowLimit={executionRows} />
        </div>
      </div>
    </div>
  )
}

export default App
