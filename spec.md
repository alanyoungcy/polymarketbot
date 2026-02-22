# Polymarket Go Bot - Technical Specification v2

## 1. Vision

A production-grade Go trading system for Polymarket prediction markets. The Go backend is a **headless server** — it runs all trading logic, data pipelines, and exposes a REST + WebSocket API. The system uses **Supabase** (PostgreSQL + Realtime) as the source of truth, **Redis** as the hot-path cache and pub/sub bus, **S3-compatible storage** for bulk historical data, and **Protobuf** as the wire format for all inter-component communication.

---

## 2. Infrastructure Stack

```
┌────────────────────────────────────────────────────────────────────┐
│                         Go Binary (polybot)                        │
│          Goroutines, channels, context-driven lifecycle             │
└──────┬────────────┬────────────────┬───────────────┬───────────────┘
       │            │                │               │
       ▼            ▼                ▼               ▼
  ┌─────────┐  ┌─────────┐  ┌──────────────┐  ┌──────────────┐
  │ Supabase│  │  Redis   │  │     S3       │  │  External    │
  │ (Postgres│  │          │  │  (MinIO /   │  │  APIs        │
  │  + Auth  │  │  Cache   │  │   AWS /     │  │              │
  │  + Real- │  │  Pub/Sub │  │   Cloudflare│  │  Polymarket  │
  │  time)   │  │  Locks   │  │   R2)       │  │  Kalshi      │
  │          │  │  Rate    │  │              │  │  Goldsky     │
  │          │  │  Limit   │  │              │  │  Gamma       │
  └─────────┘  └─────────┘  └──────────────┘  └──────────────┘
```

### Why Each Component

| Component | Role | Why Not Alternatives |
|---|---|---|
| **Supabase (PostgreSQL)** | Source of truth for all relational data: positions, orders, trades, strategy configs, arbitrage history, audit log | Managed Postgres with built-in auth, Row Level Security, and realtime subscriptions via WebSocket — replaces the need for a separate auth service and change-data-capture system |
| **Redis** | Hot-path cache (orderbooks, prices, market metadata), distributed rate limiting, pub/sub between goroutines that may run across instances, distributed locks for single-execution guarantees | In-memory speed for data that changes every 100ms; sorted sets are a natural fit for orderbook levels; pub/sub decouples producers from consumers without channel coupling |
| **S3-compatible** | Bulk storage for historical CSVs (Goldsky scrapes, processed trades), backtest datasets, strategy snapshots, encrypted key backups | Cheap, durable, append-friendly; keeps the database lean by offloading cold data; any S3-compatible backend works (AWS S3, MinIO self-hosted, Cloudflare R2) |

---

## 3. Data Ownership Map

Every piece of data has exactly one authoritative home:

| Data | Primary Store | Cache/Index | Bulk Archive |
|---|---|---|---|
| **Open positions** | Supabase `positions` | Redis hash `pos:{wallet}` | — |
| **Order history** | Supabase `orders` | Redis (last 100 per market) | S3 monthly partitions |
| **Trade fills** | Supabase `trades` | Redis stream `fills` (last 1h) | S3 `trades/YYYY/MM/DD.parquet` |
| **Orderbook snapshots** | — (ephemeral) | Redis sorted sets `book:{asset}:bids`, `book:{asset}:asks` | S3 snapshots (if backtesting) |
| **Current prices** | — (ephemeral) | Redis hash `price:{asset}` | — |
| **Market metadata** | Supabase `markets` | Redis hash `market:{id}` (TTL 5min) | — |
| **Strategy config** | Supabase `strategy_configs` | Redis hash `stratcfg:{name}` | — |
| **Arbitrage opportunities** | Supabase `arb_history` | Redis stream `arb:signals` | S3 monthly |
| **Arb executions + PnL** | Supabase `arb_executions` + `arb_execution_legs` | Redis `arb:session:pnl*` counters, stream `arb:exec` | S3 monthly |
| **Condition groups** | Supabase `condition_groups` + junction | Redis hash `cg:{id}` (TTL 10min) | — |
| **Bond positions** | Supabase `bond_positions` | — | — |
| **Market relations** | Supabase `market_relations` | — | — |
| **Goldsky raw events** | — | — | S3 `goldsky/orderFilled/YYYY-MM-DD.csv` |
| **Backtest datasets** | — | — | S3 `backtest/{dataset_id}/` |
| **Audit / event log** | Supabase `audit_log` | — | S3 yearly archive |
| **API sessions / JWT** | Supabase Auth | Redis `session:{token}` (TTL) | — |
| **Rate limit counters** | — | Redis `rl:{endpoint}:{window}` | — |
| **Distributed locks** | — | Redis `lock:{resource}` (SETNX + TTL) | — |

---

## 4. High-Level Architecture

```
                           ┌─────────────────────────────────────┐
                           │   Web / API clients (e.g. dashboard)  │
                           │   REST + WebSocket                    │
                           └──────────┬──────────────────────────┘
                                      │
┌─────────────────────────────────────▼──────────────────────────────────────┐
│                         polybot (Go backend — headless)                     │
│                                                                            │
│  ┌──────┐┌──────┐┌──────┐┌──────┐┌──────┐┌──────┐┌──────┐┌──────┐       │
│  │WS Hub││Mkt   ││Strat ││Exec  ││Arb   ││Pipe- ││API   ││Notif │       │
│  │      ││Disc  ││Engine││utor  ││Det   ││line  ││Srvr  ││ier   │       │
│  └──┬───┘└──┬───┘└──┬───┘└──┬───┘└──┬───┘└──┬───┘└──┬───┘└──┬───┘       │
│     └───────┴───────┴───────┴───────┴───────┴───────┴───────┘            │
│                              │              │              │              │
│                     ┌────────┴──┐    ┌──────┴──┐    ┌─────┴──────┐       │
│                     │  store.*  │    │ cache.* │    │  blob.*    │       │
│                     │ (Supabase)│    │ (Redis) │    │  (S3)      │       │
│                     └───────────┘    └─────────┘    └────────────┘       │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Layered Module Architecture

The codebase follows a **clean architecture** with explicit dependency direction: outer layers depend on inner layers, never the reverse.

```
       ┌──────────────────────────────────┐
       │    cmd/polybot/ (backend)       │  ← wiring, main()
       └──────────────┬─────────────────-┘
                      │
       ┌──────────────▼──────────────────┐
       │     internal/app/               │  ← orchestration, lifecycle
       └──────────────┬─────────────────-┘
                      │
       ┌──────────────▼──────────────────┐
       │     internal/server/            │  ← headless API (REST + WS)
       └──────────────┬─────────────────-┘
                      │
         ┌────────────┼────────────────────────┐
         │            │                        │
┌────────▼─────────┐ │ ┌──────────────────────▼──┐
│ internal/service/ │ │ │ internal/strategy/       │
│ (business logic)  │ │ │ internal/executor/       │
└────────┬─────────┘ │ │ internal/pipeline/        │
         │            │ └──────────┬──────────────-┘
         └────────────┼────────────┘
                      │
       ┌──────────────▼──────────────────┐
       │       internal/domain/          │  ← pure types, interfaces
       └──────────────┬─────────────────-┘
                      │
         ┌────────────┼────────────────────────┐
         │            │                        │
┌────────▼─────────┐ ▼                ┌───────▼──────────┐
│ internal/store/  │ internal/cache/  │ internal/blob/   │
│ (Supabase/PG)    │ (Redis)          │ (S3)             │
└──────────────────┘                  └──────────────────┘
         │            │                        │
         └────────────┼────────────────────────┘
                      │
       ┌──────────────▼──────────────────┐
       │     internal/platform/          │  ← external API clients
       │  polymarket/ kalshi/ goldsky/   │
       └─────────────────────────────────┘
```

### Dependency Rule

- `domain` depends on nothing (pure Go types and interfaces)
- `store`, `cache`, `blob` implement `domain` interfaces
- `platform` wraps external APIs, returns `domain` types
- `service` contains business logic, depends only on `domain` interfaces
- `strategy` and `pipeline` use `service` layer
- `app` wires everything together
- `cmd` calls `app`

---

## 6. Protobuf Serialization Layer

All inter-goroutine messages, Redis pub/sub payloads, Redis stream entries, and WebSocket push frames use **Protocol Buffers** as the wire format. This replaces ad-hoc JSON marshaling with a schema-enforced, compact, zero-copy-friendly binary protocol.

### 6.1 Why Protobuf

| Concern | JSON | Protobuf |
|---|---|---|
| **Serialization speed** | `encoding/json` reflect-based, ~5μs per orderbook | `proto.Marshal` generated code, ~200ns per orderbook |
| **Payload size** | ~800 bytes for a 10-level orderbook | ~180 bytes (4-5x smaller) |
| **Schema enforcement** | Runtime panics on field mismatch | Compile-time safety via generated types |
| **Backward compatibility** | Fragile — rename a JSON key and consumers break | Field numbers are stable; old consumers ignore new fields |
| **Redis bandwidth** | High-frequency pub/sub (1000+ msg/s) amplifies JSON overhead | 4-5x less Redis memory + network for the same throughput |
| **Cross-language** | Only useful if every consumer is rewritten | `.proto` files generate clients for Python, TypeScript, Rust — web frontends and analytics scripts can all consume the same streams |

### 6.2 Proto Definitions

```
proto/
├── polybot/v1/
│   ├── market.proto              # Market, TokenPair, MarketStatus
│   ├── orderbook.proto           # OrderbookSnapshot, PriceLevel, PriceChange, BBO
│   ├── order.proto               # Order, OrderResult, OrderSide, OrderType
│   ├── position.proto            # Position, PositionStatus, PnL
│   ├── trade.proto               # Trade, Fill, TradeDirection
│   ├── signal.proto              # TradeSignal, ArbOpportunity, SignalUrgency
│   ├── pipeline.proto            # PipelineStatus, ScrapeProgress, ArchiveResult
│   └── events.proto              # Wrapper envelope for all pub/sub messages
└── buf.yaml                      # Buf configuration (linting, breaking change detection)
```

#### Key Proto Files

```protobuf
// proto/polybot/v1/orderbook.proto
syntax = "proto3";
package polybot.v1;

import "google/protobuf/timestamp.proto";

message PriceLevel {
  double price = 1;
  double size  = 2;
}

message OrderbookSnapshot {
  string                    asset_id  = 1;
  repeated PriceLevel       bids      = 2;
  repeated PriceLevel       asks      = 3;
  double                    best_bid  = 4;
  double                    best_ask  = 5;
  double                    mid_price = 6;
  google.protobuf.Timestamp timestamp = 7;
}

message PriceChange {
  string asset_id = 1;
  string side     = 2;   // "BUY" or "SELL"
  double price    = 3;
  double size     = 4;   // 0 means remove level
  google.protobuf.Timestamp timestamp = 5;
}

message LastTradePrice {
  string asset_id = 1;
  double price    = 2;
  double size     = 3;
  google.protobuf.Timestamp timestamp = 4;
}
```

```protobuf
// proto/polybot/v1/signal.proto
syntax = "proto3";
package polybot.v1;

import "google/protobuf/timestamp.proto";

enum SignalUrgency {
  SIGNAL_URGENCY_UNSPECIFIED = 0;
  SIGNAL_URGENCY_LOW         = 1;
  SIGNAL_URGENCY_MEDIUM      = 2;
  SIGNAL_URGENCY_HIGH        = 3;
  SIGNAL_URGENCY_IMMEDIATE   = 4;
}

enum OrderSide {
  ORDER_SIDE_UNSPECIFIED = 0;
  ORDER_SIDE_BUY         = 1;
  ORDER_SIDE_SELL        = 2;
}

message TradeSignal {
  string          id          = 1;
  string          source      = 2;   // strategy name
  string          market_id   = 3;
  string          token_id    = 4;
  OrderSide       side        = 5;
  double          price       = 6;   // display value only; executor converts to fixed-point ticks
  double          size        = 7;   // display value only; executor converts to integer base units
  SignalUrgency   urgency     = 8;
  string          reason      = 9;
  map<string, string> metadata = 10;
  google.protobuf.Timestamp created_at  = 11;
  google.protobuf.Timestamp expires_at  = 12;
}

message ArbOpportunity {
  string id                = 1;
  string poly_market_id    = 2;
  string poly_token_id     = 3;
  double poly_price        = 4;
  string kalshi_market_id  = 5;
  double kalshi_price      = 6;
  double gross_edge_bps    = 7;
  string direction         = 8;
  double max_amount        = 9;
  google.protobuf.Timestamp detected_at = 10;
  int64  duration_ms       = 11;
  bool   executed          = 12;
  double est_fee_bps       = 13;
  double est_slippage_bps  = 14;
  double est_latency_bps   = 15;
  double net_edge_bps      = 16;
  double expected_pnl_usd  = 17;
}
```

```protobuf
// proto/polybot/v1/events.proto
syntax = "proto3";
package polybot.v1;

import "google/protobuf/timestamp.proto";
import "polybot/v1/orderbook.proto";
import "polybot/v1/signal.proto";
import "polybot/v1/order.proto";
import "polybot/v1/position.proto";

// Envelope wrapping all pub/sub and stream messages.
// Every Redis pub/sub payload and WebSocket push frame is an Event.
message Event {
  string          event_id  = 1;   // UUID
  string          type      = 2;   // "orderbook_snapshot", "price_change", "trade_signal", ...
  google.protobuf.Timestamp timestamp = 3;

  oneof payload {
    OrderbookSnapshot  orderbook_snapshot  = 10;
    PriceChange        price_change        = 11;
    LastTradePrice     last_trade_price    = 12;
    TradeSignal        trade_signal        = 13;
    ArbOpportunity     arb_opportunity     = 14;
    OrderResult        order_result        = 15;
    PositionUpdate     position_update     = 16;
    BotStatus          bot_status          = 17;
  }
}

message BotStatus {
  string mode           = 1;
  bool   ws_connected   = 2;
  int64  uptime_seconds = 3;
  int32  open_positions = 4;
  int32  open_orders    = 5;
  string strategy_name  = 6;
}
```

### 6.3 Where Protobuf Is Used

| Communication Path | Before | After |
|---|---|---|
| **Go channel payloads** (goroutine ↔ goroutine) | Native Go structs | Native Go structs generated from `.proto` — same performance, but types are shared with external consumers |
| **Redis pub/sub** (`ch:book:*`, `ch:price:*`, `ch:signal`, `ch:arb`, `ch:order`, `ch:status`) | `json.Marshal` | `proto.Marshal` — 4-5x smaller, 10-25x faster serialization |
| **Redis streams** (`stream:fills`, `stream:signals`, `stream:arb`) | JSON byte field | Protobuf byte field — compact storage, replayable with any protobuf-capable consumer |
| **Redis hash values** (`price:{asset}`, `pos:{wallet}:{id}`) | JSON string | Protobuf bytes — smaller memory footprint in Redis |
| **Client WebSocket** (backend → clients) | JSON | Protobuf binary frames (Go clients decode with `proto.Unmarshal`; web clients can use `@bufbuild/protobuf`) |
| **S3 archived data** | CSV / Parquet | Protobuf length-delimited streams (`.pb.gz`) for replay; CSV/Parquet still available as export format |

### 6.4 Code Generation

```makefile
# Makefile targets

PROTO_DIR    = proto
GEN_GO_DIR   = internal/pb

.PHONY: proto
proto:
	buf generate

.PHONY: proto-lint
proto-lint:
	buf lint

.PHONY: proto-breaking
proto-breaking:
	buf breaking --against '.git#branch=main'
```

```yaml
# buf.gen.yaml
version: v2
plugins:
  - remote: buf.build/protocolbuffers/go
    out: internal/pb
    opt: paths=source_relative
  - remote: buf.build/grpc/go            # only if gRPC is added later
    out: internal/pb
    opt: paths=source_relative
```

Generated Go code lands in `internal/pb/polybot/v1/` and is imported by all layers that need to serialize/deserialize.

### 6.5 Domain ↔ Protobuf Mapping

The `domain` layer keeps its own pure Go structs (no protobuf dependency). A thin **mapper** package converts between domain types and protobuf types at the boundary:

```
internal/
├── domain/
│   └── orderbook.go         # domain.OrderbookSnapshot (pure Go)
├── pb/
│   └── polybot/v1/
│       └── orderbook.pb.go  # pbv1.OrderbookSnapshot (generated)
└── pbconv/
    ├── orderbook.go          # ToProto(domain.OrderbookSnapshot) → *pbv1.OrderbookSnapshot
    │                         # FromProto(*pbv1.OrderbookSnapshot) → domain.OrderbookSnapshot
    ├── signal.go             # ToProto / FromProto for TradeSignal, ArbOpportunity
    ├── order.go
    ├── position.go
    └── event.go              # WrapEvent(payload) → *pbv1.Event
```

This keeps the domain layer free of generated code and wire-format concerns, while the `pbconv` package is a pure mapping layer with no business logic.

### 6.6 SignalBus Interface (Updated for Protobuf)

```go
// domain/cache.go — updated

type SignalBus interface {
    // Publish sends protobuf-encoded bytes to Redis pub/sub.
    // Caller serializes with proto.Marshal at the boundary.
    Publish(ctx context.Context, channel string, payload []byte) error
    // Subscribe returns raw protobuf bytes; caller deserializes with proto.Unmarshal.
    Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
    // StreamAppend writes a protobuf-encoded entry to a Redis stream.
    StreamAppend(ctx context.Context, stream string, payload []byte) error
    // StreamRead returns raw protobuf bytes.
    StreamRead(ctx context.Context, stream string, lastID string, count int) ([]StreamMessage, error)
}
```

### 6.7 API WebSocket Protocol

The backend pushes `Event` protobuf messages over WebSocket binary frames. Any client (web frontend, scripts) connects to the same endpoint:

```
Client connects to ws://host:port/ws
  ← Server sends Event{type: "bot_status", payload: BotStatus{...}}    (binary frame)
  ← Server sends Event{type: "orderbook_snapshot", payload: ...}       (binary frame)
  → Client sends subscription request (JSON text frame for simplicity):
    {"subscribe": ["orderbook", "signals", "positions"]}
  ← Server streams matching Event messages as binary frames
```

**Go client decoding**:
```go
for {
    _, msg, err := conn.ReadMessage()
    if err != nil { break }

    var event pbv1.Event
    if err := proto.Unmarshal(msg, &event); err != nil { continue }

    switch p := event.Payload.(type) {
    case *pbv1.Event_OrderbookSnapshot:
        client.handleOrderbook(p.OrderbookSnapshot)
    case *pbv1.Event_TradeSignal:
        client.handleSignal(p.TradeSignal)
    case *pbv1.Event_PositionUpdate:
        client.handlePosition(p.PositionUpdate)
    }
}
```

**JSON fallback**: For debugging or curl-based access, the API server also exposes `GET /api/stream` with `protojson.Marshal` (human-readable JSON output from the same proto messages).

---

## 7. Project Layout

```
polymarketbot/
├── cmd/
│   ├── polybot/
│   │   └── main.go                       # Backend entry: config load, wire, run
│   └── polyapp/                          # Web dashboard (React + Vite)
│
├── proto/                                # ── PROTOBUF DEFINITIONS ──
│   ├── buf.yaml                          # Buf workspace config
│   ├── buf.gen.yaml                      # Code generation config
│   └── polybot/v1/
│       ├── market.proto
│       ├── orderbook.proto
│       ├── order.proto
│       ├── position.proto
│       ├── trade.proto
│       ├── signal.proto
│       ├── pipeline.proto
│       └── events.proto                  # Event envelope (oneof all message types)
│
├── internal/
│   ├── pb/                               # ── GENERATED CODE (do not edit) ──
│   │   └── polybot/v1/
│   │       ├── market.pb.go
│   │       ├── orderbook.pb.go
│   │       ├── order.pb.go
│   │       ├── position.pb.go
│   │       ├── trade.pb.go
│   │       ├── signal.pb.go
│   │       ├── pipeline.pb.go
│   │       └── events.pb.go
│   │
│   ├── pbconv/                           # ── PROTO ↔ DOMAIN MAPPERS ──
│   │   ├── orderbook.go
│   │   ├── signal.go
│   │   ├── order.go
│   │   ├── position.go
│   │   ├── market.go
│   │   └── event.go                      # WrapEvent / UnwrapEvent helpers
│   │
│   ├── app/
│   │   ├── app.go                        # Application struct, lifecycle
│   │   ├── wire.go                       # Dependency injection wiring
│   │   └── modes.go                      # Mode-specific goroutine sets
│   │
│   ├── domain/                           # ── LAYER 0: Pure types & interfaces ──
│   │   ├── market.go                     # Market, TokenPair, MarketStatus
│   │   ├── order.go                      # Order, OrderSide, OrderType, OrderResult
│   │   ├── position.go                   # Position, PositionSide, PnL
│   │   ├── trade.go                      # Trade, Fill, TradeDirection
│   │   ├── orderbook.go                  # OrderbookSnapshot, PriceLevel, PriceChange
│   │   ├── signal.go                     # TradeSignal, ArbOpportunity, ArbExecution, ArbLeg
│   │   ├── condition_group.go            # ConditionGroup, PriceSum helper
│   │   ├── multi_leg.go                  # LegPolicy (all_or_none, best_effort, sequential)
│   │   ├── market_relation.go            # MarketRelation, RelationType
│   │   ├── bond_position.go              # BondPosition, BondStatus
│   │   ├── errors.go                     # Sentinel errors (ErrNotFound, etc.)
│   │   │
│   │   ├── store.go                      # Repository interfaces
│   │   ├── cache.go                      # Cache interfaces
│   │   └── blob.go                       # Blob interfaces
│   │
│   ├── store/                            # ── LAYER 1a: Supabase/PostgreSQL ──
│   │   ├── postgres/
│   │   │   ├── client.go                 # Connection pool (*pgxpool.Pool)
│   │   │   ├── migrations/               # SQL migration files (embed.FS)
│   │   │   │   ├── 001_markets.sql
│   │   │   │   ├── 002_orders.sql
│   │   │   │   ├── 003_positions.sql
│   │   │   │   ├── 004_trades.sql
│   │   │   │   ├── 005_arb_history.sql
│   │   │   │   ├── 006_strategy_configs.sql
│   │   │   │   ├── 007_audit_log.sql
│   │   │   │   ├── 008_condition_groups.sql
│   │   │   │   ├── 009_bond_positions.sql
│   │   │   │   └── 010_market_relations.sql
│   │   │   ├── market_store.go           # implements domain.MarketStore
│   │   │   ├── order_store.go            # implements domain.OrderStore
│   │   │   ├── position_store.go         # implements domain.PositionStore
│   │   │   ├── trade_store.go            # implements domain.TradeStore
│   │   │   ├── arb_store.go              # implements domain.ArbStore
│   │   │   ├── arb_execution_store.go    # implements domain.ArbExecutionStore
│   │   │   ├── audit_store.go            # implements domain.AuditStore
│   │   │   ├── condition_group_store.go  # implements domain.ConditionGroupStore
│   │   │   ├── bond_position_store.go    # implements domain.BondPositionStore
│   │   │   ├── relation_store.go         # implements domain.MarketRelationStore
│   │   │   └── strategy_config_store.go  # implements domain.StrategyConfigStore
│   │   └── supabase/
│   │       ├── auth.go                   # Supabase Auth (JWT verification)
│   │       └── realtime.go               # Supabase Realtime subscription client
│   │
│   ├── cache/                            # ── LAYER 1b: Redis ──
│   │   └── redis/
│   │       ├── client.go                 # Connection pool (go-redis/redis/v9)
│   │       ├── price_cache.go            # implements domain.PriceCache
│   │       ├── orderbook_cache.go        # implements domain.OrderbookCache
│   │       ├── market_cache.go           # implements domain.MarketCache
│   │       ├── rate_limiter.go           # implements domain.RateLimiter
│   │       ├── lock.go                   # implements domain.LockManager
│   │       ├── signal_bus.go             # implements domain.SignalBus (pub/sub)
│   │       └── scripts/                  # Lua scripts for atomic operations
│   │           ├── sliding_window.lua
│   │           └── orderbook_update.lua
│   │
│   ├── blob/                             # ── LAYER 1c: S3-compatible storage ──
│   │   └── s3/
│   │       ├── client.go
│   │       ├── writer.go                 # implements domain.BlobWriter
│   │       ├── reader.go                 # implements domain.BlobReader
│   │       └── archiver.go              # implements domain.Archiver
│   │
│   ├── platform/                         # ── LAYER 1d: External API clients ──
│   │   ├── polymarket/
│   │   │   ├── clob.go                   # CLOB REST client
│   │   │   ├── gamma.go                  # Gamma API (market + event discovery)
│   │   │   ├── relayer.go                # Gasless tx relayer
│   │   │   ├── ws.go                     # WebSocket feed client
│   │   │   └── types.go                  # API DTOs (APIMarket, APIEvent + converters)
│   │   ├── kalshi/
│   │   │   ├── client.go                 # Kalshi REST client (RSA auth)
│   │   │   ├── ws.go                     # Kalshi WebSocket feed
│   │   │   └── types.go
│   │   └── goldsky/
│   │       └── client.go                 # GraphQL client for on-chain events
│   │
│   ├── crypto/                           # ── CROSS-CUTTING: Signing & encryption ──
│   │   ├── keymanager.go
│   │   ├── signer.go                     # EIP-712 signing
│   │   └── hmac.go                       # Builder HMAC + L2 API HMAC
│   │
│   ├── service/                          # ── LAYER 2: Business logic ──
│   │   ├── market_service.go
│   │   ├── order_service.go              # includes ReplaceOrder for LP requoting
│   │   ├── position_service.go
│   │   ├── trade_service.go
│   │   ├── arb_service.go                # net-edge model + realized PnL computation
│   │   ├── price_service.go
│   │   ├── auth_service.go
│   │   ├── bond_tracker.go               # bond position lifecycle + resolution monitoring
│   │   ├── rewards_tracker.go            # LP reward eligibility + accrual tracking
│   │   └── relation_service.go           # relation discovery + implied price computation
│   │
│   ├── strategy/                         # ── LAYER 2: Strategy implementations ──
│   │   ├── engine.go                     # Multi-strategy engine (RunAll with errgroup)
│   │   ├── interface.go
│   │   ├── registry.go                   # Registry with ListInfo() for status tracking
│   │   ├── price_tracker.go
│   │   ├── flash_crash.go
│   │   ├── mean_reversion.go
│   │   ├── arb_strategy.go               # cross-platform arb pass-through
│   │   ├── rebalancing_arb.go            # market rebalancing arbitrage
│   │   ├── bond.go                       # high-probability bond strategy
│   │   ├── liquidity_provider.go         # two-sided LP quoting
│   │   └── combinatorial_arb.go          # cross-event combinatorial arb
│   │
│   ├── executor/                         # ── LAYER 2: Order execution ──
│   │   ├── executor.go                   # signal routing (single-leg vs multi-leg)
│   │   ├── dedup.go
│   │   └── leg_group.go                  # LegGroupAccumulator for multi-leg execution
│   │
│   ├── pipeline/                         # ── LAYER 2: Data pipeline ──
│   │   ├── orchestrator.go
│   │   ├── market_scraper.go
│   │   ├── goldsky_scraper.go
│   │   ├── trade_processor.go
│   │   └── archiver.go
│   │
│   ├── server/                           # ── LAYER 3: API server (headless) ──
│   │   ├── server.go                     # HTTP server setup, routing
│   │   ├── middleware/
│   │   │   ├── auth.go                   # JWT / API key validation
│   │   │   ├── ratelimit.go              # Per-client rate limiting (Redis)
│   │   │   └── logging.go
│   │   ├── handler/
│   │   │   ├── health.go                 # GET /api/health
│   │   │   ├── market.go                 # GET /api/markets
│   │   │   ├── order.go                  # POST/DELETE /api/orders
│   │   │   ├── position.go              # GET /api/positions
│   │   │   ├── arbitrage.go             # GET /api/arbitrage/*, /api/arbitrage/profit, /api/arbitrage/executions
│   │   │   ├── strategy.go              # GET/PUT /api/strategies (list, enable/disable, per-strategy config)
│   │   │   ├── bonds.go                 # GET /api/bonds (portfolio, APR, yields)
│   │   │   └── pipeline.go              # POST /api/pipeline/trigger
│   │   └── ws/
│   │       └── hub.go                    # WebSocket: Redis pub/sub → protobuf
│   │                                     #   frames to any connected client
│   │
│   ├── tui/                              # ── LAYER 3: Terminal UI client ──
│   │   ├── app.go                        # Bubble Tea program, model, update loop
│   │   ├── client.go                     # HTTP + WebSocket client to backend API
│   │   ├── views/
│   │   │   ├── dashboard.go              # Main dashboard: status, prices, PnL, active strategies
│   │   │   ├── orderbook.go              # Side-by-side bid/ask depth view
│   │   │   ├── positions.go              # Open positions table with live PnL
│   │   │   ├── orders.go                 # Open/recent orders table
│   │   │   ├── arbitrage.go              # Arb opportunities + profit by type + execution details
│   │   │   ├── logs.go                   # Scrollable event log
│   │   │   ├── strategy.go              # Multi-strategy dashboard: status, signals, enable/disable
│   │   │   └── bonds.go                 # Bond portfolio: APR, yields, resolution status
│   │   ├── components/
│   │   │   ├── table.go                  # Reusable table component
│   │   │   ├── sparkline.go              # Inline price sparkline
│   │   │   ├── statusbar.go              # Bottom bar: mode, connection, uptime
│   │   │   └── help.go                   # Keybinding help overlay
│   │   └── theme/
│   │       └── colors.go                 # Lipgloss color palette
│   │
│   ├── notify/                           # ── CROSS-CUTTING: Notifications ──
│   │   ├── notifier.go
│   │   ├── telegram.go
│   │   └── discord.go
│   │
│   └── config/                           # ── CROSS-CUTTING: Configuration ──
│       ├── config.go
│       ├── loader.go
│       └── secrets.go
│
├── migrations/
│   └── *.sql
│
├── config.example.toml
├── docker-compose.yml                    # Supabase + Redis + MinIO for local dev
├── go.mod
├── go.sum
├── Makefile
└── spec.md
```

---

## 8. Domain Layer (`internal/domain/`)

Pure Go types and interfaces. Zero external dependencies. This is the contract that all other layers implement or consume.

### 8.1 Core Types

```go
// market.go
type Market struct {
    ID          string
    Question    string
    Slug        string
    Outcomes    [2]string        // ["Yes","No"] or ["Up","Down"]
    TokenIDs    [2]string        // ERC-1155 token IDs (76-digit strings)
    ConditionID string
    NegRisk     bool
    Volume      float64
    Status      MarketStatus     // Active, Closed, Settled
    ClosedAt    *time.Time
    CreatedAt   time.Time
}

// order.go
type Order struct {
    ID              string
    MarketID        string
    TokenID         string
    Side            OrderSide      // Buy, Sell
    Type            OrderType      // GTC, GTD, FOK, FAK
    PriceTicks      int64          // fixed-point: price * 1e6
    SizeUnits       int64          // fixed-point: size  * 1e6
    MakerAmount     *big.Int       // integer notional used in signed payload
    TakerAmount     *big.Int       // integer quantity used in signed payload
    Status          OrderStatus    // Pending, Open, Matched, Cancelled, Failed
    Signature       string         // EIP-712 hex
    CreatedAt       time.Time
    FilledAt        *time.Time
}

// position.go
type Position struct {
    ID            string
    MarketID      string
    TokenID       string
    Side          string          // "token1" or "token2"
    Direction     OrderSide       // Buy or Sell inventory direction
    EntryPrice    float64
    CurrentPrice  float64
    Size          float64
    UnrealizedPnL float64
    RealizedPnL   float64
    TakeProfit    *float64
    StopLoss      *float64
    Status        PositionStatus  // Open, Closed
    OpenedAt      time.Time
    ClosedAt      *time.Time
    StrategyName  string
}

// signal.go
type TradeSignal struct {
    ID          string            // UUID for dedup
    Source      string            // strategy name or "arb_detector"
    MarketID    string
    TokenID     string
    Side        OrderSide
    PriceTicks  int64             // fixed-point price, 1e6 ticks
    SizeUnits   int64             // fixed-point size, 1e6 units
    Urgency     SignalUrgency     // Low, Medium, High, Immediate
    Reason      string            // human-readable
    Metadata    map[string]string
    CreatedAt   time.Time
    ExpiresAt   time.Time         // signal validity window
}

type ArbOpportunity struct {
    ID              string
    PolyMarketID    string
    PolyTokenID     string
    PolyPrice       float64
    KalshiMarketID  string
    KalshiPrice     float64
    GrossEdgeBps    float64
    Direction       string         // "poly_yes_kalshi_no" or reverse
    MaxAmount       float64
    EstFeeBps       float64
    EstSlippageBps  float64
    EstLatencyBps   float64
    NetEdgeBps      float64
    ExpectedPnLUSD  float64
    DetectedAt      time.Time
    Duration        time.Duration
    Executed        bool
}
```

### 8.1.2 Multi-Outcome & Arbitrage Types

```go
// condition_group.go — wraps N binary markets sharing one event
type ConditionGroup struct {
    ID          string           // Polymarket event/group slug
    Title       string           // e.g., "2024 US Presidential Election Winner"
    Markets     []Market         // constituent binary YES/NO markets
    Status      MarketStatus     // derived from constituent statuses
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// PriceSum returns the sum of YES prices across all constituent markets.
// For a well-formed group the sum should be close to 1.0.
func (cg ConditionGroup) PriceSum(yesPrices map[string]float64) float64 { ... }

// multi_leg.go — coordination policy for multi-leg signal groups
type LegPolicy string

const (
    LegPolicyAllOrNone   LegPolicy = "all_or_none"    // cancel all if any leg fails
    LegPolicySiblings    LegPolicy = "best_effort"     // place all, accept partials
    LegPolicySequential  LegPolicy = "sequential"      // wait for each leg before next
)

// market_relation.go — links between related condition groups (for combinatorial arb)
type MarketRelation struct {
    ID              string
    SourceGroupID   string          // e.g., "presidential-election-winner"
    TargetGroupID   string          // e.g., "presidential-party-winner"
    RelationType    RelationType    // implies, excludes, subset
    Confidence      float64         // 0.0–1.0, for auto-discovered relations
    Config          map[string]any  // manual overrides (price mapping rules)
    CreatedAt       time.Time
}

type RelationType string

const (
    RelationImplies  RelationType = "implies"   // A winning implies B winning
    RelationExcludes RelationType = "excludes"  // A winning excludes B winning
    RelationSubset   RelationType = "subset"    // A outcomes are a subset of B outcomes
)

// bond_position.go — tracks high-probability bond holdings to expiry
type BondPosition struct {
    ID              string
    MarketID        string
    TokenID         string
    EntryPrice      float64
    ExpectedExpiry  time.Time
    ExpectedAPR     float64         // (1 - entry_price) / entry_price * (365 / days_to_exp)
    Size            float64
    Status          BondStatus      // open, resolved_win, resolved_loss
    RealizedPnL     float64
    CreatedAt       time.Time
    ResolvedAt      *time.Time
}

type BondStatus string

const (
    BondOpen         BondStatus = "open"
    BondResolvedWin  BondStatus = "resolved_win"
    BondResolvedLoss BondStatus = "resolved_loss"
)

// arb_profit.go — realized PnL tracking per arbitrage execution
type ArbExecution struct {
    ID              string
    OpportunityID   string          // links to ArbOpportunity
    ArbType         ArbType         // rebalancing, combinatorial, cross_platform
    LegGroupID      string          // shared ID linking all legs
    Legs            []ArbLeg        // individual fills
    GrossEdgeBps    float64
    TotalFees       float64         // sum of all leg fees
    TotalSlippage   float64         // sum of realized slippage vs expected
    NetPnLUSD       float64         // realized profit/loss
    Status          ArbExecStatus   // pending, partial, filled, cancelled, failed
    StartedAt       time.Time
    CompletedAt     *time.Time
}

type ArbLeg struct {
    OrderID         string
    MarketID        string
    TokenID         string
    Side            OrderSide
    ExpectedPrice   float64
    FilledPrice     float64
    Size            float64
    FeeUSD          float64
    SlippageBps     float64         // (filled - expected) / expected * 10000
    Status          OrderStatus
}

type ArbType string

const (
    ArbTypeRebalancing    ArbType = "rebalancing"
    ArbTypeCombinatorial  ArbType = "combinatorial"
    ArbTypeCrossPlatform  ArbType = "cross_platform"
)

type ArbExecStatus string

const (
    ArbExecPending   ArbExecStatus = "pending"
    ArbExecPartial   ArbExecStatus = "partial"
    ArbExecFilled    ArbExecStatus = "filled"
    ArbExecCancelled ArbExecStatus = "cancelled"
    ArbExecFailed    ArbExecStatus = "failed"
)
```

### 8.1.1 Precision Rules

Execution-critical math is fixed-point:
- price scale = `1e6` (`PriceTicks`)
- size scale = `1e6` (`SizeUnits`)

Boundary conversions:
1. External API payloads and clients may use decimal strings / float display values.
2. `service` + `executor` convert once into fixed-point integers.
3. Signing, risk checks, and dedup always use integer values.
4. Storage can persist both integer canonical fields and decimal projections for analytics.

### 8.2 Repository Interfaces

```go
// store.go — Supabase/PostgreSQL contracts

type MarketStore interface {
    Upsert(ctx context.Context, market Market) error
    UpsertBatch(ctx context.Context, markets []Market) error
    GetByID(ctx context.Context, id string) (Market, error)
    GetByTokenID(ctx context.Context, tokenID string) (Market, error)
    GetBySlug(ctx context.Context, slug string) (Market, error)
    ListActive(ctx context.Context, opts ListOpts) ([]Market, error)
    Count(ctx context.Context) (int64, error)
}

type OrderStore interface {
    Create(ctx context.Context, order Order) error
    UpdateStatus(ctx context.Context, id string, status OrderStatus) error
    GetByID(ctx context.Context, id string) (Order, error)
    ListOpen(ctx context.Context, wallet string) ([]Order, error)
    ListByMarket(ctx context.Context, marketID string, opts ListOpts) ([]Order, error)
}

type PositionStore interface {
    Create(ctx context.Context, pos Position) error
    Update(ctx context.Context, pos Position) error
    Close(ctx context.Context, id string, exitPrice float64) error
    GetOpen(ctx context.Context, wallet string) ([]Position, error)
    GetByID(ctx context.Context, id string) (Position, error)
    ListHistory(ctx context.Context, wallet string, opts ListOpts) ([]Position, error)
}

type TradeStore interface {
    InsertBatch(ctx context.Context, trades []Trade) error
    GetLastTimestamp(ctx context.Context) (time.Time, error)
    ListByMarket(ctx context.Context, marketID string, opts ListOpts) ([]Trade, error)
    ListByWallet(ctx context.Context, wallet string, opts ListOpts) ([]Trade, error)
}

type ArbStore interface {
    Insert(ctx context.Context, opp ArbOpportunity) error
    MarkExecuted(ctx context.Context, id string) error
    ListRecent(ctx context.Context, limit int) ([]ArbOpportunity, error)
}

type AuditStore interface {
    Log(ctx context.Context, event string, detail map[string]any) error
    List(ctx context.Context, opts ListOpts) ([]AuditEntry, error)
}

type StrategyConfigStore interface {
    Get(ctx context.Context, name string) (StrategyConfig, error)
    Upsert(ctx context.Context, cfg StrategyConfig) error
    List(ctx context.Context) ([]StrategyConfig, error)
}

type ConditionGroupStore interface {
    Upsert(ctx context.Context, group ConditionGroup) error
    GetByID(ctx context.Context, id string) (ConditionGroup, error)
    GetByMarketID(ctx context.Context, marketID string) (ConditionGroup, error)
    ListActive(ctx context.Context, opts ListOpts) ([]ConditionGroup, error)
    ListMultiOutcome(ctx context.Context, opts ListOpts) ([]ConditionGroup, error) // groups with 3+ markets
}

type MarketRelationStore interface {
    Upsert(ctx context.Context, rel MarketRelation) error
    GetByID(ctx context.Context, id string) (MarketRelation, error)
    ListByGroup(ctx context.Context, groupID string) ([]MarketRelation, error)
    ListAll(ctx context.Context) ([]MarketRelation, error)
}

type BondPositionStore interface {
    Create(ctx context.Context, pos BondPosition) error
    Update(ctx context.Context, pos BondPosition) error
    GetOpen(ctx context.Context) ([]BondPosition, error)
    Resolve(ctx context.Context, id string, status BondStatus, pnl float64) error
    ListHistory(ctx context.Context, opts ListOpts) ([]BondPosition, error)
}

type ArbExecutionStore interface {
    Create(ctx context.Context, exec ArbExecution) error
    Update(ctx context.Context, exec ArbExecution) error
    GetByID(ctx context.Context, id string) (ArbExecution, error)
    GetByOpportunityID(ctx context.Context, oppID string) (ArbExecution, error)
    ListRecent(ctx context.Context, limit int) ([]ArbExecution, error)
    ListByType(ctx context.Context, arbType ArbType, opts ListOpts) ([]ArbExecution, error)
    // Aggregate profit reporting
    SumPnL(ctx context.Context, since time.Time) (float64, error)
    SumPnLByType(ctx context.Context, arbType ArbType, since time.Time) (float64, error)
}
```

### 8.3 Cache Interfaces

```go
// cache.go — Redis contracts

type PriceCache interface {
    SetPrice(ctx context.Context, assetID string, price float64, ts time.Time) error
    GetPrice(ctx context.Context, assetID string) (float64, time.Time, error)
    GetPrices(ctx context.Context, assetIDs []string) (map[string]float64, error)
}

type OrderbookCache interface {
    // Store full orderbook snapshot as sorted sets
    SetSnapshot(ctx context.Context, assetID string, snap OrderbookSnapshot) error
    GetSnapshot(ctx context.Context, assetID string) (OrderbookSnapshot, error)
    // Incremental updates
    UpdateLevel(ctx context.Context, assetID string, side string, price, size float64) error
    GetBBO(ctx context.Context, assetID string) (bestBid, bestAsk float64, err error)
}

type MarketCache interface {
    Set(ctx context.Context, market Market) error
    Get(ctx context.Context, id string) (Market, error)
    GetByToken(ctx context.Context, tokenID string) (Market, error)
    Invalidate(ctx context.Context, id string) error
}

type RateLimiter interface {
    // Sliding window rate limiter
    Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
    // Token bucket for outgoing API calls
    Wait(ctx context.Context, key string) error
}

type LockManager interface {
    // Distributed lock for single-execution guarantees
    Acquire(ctx context.Context, key string, ttl time.Duration) (unlock func(), err error)
}

type SignalBus interface {
    // Pub/sub for cross-goroutine communication that also works cross-instance
    Publish(ctx context.Context, channel string, payload []byte) error
    Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
    // Streams for durable, replayable event logs
    StreamAppend(ctx context.Context, stream string, payload []byte) error
    StreamRead(ctx context.Context, stream string, lastID string, count int) ([]StreamMessage, error)
}

type ConditionGroupCache interface {
    Set(ctx context.Context, group ConditionGroup) error
    Get(ctx context.Context, id string) (ConditionGroup, error)
    GetByMarketID(ctx context.Context, marketID string) (ConditionGroup, error)
    Invalidate(ctx context.Context, id string) error
}
```

### 8.4 Blob Interfaces

```go
// blob.go — S3 contracts

type BlobWriter interface {
    // Upload a blob (CSV, Parquet, JSON) to a path
    Put(ctx context.Context, path string, data io.Reader, contentType string) error
    // Multipart upload for large files
    PutMultipart(ctx context.Context, path string, data io.Reader, partSize int64) error
}

type BlobReader interface {
    Get(ctx context.Context, path string) (io.ReadCloser, error)
    List(ctx context.Context, prefix string) ([]BlobInfo, error)
    Exists(ctx context.Context, path string) (bool, error)
}

type Archiver interface {
    // Move old data from Supabase to S3
    ArchiveTrades(ctx context.Context, before time.Time) (int64, error)
    ArchiveOrders(ctx context.Context, before time.Time) (int64, error)
    ArchiveArbHistory(ctx context.Context, before time.Time) (int64, error)
}
```

---

## 9. Supabase Schema (`internal/store/postgres/migrations/`)

### 9.1 Tables

```sql
-- 001_markets.sql
CREATE TABLE markets (
    id              TEXT PRIMARY KEY,
    question        TEXT NOT NULL,
    slug            TEXT UNIQUE,
    outcome_1       TEXT NOT NULL,
    outcome_2       TEXT NOT NULL,
    token_id_1      TEXT NOT NULL,      -- 76-digit ERC-1155 token ID
    token_id_2      TEXT NOT NULL,
    condition_id    TEXT,
    neg_risk        BOOLEAN DEFAULT FALSE,
    volume          NUMERIC(20,2) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'active',  -- active, closed, settled
    closed_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_markets_token1 ON markets(token_id_1);
CREATE INDEX idx_markets_token2 ON markets(token_id_2);
CREATE INDEX idx_markets_slug ON markets(slug);
CREATE INDEX idx_markets_status ON markets(status);

-- 002_orders.sql
CREATE TABLE orders (
    id              TEXT PRIMARY KEY,     -- from CLOB API
    market_id       TEXT REFERENCES markets(id),
    token_id        TEXT NOT NULL,
    wallet          TEXT NOT NULL,
    side            TEXT NOT NULL CHECK (side IN ('buy','sell')),
    order_type      TEXT NOT NULL CHECK (order_type IN ('GTC','GTD','FOK','FAK')),
    price_ticks     BIGINT NOT NULL,       -- canonical fixed-point price (1e6)
    size_units      BIGINT NOT NULL,       -- canonical fixed-point size (1e6)
    price           NUMERIC(10,6) NOT NULL CHECK (price >= 0 AND price <= 1),
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    filled_size     NUMERIC(20,6) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    signature       TEXT,
    strategy_name   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    filled_at       TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ
);
CREATE INDEX idx_orders_wallet_status ON orders(wallet, status);
CREATE INDEX idx_orders_market ON orders(market_id);
CREATE INDEX idx_orders_created ON orders(created_at);

-- 003_positions.sql
CREATE TABLE positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id       TEXT REFERENCES markets(id),
    token_id        TEXT NOT NULL,
    wallet          TEXT NOT NULL,
    side            TEXT NOT NULL CHECK (side IN ('token1','token2')),
    direction       TEXT NOT NULL CHECK (direction IN ('buy','sell')),
    entry_price     NUMERIC(10,6) NOT NULL CHECK (entry_price >= 0 AND entry_price <= 1),
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    take_profit     NUMERIC(10,6),
    stop_loss       NUMERIC(10,6),
    realized_pnl    NUMERIC(20,6) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    strategy_name   TEXT,
    opened_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at       TIMESTAMPTZ,
    exit_price      NUMERIC(10,6)
);
CREATE INDEX idx_positions_wallet_status ON positions(wallet, status);
CREATE INDEX idx_positions_market ON positions(market_id);

-- 004_trades.sql
CREATE TABLE trades (
    id              BIGSERIAL PRIMARY KEY,
    source          TEXT NOT NULL,           -- "polymarket" / "kalshi" / "goldsky"
    source_trade_id TEXT NOT NULL,           -- venue-native trade/fill identifier
    source_log_idx  BIGINT,                  -- on-chain log index when available
    timestamp       TIMESTAMPTZ NOT NULL,
    market_id       TEXT REFERENCES markets(id),
    maker           TEXT NOT NULL,
    taker           TEXT NOT NULL,
    token_side      TEXT NOT NULL CHECK (token_side IN ('token1','token2')),
    maker_direction TEXT NOT NULL CHECK (maker_direction IN ('buy','sell')),
    taker_direction TEXT NOT NULL CHECK (taker_direction IN ('buy','sell')),
    price           NUMERIC(10,6) NOT NULL CHECK (price >= 0 AND price <= 1),
    usd_amount      NUMERIC(20,6) NOT NULL CHECK (usd_amount >= 0),
    token_amount    NUMERIC(20,6) NOT NULL CHECK (token_amount > 0),
    tx_hash         TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_trades_source_idempotency
    ON trades(source, source_trade_id, COALESCE(source_log_idx, -1));
CREATE INDEX idx_trades_market_ts ON trades(market_id, timestamp);
CREATE INDEX idx_trades_maker ON trades(maker);
CREATE INDEX idx_trades_taker ON trades(taker);
CREATE INDEX idx_trades_timestamp ON trades(timestamp);
-- Partition by month for large-scale data
-- CREATE TABLE trades ... PARTITION BY RANGE (timestamp);

-- 005_arb_history.sql
CREATE TABLE arb_history (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    poly_market_id      TEXT,
    poly_token_id       TEXT,
    poly_price          NUMERIC(10,6) NOT NULL,
    kalshi_market_id    TEXT,
    kalshi_price        NUMERIC(10,6) NOT NULL,
    gross_edge_bps      NUMERIC(10,2) NOT NULL,
    est_fee_bps         NUMERIC(10,2) NOT NULL,
    est_slippage_bps    NUMERIC(10,2) NOT NULL,
    est_latency_bps     NUMERIC(10,2) NOT NULL,
    net_edge_bps        NUMERIC(10,2) NOT NULL,
    expected_pnl_usd    NUMERIC(20,6) NOT NULL,
    direction           TEXT NOT NULL CHECK (direction IN ('poly_yes_kalshi_no', 'poly_no_kalshi_yes')),
    max_amount          NUMERIC(20,6) CHECK (max_amount IS NULL OR max_amount > 0),
    detected_at         TIMESTAMPTZ NOT NULL,
    duration_ms         BIGINT,
    executed            BOOLEAN DEFAULT FALSE,
    executed_at         TIMESTAMPTZ
);
CREATE INDEX idx_arb_detected ON arb_history(detected_at);
CREATE INDEX idx_arb_net_edge ON arb_history(net_edge_bps);

-- 006_strategy_configs.sql
CREATE TABLE strategy_configs (
    name            TEXT PRIMARY KEY,
    config_json     JSONB NOT NULL,       -- flexible key-value for strategy params
    enabled         BOOLEAN DEFAULT TRUE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 007_audit_log.sql
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    event           TEXT NOT NULL,         -- 'order_placed','position_opened','arb_detected',...
    detail          JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_event ON audit_log(event, created_at);

-- 008_condition_groups.sql
CREATE TABLE condition_groups (
    id              TEXT PRIMARY KEY,        -- Polymarket event slug or ID
    title           TEXT NOT NULL,           -- e.g., "2024 US Presidential Election Winner"
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','closed','settled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE condition_group_markets (
    group_id        TEXT NOT NULL REFERENCES condition_groups(id),
    market_id       TEXT NOT NULL REFERENCES markets(id),
    PRIMARY KEY (group_id, market_id)
);
CREATE INDEX idx_cgm_market ON condition_group_markets(market_id);

-- 009_bond_positions.sql
CREATE TABLE bond_positions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    market_id       TEXT NOT NULL REFERENCES markets(id),
    token_id        TEXT NOT NULL,
    entry_price     NUMERIC(10,6) NOT NULL CHECK (entry_price > 0 AND entry_price <= 1),
    expected_expiry TIMESTAMPTZ NOT NULL,
    expected_apr    NUMERIC(10,4) NOT NULL,
    size            NUMERIC(20,6) NOT NULL CHECK (size > 0),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved_win','resolved_loss')),
    realized_pnl    NUMERIC(20,6) DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX idx_bond_status ON bond_positions(status);
CREATE INDEX idx_bond_market ON bond_positions(market_id);
CREATE INDEX idx_bond_expiry ON bond_positions(expected_expiry) WHERE status = 'open';

-- 010_market_relations.sql
CREATE TABLE market_relations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_group_id TEXT NOT NULL REFERENCES condition_groups(id),
    target_group_id TEXT NOT NULL REFERENCES condition_groups(id),
    relation_type   TEXT NOT NULL CHECK (relation_type IN ('implies','excludes','subset')),
    confidence      NUMERIC(5,4) DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    config          JSONB,                  -- manual overrides, price mapping rules
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_group_id, target_group_id, relation_type)
);
CREATE INDEX idx_mr_source ON market_relations(source_group_id);
CREATE INDEX idx_mr_target ON market_relations(target_group_id);

-- 011_arb_executions.sql
CREATE TABLE arb_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    opportunity_id  UUID REFERENCES arb_history(id),
    arb_type        TEXT NOT NULL CHECK (arb_type IN ('rebalancing','combinatorial','cross_platform')),
    leg_group_id    TEXT NOT NULL,
    gross_edge_bps  NUMERIC(10,2),
    total_fees      NUMERIC(20,6) DEFAULT 0,
    total_slippage  NUMERIC(20,6) DEFAULT 0,
    net_pnl_usd     NUMERIC(20,6) NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','partial','filled','cancelled','failed')),
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX idx_arb_exec_type ON arb_executions(arb_type);
CREATE INDEX idx_arb_exec_started ON arb_executions(started_at);
CREATE INDEX idx_arb_exec_status ON arb_executions(status);

CREATE TABLE arb_execution_legs (
    id              BIGSERIAL PRIMARY KEY,
    execution_id    UUID NOT NULL REFERENCES arb_executions(id),
    order_id        TEXT,
    market_id       TEXT REFERENCES markets(id),
    token_id        TEXT NOT NULL,
    side            TEXT NOT NULL CHECK (side IN ('buy','sell')),
    expected_price  NUMERIC(10,6) NOT NULL,
    filled_price    NUMERIC(10,6),
    size            NUMERIC(20,6) NOT NULL,
    fee_usd         NUMERIC(20,6) DEFAULT 0,
    slippage_bps    NUMERIC(10,2) DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending'
);
CREATE INDEX idx_arb_leg_exec ON arb_execution_legs(execution_id);
```

### 9.2 Supabase-Specific Features Used

| Feature | Usage |
|---|---|
| **Row Level Security** | Restrict API access by wallet address; bot uses service_role key to bypass |
| **Realtime** | Subscribe to `positions` and `orders` changes for push to connected clients |
| **Auth (optional)** | JWT-based auth for the API; API key for the bot itself |
| **Database Functions** | `pg_notify` for triggering on inserts (alternative to polling) |
| **Connection Pooling** | Use Supabase's built-in PgBouncer or `pgxpool` client-side pool |

### 9.3 Go Client

```
Driver: github.com/jackc/pgx/v5 + pgxpool
  - Pgx is the fastest pure-Go Postgres driver
  - pgxpool provides connection pooling with health checks
  - Native support for LISTEN/NOTIFY (for Supabase Realtime alternative)
  - Supports COPY protocol for bulk inserts (trades, Goldsky data)
```

---

## 10. Redis Usage (`internal/cache/redis/`)

### 10.1 Data Structures

```
┌─────────────────────────────────────────────────────────────────┐
│                         Redis Key Space                         │
│                                                                 │
│  HASHES (fast key-value lookup):                                │
│    price:{assetID}          → {protobuf PriceLevel}             │
│    market:{marketID}        → {protobuf Market}       TTL 5m    │
│    market:token:{tokenID}   → marketID               TTL 5m    │
│    pos:{wallet}:{posID}     → {protobuf Position}               │
│    stratcfg:{name}          → {protobuf StrategyConfig}         │
│    book:{assetID}:bid:size  → {price -> size}                   │
│    book:{assetID}:ask:size  → {price -> size}                   │
│    session:{token}          → {userID, wallet}        TTL 24h   │
│    cg:{groupID}             → {protobuf ConditionGroup} TTL 10m │
│    cg:market:{marketID}     → groupID                 TTL 10m   │
│                                                                 │
│  SORTED SETS (ordered data):                                    │
│    book:{assetID}:bids      → members=price, score=price        │
│    book:{assetID}:asks      → members=price, score=price        │
│    pricehistory:{assetID}   → members=protobuf, score=timestamp │
│                               (ring buffer via ZREMRANGEBYSCORE) │
│                                                                 │
│  STREAMS (durable event log — protobuf-encoded payloads):       │
│    stream:fills             → recent trade fills (maxlen 10000) │
│    stream:signals           → trade signals                     │
│    stream:arb               → arbitrage opportunities           │
│    stream:arb:exec          → arb execution results + PnL       │
│    stream:bond              → bond position events              │
│                                                                 │
│  PUB/SUB (ephemeral broadcast — protobuf Event envelope):       │
│    ch:price:{assetID}       → real-time price ticks             │
│    ch:book:{assetID}        → orderbook updates                 │
│    ch:signal                → trade signals (all strategies)    │
│    ch:arb                   → arb opportunities                 │
│    ch:arb:profit            → arb execution PnL updates         │
│    ch:order                 → order results / status transitions │
│    ch:status                → bot status changes                │
│    ch:bond                  → bond position status changes      │
│    ch:strategy:{name}       → per-strategy events               │
│                                                                 │
│  STRINGS (atomic counters / locks):                             │
│    rl:{endpoint}:{window}   → rate limit counter     TTL=window │
│    lock:{resource}          → lock holder ID         TTL=30s    │
│    pipeline:cursor:goldsky  → last scraped timestamp            │
│    pipeline:cursor:markets  → last scraped offset               │
│    pipeline:cursor:events   → last scraped event offset         │
│    arb:session:pnl          → session cumulative PnL (float)    │
│    arb:session:pnl:{type}   → PnL per arb type (float)         │
│    arb:session:count        → total executions (int)            │
│    arb:session:count:win    → profitable executions (int)       │
│    arb:session:count:loss   → unprofitable executions (int)     │
│                                                                 │
│  SETS:                                                          │
│    ws:subscribed             → set of currently subscribed      │
│                                asset IDs                        │
└─────────────────────────────────────────────────────────────────┘
```

### 10.2 Why Redis for Each Use Case

| Use Case | Redis Structure | Why Not Supabase |
|---|---|---|
| **Orderbook** | Sorted sets | Updates every 100ms; Postgres can't handle this write rate; store `score=price` so BBO lookup stays correct with O(log N) insert + top-of-book reads |
| **Current prices** | Hash | Sub-second reads needed; polling Postgres adds 5-10ms latency |
| **Price history** | Sorted set (sliding window) | Strategy needs last 10s of ticks in-memory; sorted set with `ZREMRANGEBYSCORE` acts as a ring buffer |
| **Signal broadcast** | Pub/sub | Strategy engine and clients (e.g. dashboard) consume signals; pub/sub is fire-and-forget with zero storage cost |
| **Signal replay** | Stream | If a client reconnects, it can read missed signals from stream with consumer groups |
| **Rate limiting** | String + INCR + EXPIRE | Atomic increment with TTL; Lua script for sliding window; <1ms per check |
| **Distributed lock** | String + SETNX + TTL | Prevents duplicate pipeline runs across instances; releases on crash via TTL |
| **Pipeline cursors** | String | Resume state for Goldsky/market scrapers; survives bot restart without touching Postgres |

### 10.3 Lua Scripts

**Sliding Window Rate Limiter** (`scripts/sliding_window.lua`):
```
Atomically: ZADD + ZREMRANGEBYSCORE + ZCARD
Returns: allowed (bool), remaining (int), reset_at (timestamp)
Eliminates race conditions in concurrent rate limit checks.
```

**Atomic Orderbook Update** (`scripts/orderbook_update.lua`):
```
Atomically: check if price level exists in sorted set
  If size > 0: ZADD (upsert price) + HSET size hash
  If size == 0: ZREM (remove price) + HDEL size hash
  Recompute BBO (ZRANGE for asks min score, ZREVRANGE for bids max score)
  HSET the BBO into a separate hash for O(1) lookup
```

### 10.4 Go Client

```
Driver: github.com/redis/go-redis/v9
  - Supports pipelines (batch multiple commands in one round-trip)
  - Supports Lua scripting (EVALSHA with script caching)
  - Supports pub/sub with automatic reconnect
  - Supports streams with consumer groups
  - Connection pooling built-in
```

---

## 11. S3 Usage (`internal/blob/s3/`)

### 11.1 Bucket Layout

```
polybot-data/
├── goldsky/
│   └── orderFilled/
│       ├── 2025-01-01.csv         # Daily partitioned raw on-chain fills
│       ├── 2025-01-02.csv
│       └── ...
├── trades/
│   └── processed/
│       ├── 2025/
│       │   ├── 01/
│       │   │   ├── 01.parquet     # Daily enriched trades (Parquet for analytics)
│       │   │   └── ...
│       │   └── ...
│       └── ...
├── orderbook-snapshots/           # Optional: periodic snapshots for backtesting
│   └── {assetID}/
│       └── {YYYY-MM-DD-HH}.json.gz
├── backtest/
│   └── {dataset_id}/
│       ├── config.json            # Backtest parameters
│       ├── trades.parquet         # Input data
│       └── results.json           # Output results
├── archive/                       # Cold storage from Supabase
│   ├── orders/
│   │   └── 2025-01.parquet
│   ├── arb_history/
│   │   └── 2025-01.parquet
│   └── audit_log/
│       └── 2025-01.parquet
└── keys/                          # Encrypted key backups
    └── {wallet}.enc
```

### 11.2 Data Lifecycle

```
HOT (Redis)                    WARM (Supabase)              COLD (S3)
───────────────────────────────────────────────────────────────────────
Orderbooks (live)              Positions (open)             Goldsky raw CSVs
Prices (current)               Orders (recent 90d)          Processed trades
Price history (10s window)     Trades (recent 90d)          Archived orders
Signals (current session)      Arb history (recent 90d)     Archived arb history
Rate limit counters            Strategy configs             Archived audit logs
Pipeline cursors               Audit log (recent 90d)       Backtest datasets
                               Markets (all)                Orderbook snapshots
```

**Archival schedule** (configurable, default monthly):
1. `Archiver` goroutine runs on cron (e.g., 1st of each month at 03:00 UTC)
2. Queries Supabase for records older than retention period (default 90 days)
3. Streams results to S3 as Parquet files (partitioned by month)
4. Deletes archived records from Supabase
5. Logs archival stats to `audit_log`

### 11.3 Go Client

```
Driver: github.com/aws/aws-sdk-go-v2/service/s3
  - Works with any S3-compatible backend (AWS, MinIO, Cloudflare R2)
  - Supports multipart uploads for large files
  - Pre-signed URLs for temporary client access to archived data
```

---

## 12. Goroutine Map

```
┌────────────────────────────────────────────────────────────────────────────┐
│                      polybot backend (errgroup.Group)                       │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                    DATA PLANE (hot path)                              │  │
│  │                                                                      │  │
│  │  WebSocket Hub ──► Redis (orderbook cache, price cache, pub/sub)     │  │
│  │       │                                                              │  │
│  │       ▼                                                              │  │
│  │  Multi-Strategy Engine ←── Price Tracker (Redis sorted set)          │  │
│  │       │  runs N strategies concurrently (one goroutine each)         │  │
│  │       │  ├── flash_crash                                             │  │
│  │       │  ├── mean_reversion                                          │  │
│  │       │  ├── rebalancing_arb ←── ConditionGroupStore                 │  │
│  │       │  ├── bond ←── BondPositionStore                              │  │
│  │       │  ├── liquidity_provider ←── RewardsTracker                   │  │
│  │       │  ├── combinatorial_arb ←── MarketRelationStore               │  │
│  │       │  └── arb (cross-platform pass-through)                       │  │
│  │       ▼                                                              │  │
│  │  Order Executor ──► Supabase (orders) + Redis (dedup, rate limit)   │  │
│  │       ├── single-leg: direct placement                               │  │
│  │       └── multi-leg: LegGroupAccumulator → FOK batch placement      │  │
│  │       ▼                                                              │  │
│  │  Position Manager ──► Supabase (positions) + Redis (position cache) │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                   CONTROL PLANE                                       │  │
│  │                                                                      │  │
│  │  Market Discovery ──► Supabase (markets) + Redis (market cache)     │  │
│  │  Event Scraper ──► ConditionGroups from Gamma /events API           │  │
│  │  Arbitrage Detector ──► Redis (pub/sub, stream) + Supabase (arb)    │  │
│  │  Bond Tracker ──► monitors bond positions to resolution             │  │
│  │  Rewards Tracker ──► queries LP reward eligibility from Gamma       │  │
│  │  Notifier ──► subscribes to Redis channels → Telegram / Discord     │  │
│  │                                                                      │  │
│  │  API Server (headless) ──► reads Supabase + Redis                   │  │
│  │    ├── REST handlers: /api/health, /orders, /positions, ...         │  │
│  │    ├── REST: /api/strategies (list active, stats, toggle)           │  │
│  │    ├── REST: /api/arbitrage/profit (realized PnL by type)           │  │
│  │    └── WS hub: Redis pub/sub → protobuf frames → connected clients │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
│                                                                            │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                  BACKGROUND PLANE                                     │  │
│  │                                                                      │  │
│  │  Pipeline Orchestrator:                                              │  │
│  │    Market Scraper ──► Supabase (markets) + Redis (market cache)      │  │
│  │    Event Scraper ──► Supabase (condition_groups) + Redis             │  │
│  │    Goldsky Scraper ──► S3 (raw CSVs) + Redis (cursor)               │  │
│  │    Trade Processor ──► Supabase (trades) + S3 (parquet)             │  │
│  │    Archiver ──► Supabase → S3 (cold storage)                        │  │
│  │  Relation Discovery ──► auto-links ConditionGroups by keywords      │  │
│  │  Health Monitor ──► Redis + Supabase + S3 self-checks               │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────┘
         ▲
         │  REST + WebSocket (protobuf binary frames)
         │
┌────────┴───────────────────────────────────────────────────────────────────┐
│                 Web dashboard / API clients (separate process)              │
│                                                                            │
│  WebSocket + REST: status, events, orders, arbitrage, executions            │
└────────────────────────────────────────────────────────────────────────────┘
```

---

## 13. Service Layer Patterns (`internal/service/`)

Services orchestrate domain logic using injected repository interfaces. They never import concrete store/cache/blob packages.

### 13.1 Example: `OrderService`

```go
type OrderService struct {
    orders     domain.OrderStore       // Supabase
    positions  domain.PositionStore    // Supabase
    book       domain.OrderbookCache   // Redis
    prices     domain.PriceCache       // Redis
    limiter    domain.RateLimiter      // Redis
    bus        domain.SignalBus        // Redis pub/sub
    audit      domain.AuditStore       // Supabase
    signer     *crypto.Signer         // EIP-712
    clob       *polymarket.ClobClient  // External API
    logger     *slog.Logger
}

// PlaceOrder: full lifecycle from signal to confirmed order
func (s *OrderService) PlaceOrder(ctx context.Context, sig domain.TradeSignal) (domain.OrderResult, error) {
    // 1. Rate limit check (Redis)
    if allowed, _ := s.limiter.Allow(ctx, "clob:post_order", 10, time.Second); !allowed {
        return domain.OrderResult{}, domain.ErrRateLimited
    }

    // 2. Build order from signal
    order := domain.Order{...}

    // 3. Sign (EIP-712)
    order.Signature = s.signer.SignOrder(order)

    // 4. Submit to CLOB API
    result, err := s.clob.PostOrder(ctx, order)
    if err != nil {
        return domain.OrderResult{}, err
    }

    // 5. Persist to Supabase
    if err := s.orders.Create(ctx, order); err != nil {
        return domain.OrderResult{}, err
    }

    // 6. Publish protobuf Event bytes to Redis for other goroutines
    event := pbconv.WrapEvent("order_result", pbconv.OrderResultToProto(result))
    payload, err := proto.Marshal(event)
    if err == nil {
        _ = s.bus.Publish(ctx, "ch:order", payload)
    }

    // 7. Audit log
    _ = s.audit.Log(ctx, "order_placed", map[string]any{"order_id": order.ID, ...})

    return result, nil
}
```

### 13.2 Example: `PriceService`

```go
type PriceService struct {
    priceCache  domain.PriceCache      // Redis
    bookCache   domain.OrderbookCache  // Redis
    bus         domain.SignalBus       // Redis pub/sub
}

// HandleBookUpdate: called by WebSocket Hub for every orderbook message
func (s *PriceService) HandleBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) error {
    // 1. Update Redis sorted sets (orderbook)
    s.bookCache.SetSnapshot(ctx, snap.AssetID, snap)

    // 2. Update price hash
    s.priceCache.SetPrice(ctx, snap.AssetID, snap.MidPrice, time.Now())

    // 3. Convert to protobuf and publish (strategies + connected clients subscribe)
    pbSnap := pbconv.OrderbookToProto(snap)
    event := pbconv.WrapEvent("orderbook_snapshot", pbSnap)
    payload, err := proto.Marshal(event)
    if err == nil {
        s.bus.Publish(ctx, "ch:book:"+snap.AssetID, payload)
    }

    return nil
}
```

### 13.3 Arbitrage Net-Edge Model & Profit Tracking

`ArbService` must evaluate opportunities using expected net edge, not raw spread:

```
gross_edge_bps = (synthetic_exit_price - synthetic_entry_price) * 10_000
net_edge_bps   = gross_edge_bps
                 - est_fee_bps
                 - est_slippage_bps
                 - est_latency_bps
expected_pnl_usd = notional_usd * net_edge_bps / 10_000
```

Execution gate (all must pass):
1. `net_edge_bps >= Arbitrage.MinNetEdgeBps`
2. `duration_ms >= Arbitrage.MinDurationMs`
3. projected unhedged exposure after leg-1 `<= Arbitrage.MaxUnhedgedNotional`
4. projected session PnL drawdown `>= -Arbitrage.KillSwitchLossUSD`

Legging policy:
1. Prefer passive leg on deeper venue, aggressive hedge on thinner venue.
2. If hedge is not complete within `Arbitrage.MaxLegGapMs`, force-close residual inventory at market.
3. Record partial fills and residual inventory as first-class state in `positions` + `arb_history`.

#### 13.3.1 Realized Profit Tracking

Every arbitrage execution is tracked end-to-end via `ArbExecution` + `ArbLeg` domain types. The executor creates an `ArbExecution` record when the first leg fills and updates it as subsequent legs complete.

```go
// arb_service.go — profit computation (called after all legs complete or timeout)
func (s *ArbService) ComputeRealizedPnL(exec *domain.ArbExecution) {
    var totalCost, totalRevenue, totalFees float64
    for _, leg := range exec.Legs {
        amount := leg.FilledPrice * leg.Size
        if leg.Side == domain.Buy {
            totalCost += amount
        } else {
            totalRevenue += amount
        }
        totalFees += leg.FeeUSD
        leg.SlippageBps = (leg.FilledPrice - leg.ExpectedPrice) / leg.ExpectedPrice * 10_000
    }
    exec.TotalFees = totalFees
    exec.TotalSlippage = sumSlippage(exec.Legs)
    exec.NetPnLUSD = totalRevenue - totalCost - totalFees
}
```

**Profit reporting queries** (exposed via `ArbExecutionStore`):

| Query | Method | Use |
|-------|--------|-----|
| Session PnL | `SumPnL(since sessionStart)` | Kill-switch check + dashboard |
| PnL by arb type | `SumPnLByType(ArbTypeRebalancing, since)` | Per-strategy profit attribution |
| Recent executions | `ListRecent(50)` | Arb profit feed (e.g. dashboard) |
| Per-opportunity detail | `GetByOpportunityID(oppID)` | Drill-down: legs, slippage, fees |

**Redis key for real-time profit** (updated on each leg fill):

```
arb:session:pnl          → float64 (session cumulative PnL)
arb:session:pnl:rebal    → float64 (rebalancing arb PnL)
arb:session:pnl:combo    → float64 (combinatorial arb PnL)
arb:session:pnl:xplat    → float64 (cross-platform arb PnL)
arb:session:count         → int    (total executions this session)
arb:session:count:win     → int    (profitable executions)
arb:session:count:loss    → int    (unprofitable executions)
```

**API endpoints for profit visibility**:

```
GET /api/arbitrage/profit                    → session PnL summary
GET /api/arbitrage/profit?type=rebalancing   → filtered by arb type
GET /api/arbitrage/profit?since=2025-01-01   → historical PnL
GET /api/arbitrage/executions                → recent executions with legs + PnL
GET /api/arbitrage/executions/:id            → single execution detail
```

### 13.4 New Services

#### `BondTracker` (`internal/service/bond_tracker.go`)

Tracks high-probability bond positions from entry to resolution:
- Monitors market resolution status via Gamma API polling
- Computes portfolio-level bond APR and expected yield
- On resolution: updates `BondPosition` status, records realized PnL
- Publishes bond events to SignalBus for client consumption

#### `RewardsTracker` (`internal/service/rewards_tracker.go`)

Queries Polymarket Gamma API for LP/holding reward eligibility:
- Identifies markets offering maker rewards
- Estimates accumulated rewards per market
- Feeds eligible market list to `liquidity_provider` strategy
- Tracks reward accruals for portfolio-level reporting

#### `RelationService` (`internal/service/relation_service.go`)

Discovers and manages relationships between condition groups:
- Auto-discovery via keyword matching and shared categories across groups
- Manual relation config via `market_relations` table
- `ComputeImpliedPrices(sourceGroup, targetGroup)`: given source group prices, calculates what target group prices should be based on the relation type
- Used by `combinatorial_arb` strategy to detect mispricing across related events

### 13.5 `OrderService` Enhancement: `ReplaceOrder`

```go
// ReplaceOrder atomically cancels an existing order and places a new one.
// Used by liquidity_provider strategy for requoting.
func (s *OrderService) ReplaceOrder(ctx context.Context, cancelID string, newSig domain.TradeSignal) (domain.OrderResult, error) {
    // 1. Cancel existing order
    if err := s.CancelOrder(ctx, cancelID); err != nil {
        return domain.OrderResult{}, fmt.Errorf("cancel leg of replace failed: %w", err)
    }
    // 2. Place new order (reuses PlaceOrder flow)
    return s.PlaceOrder(ctx, newSig)
}
```

---

## 13A. Multi-Strategy Engine Architecture

The strategy engine supports **concurrent execution of multiple strategies**. Each enabled strategy runs in its own goroutine, receives market data events, and emits signals to a shared channel consumed by the executor.

### 13A.1 Engine Design

```go
// engine.go — updated for multi-strategy

type Engine struct {
    registry      *Registry
    tracker       *PriceTracker
    signalCh      chan<- domain.TradeSignal
    activeNames   []string                    // list of enabled strategy names
    logger        *slog.Logger
}

// RunAll starts one goroutine per enabled strategy. Each strategy receives
// all market data events via its own fan-out channel. All strategies emit
// signals to the shared signalCh.
func (e *Engine) RunAll(ctx context.Context) error {
    g, ctx := errgroup.WithContext(ctx)
    for _, name := range e.activeNames {
        strat, _ := e.registry.Get(name)
        g.Go(func() error {
            return e.runStrategy(ctx, strat)
        })
    }
    return g.Wait()
}
```

### 13A.2 Strategy Lifecycle

```
┌───────────────┐
│    pending     │  ← registered in registry but not started
└──────┬────────┘
       │ Engine.RunAll()
       ▼
┌───────────────┐
│  initializing  │  ← Init() called, loads config/state
└──────┬────────┘
       │ Init() returns nil
       ▼
┌───────────────┐
│    running     │  ← receiving events, emitting signals
└──┬────────┬───┘
   │        │ context cancelled or fatal error
   │        ▼
   │  ┌───────────────┐
   │  │   stopping     │  ← Close() called, draining signals
   │  └──────┬────────┘
   │         │
   ▼         ▼
┌───────────────┐
│   stopped      │
└───────────────┘
```

### 13A.3 Strategy Registry (Updated)

```go
// The registry now supports listing all strategies with their runtime status
type StrategyInfo struct {
    Name       string
    Status     string   // "pending", "running", "stopped", "error"
    SignalsSent int64   // total signals emitted since start
    LastSignal *time.Time
    ErrorCount int64
}

func (r *Registry) ListInfo() []StrategyInfo { ... }
```

### 13A.4 Strategy Catalog

All strategies implement the existing `Strategy` interface. The engine fans out each market data event to all running strategies.

#### Existing Strategies (unchanged)

| Strategy | Type | Signal | Description |
|----------|------|--------|-------------|
| `flash_crash` | Single-leg | BUY | Detects sharp price drops vs recent average |
| `mean_reversion` | Single-leg | BUY/SELL | Z-score based mean reversion |
| `arb` | Single-leg (pass-through) | BUY/SELL | Re-emits arb_detector signals at IMMEDIATE urgency |

#### New Strategies

| Strategy | Type | Signal | Dependencies |
|----------|------|--------|--------------|
| `rebalancing_arb` | Multi-leg | N × BUY/SELL | ConditionGroupStore, PriceCache |
| `bond` | Single-leg | BUY | BondPositionStore, market metadata |
| `liquidity_provider` | Paired (bid+ask) | BUY + SELL | RewardsTracker, OrderService |
| `combinatorial_arb` | Multi-leg | N × BUY/SELL | MarketRelationStore, ConditionGroupStore |

---

### 13A.5 `rebalancing_arb` — Market Rebalancing Arbitrage

Exploits mispricing within a single Polymarket event (condition group). When the sum of YES prices across all outcomes deviates from 1.0, the strategy buys the underpriced side.

```go
// rebalancing_arb.go

type RebalancingArb struct {
    cfg           strategy.Config
    tracker       *PriceTracker
    groups        domain.ConditionGroupStore
    prices        domain.PriceCache
    groupStates   map[string]*GroupPriceState   // per-group YES/NO price tracking
    logger        *slog.Logger
}

type GroupPriceState struct {
    GroupID     string
    YesPrices  map[string]float64  // marketID → YES price
    NoPrices   map[string]float64  // marketID → NO price
    LastUpdate time.Time
}

// Config params
//   min_edge_bps:    50      (minimum deviation to trigger, in basis points)
//   max_group_size:  10      (skip groups with more than N outcomes)
//   size_per_leg:    5.0     (USDC per leg)
//   ttl_seconds:     30      (signal expiry)
//   max_stale_sec:   5       (all prices must be within this window)
```

**Signal generation logic**:
```
For each condition group:
  sum_yes = Σ yes_prices[i]
  if ALL prices refreshed within max_stale_sec:
    if sum_yes < 1.0 - min_edge:
      → BUY YES on all outcomes (long the group)
      → emit N signals with shared leg_group_id, policy=all_or_none
    if sum_yes > 1.0 + min_edge:
      → SELL YES on all outcomes (short the group)
      → emit N signals with shared leg_group_id, policy=all_or_none
```

---

### 13A.6 `bond` — High-Probability Bond Strategy

Buys high-probability YES tokens and holds to resolution, treating them like short-duration bonds. The return comes from the gap between purchase price (e.g., $0.97) and resolution ($1.00).

```go
// bond.go

type BondStrategy struct {
    cfg         strategy.Config
    tracker     *PriceTracker
    bonds       domain.BondPositionStore
    markets     domain.MarketStore
    logger      *slog.Logger
}

// Config params
//   min_yes_price:     0.95     (minimum YES price to qualify)
//   min_apr:           0.10     (10% annualized minimum)
//   min_volume:        100000   (minimum market volume in USD)
//   max_days_to_exp:   90       (maximum days to expected resolution)
//   min_days_to_exp:   7        (skip markets resolving too soon)
//   max_positions:     10       (portfolio diversification cap)
//   size_per_position: 50.0     (USDC per bond purchase)
```

**APR calculation**:
```
yield        = (1.0 - yes_price) / yes_price
days_to_exp  = (resolution_date - now).Days()
apr          = yield * (365 / days_to_exp)

Example: price=0.97, 30 days out
  yield = 0.03/0.97 = 3.09%
  apr   = 3.09% * (365/30) = 37.6% annualized
```

**Candidate filters** (all must pass):
1. `yes_price >= min_yes_price`
2. `apr >= min_apr`
3. `volume >= min_volume`
4. `min_days_to_exp <= days_to_exp <= max_days_to_exp`
5. Not already at `max_positions`

---

### 13A.7 `liquidity_provider` — Two-Sided Quoting

Places and maintains bid/ask quotes on eligible markets, earning the spread and optionally collecting LP rewards from Polymarket.

```go
// liquidity_provider.go

type LiquidityProvider struct {
    cfg           strategy.Config
    tracker       *PriceTracker
    rewards       *service.RewardsTracker
    activeQuotes  map[string]*QuotePair   // marketID → active bid/ask pair
    logger        *slog.Logger
}

type QuotePair struct {
    MarketID     string
    BidOrderID   string
    BidPrice     float64
    AskOrderID   string
    AskPrice     float64
    Size         float64
    LastQuoteAt  time.Time
}

// Config params
//   half_spread_bps:    50       (distance from mid to each quote)
//   requote_threshold:  0.005    (requote when mid moves > this amount)
//   size:               10.0     (USDC per side per market)
//   max_markets:        5        (maximum concurrent quoted markets)
//   min_volume:         50000    (minimum daily volume)
//   rewards_only:       true     (only quote on reward-eligible markets)
```

**Quoting logic**:
```
On each book update for a quoted market:
  new_mid = (best_bid + best_ask) / 2
  if |new_mid - last_quoted_mid| > requote_threshold:
    cancel existing bid + ask (via ReplaceOrder)
    new_bid = new_mid - half_spread
    new_ask = new_mid + half_spread
    emit BUY signal at new_bid + SELL signal at new_ask (paired, no leg_group_id)
```

**Risk controls**:
- Total LP exposure cap across all markets
- Auto-cancel all quotes on WebSocket disconnect
- Widen spread during high-volatility periods (tracked via PriceTracker volatility)

---

### 13A.8 `combinatorial_arb` — Cross-Event Arbitrage

Exploits mispricing between related condition groups. When event A outcomes imply specific prices for event B outcomes, this strategy detects and trades deviations.

```go
// combinatorial_arb.go

type CombinatorialArb struct {
    cfg         strategy.Config
    tracker     *PriceTracker
    groups      domain.ConditionGroupStore
    relations   domain.MarketRelationStore
    relSvc      *service.RelationService
    prices      domain.PriceCache
    logger      *slog.Logger
}

// Config params
//   min_edge_bps:    100      (wider threshold than rebalancing — more model risk)
//   max_relations:   10       (limit tracked relation pairs)
//   size_per_leg:    5.0      (USDC per leg)
```

**Signal generation logic**:
```
For each relation (source_group → target_group):
  implied_prices = RelationService.ComputeImpliedPrices(source_group, target_group)
  for each target_market:
    actual_price  = prices.Get(target_market.TokenIDs[0])
    implied_price = implied_prices[target_market.ID]
    deviation_bps = |actual - implied| / implied * 10_000
    if deviation_bps >= min_edge_bps:
      if actual < implied: BUY target_market YES
      if actual > implied: SELL target_market YES
      → emit signals with shared leg_group_id across source + target legs
```

---

## 13B. Multi-Leg Executor Architecture

The executor is enhanced with a `LegGroupAccumulator` that buffers related signals (sharing a `leg_group_id` in metadata) and places them atomically.

### 13B.1 Executor Signal Routing

```go
// executor.go — updated process() method

func (e *Executor) process(ctx context.Context, sig domain.TradeSignal) {
    legGroupID, hasLegGroup := sig.Metadata["leg_group_id"]
    if hasLegGroup {
        // Multi-leg: buffer until all legs arrive, then batch-place
        e.legAccum.Add(sig)
        return
    }
    // Single-leg: direct placement (existing flow)
    e.placeSingle(ctx, sig)
}
```

### 13B.2 `LegGroupAccumulator`

```go
// leg_group.go

type LegGroupAccumulator struct {
    groups     map[string]*PendingLegGroup   // leg_group_id → pending group
    maxGapMs   int64                          // max time between first and last leg
    onComplete func(ctx context.Context, legs []domain.TradeSignal, policy domain.LegPolicy) error
    mu         sync.Mutex
}

type PendingLegGroup struct {
    Legs      []domain.TradeSignal
    Expected  int                            // from metadata "leg_count"
    Policy    domain.LegPolicy               // from metadata "leg_policy"
    FirstSeen time.Time
    Timer     *time.Timer                    // fires at maxGapMs → timeout
}
```

**Accumulation flow**:
```
Signal arrives with leg_group_id="abc123", leg_count="4", leg_policy="all_or_none"

  1. If group "abc123" doesn't exist → create PendingLegGroup, start timeout timer
  2. Append signal to group.Legs
  3. If len(Legs) == Expected:
     → cancel timer
     → call onComplete(legs, policy)
       → for all_or_none: place all as FOK orders; if any fails, cancel filled legs
       → for best_effort: place all, accept partials
       → for sequential: place one at a time, abort on failure
  4. If timer fires before all legs arrive:
     → cancel partially accumulated group
     → log warning with received vs expected leg count
```

### 13B.3 Multi-Leg Execution Policies

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `all_or_none` | Place all legs as FOK. If any leg rejects/fails, cancel all filled legs. | Rebalancing arb — naked exposure without all legs |
| `best_effort` | Place all legs, accept partial fills. | Combinatorial arb — partial edge still profitable |
| `sequential` | Place leg 1, wait for fill, then leg 2, etc. Abort remaining on failure. | Cross-platform arb — latency-sensitive sequencing |

### 13B.4 Arb Execution Recording

After multi-leg placement completes (success or failure), the executor creates an `ArbExecution` record:

```go
func (e *Executor) recordArbExecution(ctx context.Context, legs []domain.TradeSignal, results []domain.OrderResult) {
    exec := domain.ArbExecution{
        ID:            uuid.New().String(),
        OpportunityID: legs[0].Metadata["opp_id"],
        ArbType:       domain.ArbType(legs[0].Metadata["arb_type"]),
        LegGroupID:    legs[0].Metadata["leg_group_id"],
        StartedAt:     time.Now(),
    }
    for i, leg := range legs {
        exec.Legs = append(exec.Legs, domain.ArbLeg{
            OrderID:       results[i].OrderID,
            MarketID:      leg.MarketID,
            TokenID:       leg.TokenID,
            Side:          leg.Side,
            ExpectedPrice: float64(leg.PriceTicks) / 1e6,
            FilledPrice:   results[i].FilledPrice,
            Size:          float64(leg.SizeUnits) / 1e6,
            FeeUSD:        results[i].FeeUSD,
            Status:        results[i].Status,
        })
    }
    e.arbSvc.ComputeRealizedPnL(&exec)
    e.arbExecStore.Create(ctx, exec)

    // Update real-time Redis counters
    e.updateSessionPnL(ctx, exec)
}

---

## 14. TUI Client (removed)

The former TUI (`cmd/polytui` + `internal/tui/`) was a separate Bubble Tea binary that connected to the backend over HTTP + WebSocket. It has been removed; the web dashboard (`cmd/polyapp`) is the supported UI. Clients (web or other) use the same REST + WebSocket API.

---

## 15. Configuration (Enhanced)

```
Config
├── Wallet
│   ├── PrivateKey          string   // raw hex or path to encrypted file
│   ├── SafeAddress         string   // Polymarket proxy wallet
│   ├── EncryptedKeyPath    string
│   └── KeyPassword         string   // env only
├── Polymarket
│   ├── ClobHost            string
│   ├── GammaHost           string
│   ├── WsHost              string
│   ├── ChainID             int
│   └── SignatureType       int
├── Builder
│   ├── ApiKey              string
│   ├── ApiSecret           string
│   └── ApiPassphrase       string
├── Kalshi
│   ├── ApiKey              string
│   ├── RsaPrivateKeyPath   string
│   └── BaseURL             string
├── Supabase
│   ├── Host                string   // e.g., "db.xxxx.supabase.co"
│   ├── Port                int      // 5432 (direct) or 6543 (pooler)
│   ├── Database            string   // "postgres"
│   ├── User                string   // "postgres" or service_role
│   ├── Password            string   // env only
│   ├── SSLMode             string   // "require"
│   ├── PoolMaxConns        int      // 10
│   ├── PoolMinConns        int      // 2
│   ├── ApiURL              string   // https://xxxx.supabase.co (for Auth/Realtime)
│   ├── ApiKey              string   // service_role key
│   └── RunMigrations       bool     // auto-run on startup
├── Redis
│   ├── Addr                string   // "localhost:6379" or Redis Cloud URL
│   ├── Password            string   // env only
│   ├── DB                  int      // 0
│   ├── PoolSize            int      // 20
│   ├── MaxRetries          int      // 3
│   └── TLSEnabled          bool
├── S3
│   ├── Endpoint            string   // MinIO: "localhost:9000", AWS: ""
│   ├── Region              string   // "us-east-1"
│   ├── Bucket              string   // "polybot-data"
│   ├── AccessKey           string   // env only
│   ├── SecretKey           string   // env only
│   ├── UseSSL              bool
│   └── ForcePathStyle      bool     // true for MinIO
├── Strategy                             // global defaults
│   ├── Coin                string
│   ├── Size                float64      // default size if not overridden per-strategy
│   ├── PriceScale          int          // fixed-point multiplier, default 1_000_000
│   ├── SizeScale           int          // fixed-point multiplier, default 1_000_000
│   ├── MaxPositions        int
│   ├── TakeProfit          float64
│   ├── StopLoss            float64
│   ├── Active              []string     // list of strategy names to run concurrently
│   │                                    // e.g., ["flash_crash", "bond", "rebalancing_arb"]
│   ├── FlashCrash                       // [strategy.flash_crash]
│   │   ├── Enabled         bool
│   │   ├── DropThreshold   float64      // 0.10
│   │   └── RecoveryTarget  float64      // 0.05
│   ├── MeanReversion                    // [strategy.mean_reversion]
│   │   ├── Enabled         bool
│   │   ├── StdDevThreshold float64      // 2.0
│   │   └── LookbackWindow  duration     // "5m"
│   ├── RebalancingArb                   // [strategy.rebalancing_arb]
│   │   ├── Enabled         bool
│   │   ├── MinEdgeBps      int          // 50
│   │   ├── MaxGroupSize    int          // 10
│   │   ├── SizePerLeg      float64      // 5.0
│   │   ├── TTLSeconds      int          // 30
│   │   └── MaxStaleSec     int          // 5
│   ├── Bond                             // [strategy.bond]
│   │   ├── Enabled         bool
│   │   ├── MinYesPrice     float64      // 0.95
│   │   ├── MinAPR          float64      // 0.10
│   │   ├── MinVolume       float64      // 100000
│   │   ├── MaxDaysToExp    int          // 90
│   │   ├── MinDaysToExp    int          // 7
│   │   ├── MaxPositions    int          // 10
│   │   └── SizePerPosition float64      // 50.0
│   ├── LiquidityProvider               // [strategy.liquidity_provider]
│   │   ├── Enabled         bool
│   │   ├── HalfSpreadBps   int          // 50
│   │   ├── RequoteThreshold float64     // 0.005
│   │   ├── Size            float64      // 10.0
│   │   ├── MaxMarkets      int          // 5
│   │   ├── MinVolume       float64      // 50000
│   │   └── RewardsOnly     bool         // true
│   ├── CombinatorialArb                 // [strategy.combinatorial_arb]
│   │   ├── Enabled         bool
│   │   ├── MinEdgeBps      int          // 100
│   │   ├── MaxRelations    int          // 10
│   │   └── SizePerLeg      float64      // 5.0
│   └── Arb                             // [strategy.arb] (cross-platform pass-through)
│       └── Enabled         bool
├── Arbitrage
│   ├── Enabled             bool
│   ├── MinNetEdgeBps       float64
│   ├── MaxTradeAmount      float64
│   ├── MaxTradesPerOpp     int
│   ├── MinDurationMs       int64
│   ├── MaxLegGapMs         int64    // max time between leg-1 fill and leg-2 hedge
│   ├── MaxUnhedgedNotional float64  // hard cap for temporary exposure
│   ├── PerVenueFeeBps      map[string]float64
│   ├── MaxSlippageBps      float64
│   └── KillSwitchLossUSD   float64  // disable arb if breached in session
├── Pipeline
│   ├── Enabled             bool
│   ├── GoldskyURL          string
│   ├── ScrapeInterval      duration
│   ├── ArchiveRetentionDays int    // 90
│   └── ArchiveCron         string  // "0 3 1 * *" (1st of month, 3am)
├── Server
│   ├── Enabled             bool
│   ├── Port                int
│   └── CORSOrigins         []string
├── Notify
│   ├── TelegramToken       string
│   ├── TelegramChatID      string
│   ├── DiscordWebhookURL   string
│   └── Events              []string  // which events trigger notifications
├── Mode                    string    // trade, arbitrage, monitor, scrape, backtest, server, full
└── LogLevel                string
```

---

## 16. Local Development

```yaml
# docker-compose.yml
services:
  supabase-db:
    image: supabase/postgres:15.6
    ports: ["5432:5432"]
    environment:
      POSTGRES_PASSWORD: postgres
    volumes:
      - pgdata:/var/lib/postgresql/data

  supabase-studio:
    image: supabase/studio:latest
    ports: ["3000:3000"]
    environment:
      STUDIO_PG_META_URL: http://supabase-meta:8080

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]
    command: redis-server --appendonly yes
    volumes:
      - redisdata:/data

  minio:
    image: minio/minio:latest
    ports: ["9000:9000", "9001:9001"]
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - miniodata:/data

volumes:
  pgdata:
  redisdata:
  miniodata:
```

One command to bring up the entire infrastructure locally:
```bash
docker-compose up -d
make migrate         # runs embedded SQL migrations against local Supabase
make run-backend     # starts the headless backend
```

---

## 17. Key Dependencies

| Package | Purpose |
|---|---|
| `google.golang.org/protobuf` | Protobuf runtime — `proto.Marshal`/`Unmarshal`, `protojson` for JSON fallback |
| `github.com/bufbuild/buf` | Proto linting, breaking change detection, code generation (dev tool) |
| `github.com/jackc/pgx/v5` | PostgreSQL driver (Supabase) — fastest pure-Go PG driver |
| `github.com/redis/go-redis/v9` | Redis client — pipelines, pub/sub, streams, Lua scripting |
| `github.com/aws/aws-sdk-go-v2/service/s3` | S3 client — works with AWS, MinIO, R2 |
| `github.com/ethereum/go-ethereum` | Keccak256, secp256k1, ABI — EIP-712 signing |
| `github.com/gorilla/websocket` | WebSocket client/server |
| `github.com/BurntSushi/toml` | Config file parsing |
| `golang.org/x/crypto` | PBKDF2, AES-GCM for key encryption |
| `golang.org/x/sync/errgroup` | Goroutine lifecycle management |
| `golang.org/x/time/rate` | Token bucket rate limiting (local fallback) |
| `log/slog` (stdlib) | Structured logging |

---

## 18. Cross-Cutting Concerns

### 18.1 Health Checks

A dedicated `HealthMonitor` goroutine periodically verifies all dependencies:

| Check | Method | Frequency |
|---|---|---|
| Supabase | `SELECT 1` | 30s |
| Redis | `PING` | 10s |
| S3 | `HeadBucket` | 60s |
| Polymarket CLOB | `GET /` | 30s |
| Polymarket WS | Last message age < 30s | 10s |
| Kalshi (if enabled) | `GET /exchange/status` | 30s |

Health state is published to Redis `ch:status` and exposed via `GET /api/health`.

### 18.2 Structured Logging

All components use `log/slog` with consistent fields:

```go
logger.Info("order placed",
    slog.String("component", "executor"),
    slog.String("order_id", result.ID),
    slog.String("market_id", signal.MarketID),
    slog.Int64("price_ticks", signal.PriceTicks),
    slog.Int64("size_units", signal.SizeUnits),
    slog.Duration("latency", elapsed),
)
```

Log output: JSON in production, text in development. Optionally ship to S3 via a log rotation tool.

### 18.3 Graceful Shutdown

```
1. SIGINT/SIGTERM → cancel root context
2. Multi-Strategy Engine: stop all strategy goroutines, stop emitting signals
3. LegGroupAccumulator: cancel pending leg groups, timeout in-flight multi-leg placements
4. Order Executor: drain queue, wait for in-flight orders (5s timeout)
5. Liquidity Provider: cancel all active quotes across all markets
6. Bond Tracker: flush bond position state to Supabase
7. Position Manager: flush open position state to Supabase
8. WebSocket Hub: close frames, disconnect
9. Pipeline: finish current batch, flush S3 uploads
10. API Server: http.Server.Shutdown(5s)
11. Notifier: send "bot shutting down" notification
12. Redis: flush pending pipelines, close pool
13. Supabase: close pgxpool
14. S3: abort any in-progress multipart uploads
15. errgroup.Wait() → exit
```

### 18.4 Testability

The interface-driven architecture enables testing at every layer:

| Layer | Test Strategy |
|---|---|
| `domain` | Pure unit tests — no dependencies |
| `service` | Unit tests with mock implementations of store/cache/blob interfaces |
| `store/postgres` | Integration tests against Dockerized Postgres (testcontainers-go) |
| `cache/redis` | Integration tests against Dockerized Redis |
| `platform/*` | Unit tests with HTTP mock servers (`httptest.NewServer`) |
| `strategy` | Unit tests with injected mock `StrategyDeps` |
| `server` | HTTP handler tests with `httptest.NewRecorder` |
| Full system | Docker-compose based end-to-end tests |

---

## 19. Operational Modes

| Mode | Goroutines Active | Infrastructure Required |
|---|---|---|
| `trade` | WS, Discovery, EventScraper, Multi-Strategy Engine (N strategies), Executor (with LegGroupAccumulator), Positions, BondTracker, RewardsTracker, Notifier | Supabase + Redis |
| `arbitrage` | Both WS, ArbDetector, Multi-Strategy Engine (arb strategies only), Executor, Positions, Notifier | Supabase + Redis |
| `monitor` | WS(s), Multi-Strategy Engine (read-only, no execution), Server, Notifier | Supabase + Redis |
| `scrape` | Pipeline + EventScraper + RelationDiscovery | Supabase + Redis (cursors) + S3 |
| `backtest` | Pipeline (read), Strategy (simulated) | S3 (read) + Redis (optional) |
| `server` | API Server only | Supabase + Redis |
| `full` | Everything | Supabase + Redis + S3 |

**Multi-strategy in trade mode**: The `Strategy.Active` config list determines which strategies run concurrently. Each gets its own goroutine and receives all market data events. All emit signals to the shared executor channel. Example config:

```toml
[strategy]
active = ["flash_crash", "bond", "rebalancing_arb", "liquidity_provider"]
```

---

## 20. Implementation Phases

### Phase 1 — Foundation
1. `proto/polybot/v1/` — all `.proto` definitions + `buf.gen.yaml`
2. `internal/pb/` — generated Go code from protobuf
3. `internal/domain/` — all types and interfaces
4. `internal/pbconv/` — domain ↔ protobuf mappers
5. `internal/config/` — TOML + env loading with validation
6. `internal/crypto/` — key management, EIP-712 signer, HMAC
7. `internal/store/postgres/` — client, migrations, market/order/position stores
8. `internal/cache/redis/` — client, price cache, orderbook cache, rate limiter
9. `docker-compose.yml` — local Supabase + Redis + MinIO
10. `internal/app/` — lifecycle wiring, errgroup, graceful shutdown

### Phase 2 — Core Trading
11. `internal/platform/polymarket/` — CLOB client, Gamma client, WebSocket
12. `internal/service/auth_service.go` — L1→L2 credential derivation
13. `internal/service/price_service.go` — WS → Redis cache hydration (protobuf pub/sub)
14. `internal/service/order_service.go` — signal → signed order → CLOB → Supabase
15. `internal/service/position_service.go` — position tracking, TP/SL, PnL
16. `internal/strategy/` — multi-strategy engine, price tracker, flash crash + mean reversion strategies
17. `internal/executor/` — order executor goroutine with dedup + retry
18. `cmd/polybot/main.go` — CLI entry point, `trade` mode

### Phase 3 — API Server & dashboard
19. `internal/blob/s3/` — S3 client, writer, reader
20. `internal/pipeline/` — market scraper, Goldsky scraper, trade processor
21. `internal/server/` — headless HTTP API, middleware (auth, rate limit), handlers
22. `internal/server/ws/` — WebSocket hub (Redis pub/sub → protobuf frames to clients)
23. `cmd/polyapp/` — Web dashboard (React + Vite) for monitoring and control
24. `internal/notify/` — Telegram + Discord notifications

### Phase 4 — Cross-Platform Arbitrage
29. `internal/platform/kalshi/` — REST + WebSocket client
30. `internal/service/arb_service.go` — event matching + net-edge model (fees/slippage/latency)
31. `internal/strategy/arb_strategy.go` — cross-platform arb strategy + two-leg execution policy
32. `internal/service/risk_service.go` — unhedged exposure caps, kill-switch, session drawdown guard
33. (TUI removed; dashboard uses `/api/arbitrage/*` for arb data)
34. `internal/cache/redis/signal_bus.go` — Redis streams for arb signals (protobuf encoded)

### Phase 5 — Multi-Outcome Foundation (ConditionGroups)
35. `internal/domain/condition_group.go` — ConditionGroup type wrapping []Market
36. `internal/domain/multi_leg.go` — LegPolicy type for multi-leg coordination
37. `migrations/008_condition_groups.sql` — condition_groups + junction table
38. `internal/store/postgres/condition_group_store.go` — Postgres ConditionGroupStore
39. `internal/cache/redis/condition_group_cache.go` — Redis ConditionGroupCache
40. `internal/platform/polymarket/types.go` — APIEvent DTO + ToDomainConditionGroup()
41. `internal/platform/polymarket/gamma.go` — GetEvents(), GetEvent(), GetActiveEvents()
42. `internal/app/wire.go` — wire ConditionGroupStore + ConditionGroupCache
43. `internal/app/modes.go` — event scraping goroutine in pipeline

### Phase 6 — Market Rebalancing Arbitrage
44. `internal/strategy/rebalancing_arb.go` — rebalancing arb strategy
45. `internal/executor/leg_group.go` — LegGroupAccumulator for multi-leg execution
46. `internal/executor/executor.go` — integrate LegGroupAccumulator routing
47. `internal/domain/signal.go` — ArbExecution + ArbLeg types for profit tracking
48. `internal/store/postgres/arb_execution_store.go` — ArbExecutionStore
49. Arb profit Redis counters + API endpoints
50. Unit tests: group price sum, signal generation, leg accumulation

### Phase 7 — High-Probability Bond Strategy
51. `internal/strategy/bond.go` — bond strategy
52. `internal/service/bond_tracker.go` — bond position lifecycle + resolution monitoring
53. `migrations/009_bond_positions.sql` — bond_positions table
54. `internal/store/postgres/bond_position_store.go` — BondPositionStore
55. (TUI removed; bond data available via API for dashboard)
56. Unit tests: APR calculation, candidate filtering, resolution handling

### Phase 8 — Liquidity Provision Strategy
57. `internal/strategy/liquidity_provider.go` — two-sided quoting strategy
58. `internal/service/rewards_tracker.go` — LP reward eligibility + accrual tracking
59. `internal/service/order_service.go` — add ReplaceOrder() for atomic requoting
60. Unit tests: quote placement, requoting threshold, spread management

### Phase 9 — Combinatorial Arbitrage
61. `internal/domain/market_relation.go` — MarketRelation type
62. `migrations/010_market_relations.sql` — market_relations table
63. `internal/store/postgres/relation_store.go` — MarketRelationStore
64. `internal/service/relation_service.go` — relation discovery + implied price computation
65. `internal/strategy/combinatorial_arb.go` — combinatorial arb strategy
66. `internal/app/wire.go` — wire MarketRelationStore
67. Unit tests: implied price calculation, deviation detection, multi-leg signals

### Phase 10 — Production
68. `internal/pipeline/archiver.go` — Supabase → S3 cold storage
69. Health monitoring goroutine
70. Comprehensive test suite (unit + integration + race) for all strategies
71. Backtest mode (S3 data replay through multi-strategy engine)
72. Hot-reload of strategy configs (Supabase → Redis → strategy)
73. Strategy/mode shown in web dashboard via WebSocket status
74. Metrics export (Prometheus-compatible via `expvar` or dedicated exporter)

---

## 21. New File Summary (Phases 5–9)

| File | Phase | Description |
|------|-------|-------------|
| `internal/domain/condition_group.go` | 5 | ConditionGroup wrapping []Market, PriceSum helper |
| `internal/domain/multi_leg.go` | 5 | LegPolicy type (all_or_none, best_effort, sequential) |
| `internal/domain/market_relation.go` | 9 | MarketRelation + RelationType for combinatorial arb |
| `internal/store/postgres/condition_group_store.go` | 5 | Postgres ConditionGroupStore impl |
| `internal/store/postgres/relation_store.go` | 9 | Postgres MarketRelationStore impl |
| `internal/store/postgres/bond_position_store.go` | 7 | Postgres BondPositionStore impl |
| `internal/store/postgres/arb_execution_store.go` | 6 | Postgres ArbExecutionStore impl |
| `migrations/008_condition_groups.sql` | 5 | condition_groups + junction table |
| `migrations/009_bond_positions.sql` | 7 | bond_positions table |
| `migrations/010_market_relations.sql` | 9 | market_relations table |
| `internal/strategy/rebalancing_arb.go` | 6 | Market rebalancing arbitrage |
| `internal/strategy/bond.go` | 7 | High-probability bond strategy |
| `internal/strategy/liquidity_provider.go` | 8 | Two-sided LP quoting |
| `internal/strategy/combinatorial_arb.go` | 9 | Cross-event combinatorial arb |
| `internal/executor/leg_group.go` | 6 | LegGroupAccumulator for multi-leg |
| `internal/service/bond_tracker.go` | 7 | Bond position lifecycle service |
| `internal/service/rewards_tracker.go` | 8 | LP reward eligibility tracking |
| `internal/service/relation_service.go` | 9 | Relation discovery + implied prices |
| (TUI removed) | — | Bond data via API/dashboard |

## 22. Modified Files (Phases 5–9)

| File | Phases | Changes |
|------|--------|---------|
| `internal/domain/store.go` | 5, 6, 7, 9 | Add ConditionGroupStore, ArbExecutionStore, BondPositionStore, MarketRelationStore |
| `internal/domain/cache.go` | 5 | Add ConditionGroupCache |
| `internal/domain/signal.go` | 6 | Add ArbExecution, ArbLeg, ArbType, ArbExecStatus types |
| `internal/platform/polymarket/gamma.go` | 5 | Add GetEvents/GetEvent/GetActiveEvents |
| `internal/platform/polymarket/types.go` | 5 | Add APIEvent DTO + ToDomainConditionGroup converter |
| `internal/executor/executor.go` | 6 | Integrate LegGroupAccumulator for multi-leg routing |
| `internal/service/order_service.go` | 8 | Add ReplaceOrder for LP requoting |
| `internal/strategy/engine.go` | 5 | RunAll() for concurrent multi-strategy execution |
| `internal/strategy/registry.go` | 5 | ListInfo() with per-strategy status/stats |
| `internal/app/wire.go` | 5, 9 | Wire new stores + caches |
| `internal/app/modes.go` | 5–9 | Event scraping goroutine, multi-strategy registry, register all new strategies |
| `config.toml` | 5–9 | Per-strategy config sections, strategy.active list |

## 23. Risk Mitigations

| Risk | Mitigation |
|------|-----------|
| Rebalancing arb partial fill (naked exposure) | FOK order type per leg, `maxLegGapMs` timeout, auto-cancel unfilled legs |
| Stale prices causing false arb signals | Require all leg prices refreshed within `max_stale_sec` window before acting |
| Bond strategy: market resolves unexpectedly NO | Conservative filters (min volume, min probability), position diversification, max_positions cap |
| LP adverse selection | Tight requoting on price moves, cancel-and-replace, total LP exposure cap, widen spread in volatility |
| Combinatorial arb model risk | Wider min_edge_bps (100 vs 50), confidence scoring on auto-discovered relations |
| Multi-strategy signal flood | Per-strategy rate limiting, shared executor channel backpressure (buffered channel), dedup across strategies |
| Market struct backward compatibility | ConditionGroup wraps existing Market; no changes to `[2]string` arrays |
