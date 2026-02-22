# Polybot Dashboard (React + Vite)

Web UI for the polymarket bot: health, mode, strategy, markets, arbitrage opportunities, PnL, and executions.

## Run

1. Start the backend (e.g. `make run-backend`) on port 8000.
2. From this directory:

   ```bash
   npm install
   npm run dev
   ```

3. Open http://localhost:5173. The app proxies `/api` and `/ws` to the backend.

If you see **`[vite] ws proxy socket error: Error: write EPIPE`** in the terminal, it’s harmless: it happens when the WebSocket is closed (e.g. you refresh the page or the backend restarts). The proxy is configured to treat EPIPE/ECONNRESET as normal.

## UI behaviour

- **Refresh**: Data auto-refreshes every 5 seconds. Use the **Refresh** button to fetch immediately; "Last updated" shows when you last clicked it.
- **Strategy**: The "Strategy & mode" card shows the backend’s current **mode** and **strategy name** from the WebSocket. The active strategy is chosen in backend config at startup; the dashboard does not change it. To switch strategy, update config and restart the backend.

## Env

- `VITE_API_URL` — optional; if unset, requests use relative URLs and Vite proxies to `http://localhost:8000`.

## Build

```bash
npm run build
npm run preview   # serve dist
```

---

## What the backend is doing

The backend (polybot) runs in one of several **modes** (e.g. `arbitrage`, `trade`, `full`), set in config.

- **HTTP server** (default port 8000): Serves health, markets, arbitrage recent/profit/executions, and a WebSocket at `/ws`. The dashboard gets strategy and mode from WebSocket status (`bot_status` on connect and status updates).

- **Arbitrage mode**: Loads markets and orderbooks, runs the selected **arbitrage strategy** (e.g. spread, imbalance, yes_no_spread) to detect opportunities, and can execute them. Recent opportunities and executions are stored and exposed via `/api/arbitrage/recent`, `/api/arbitrage/profit`, `/api/arbitrage/executions`.

- **Trade / Full mode**: Runs a **strategy engine** with a single active strategy (e.g. `rebalancing_arb`, `bond`, `flash_crash`) or multiple strategies from config. The engine gets orderbook/price updates (e.g. from Polymarket WebSocket or Redis), emits trade signals, and can place/cancel orders. Active strategy is set at startup from `config.strategy.name` or `config.strategy.active`; there is no HTTP API to switch it at runtime.

- **WebSocket** (`/ws`): Clients connect and receive an initial `bot_status` (mode, strategy_name, uptime). The hub also forwards events from the Redis signal bus (prices, orders, arb, etc.) to subscribed clients.
