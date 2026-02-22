# Polymarket Go Bot

Go backend for trading on Polymarket prediction markets: strategies, arbitrage detection, and a REST + WebSocket API. A React dashboard (`cmd/polyapp`) connects to the backend for monitoring and control.

## Prerequisites

- Go 1.21+
- Node.js 18+ (for the dashboard)
- PostgreSQL (e.g. Supabase or local)
- Redis
- Optional: S3-compatible storage, Polymarket wallet and API access

## Setup

**1. Clone and build**

```bash
git clone <repo-url>
cd polymarketbot
make build
```

**2. Configuration (no secrets in repo)**

- Copy the example config and env template; **do not commit your real config or `.env`**.

```bash
cp config.example.toml config.toml
cp .env.example .env
```

- Edit `config.toml` for your environment (modes, strategy, Supabase/Redis/S3 hosts, etc.).
- Edit `.env` and set all `POLYBOT_*` variables. Secrets (private keys, passwords, API keys) are read from env and override `config.toml`. Keep them only in `.env`.

**3. Security before git push**

- **Never commit** `.env` or `config.toml`. They are in `.gitignore`.
- Only `config.example.toml` and `.env.example` are safe to commit (no real credentials).

## Run

**Backend**

```bash
make run-backend
# Or: ./bin/polybot --config config.toml
```

HTTP server listens on port 8000 by default (override with `POLYBOT_SERVER_PORT` or `[server] port` in config).

**Dashboard**

```bash
cd cmd/polyapp
npm install
npm run dev
```

Open http://localhost:5173. The dev server proxies `/api` and `/ws` to the backend (port 8000).

## Modes

Configured via `mode` in config or `POLYBOT_MODE`:

- **full** — Strategy engine + arbitrage + HTTP server (typical for live use).
- **trade** — Strategy engine only (e.g. flash_crash, bond).
- **arbitrage** — Arbitrage detection and execution only.
- **monitor** — Read-only: price feeds, positions, HTTP server (no orders).

## Project layout

- `cmd/polybot` — Backend entrypoint.
- `cmd/polyapp` — React + Vite dashboard (monitoring, markets, arb, executions).
- `internal/app` — App lifecycle, mode selection, wiring.
- `internal/server` — HTTP handlers, WebSocket hub, middleware.
- `internal/strategy` — Trading strategies (e.g. flash_crash, mean_reversion).
- `internal/arbitrage` — Arb strategies and detector.
- `config.example.toml` — Example config; copy to `config.toml`.
- `.env.example` — Example env vars; copy to `.env`.

## License

Private / use as you see fit.
