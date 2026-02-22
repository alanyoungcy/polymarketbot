package domain

import (
	"context"
	"time"
)

// ListOpts provides pagination and filtering for list queries.
type ListOpts struct {
	Limit  int
	Offset int
	Since  *time.Time
	Until  *time.Time
}

// MarketStore persists market metadata.
type MarketStore interface {
	Upsert(ctx context.Context, market Market) error
	UpsertBatch(ctx context.Context, markets []Market) error
	GetByID(ctx context.Context, id string) (Market, error)
	GetByTokenID(ctx context.Context, tokenID string) (Market, error)
	GetBySlug(ctx context.Context, slug string) (Market, error)
	ListActive(ctx context.Context, opts ListOpts) ([]Market, error)
	Count(ctx context.Context) (int64, error)
}

// OrderStore persists trading orders.
type OrderStore interface {
	Create(ctx context.Context, order Order) error
	UpdateStatus(ctx context.Context, id string, status OrderStatus) error
	GetByID(ctx context.Context, id string) (Order, error)
	ListOpen(ctx context.Context, wallet string) ([]Order, error)
	ListByMarket(ctx context.Context, marketID string, opts ListOpts) ([]Order, error)
}

// PositionStore persists positions.
type PositionStore interface {
	Create(ctx context.Context, pos Position) error
	Update(ctx context.Context, pos Position) error
	Close(ctx context.Context, id string, exitPrice float64) error
	GetOpen(ctx context.Context, wallet string) ([]Position, error)
	GetByID(ctx context.Context, id string) (Position, error)
	ListHistory(ctx context.Context, wallet string, opts ListOpts) ([]Position, error)
}

// TradeStore persists enriched trade fills.
type TradeStore interface {
	InsertBatch(ctx context.Context, trades []Trade) error
	GetLastTimestamp(ctx context.Context) (time.Time, error)
	ListByMarket(ctx context.Context, marketID string, opts ListOpts) ([]Trade, error)
	ListByWallet(ctx context.Context, wallet string, opts ListOpts) ([]Trade, error)
}

// ArbStore persists arbitrage opportunity history.
type ArbStore interface {
	Insert(ctx context.Context, opp ArbOpportunity) error
	MarkExecuted(ctx context.Context, id string) error
	ListRecent(ctx context.Context, limit int) ([]ArbOpportunity, error)
}

// AuditEntry is a single audit log row.
type AuditEntry struct {
	ID        int64
	Event     string
	Detail    map[string]any
	CreatedAt time.Time
}

// AuditStore persists an append-only audit log.
type AuditStore interface {
	Log(ctx context.Context, event string, detail map[string]any) error
	List(ctx context.Context, opts ListOpts) ([]AuditEntry, error)
}

// StrategyConfig is a named strategy configuration blob.
type StrategyConfig struct {
	Name      string
	Config    map[string]any
	Enabled   bool
	UpdatedAt time.Time
}

// StrategyConfigStore persists strategy configurations.
type StrategyConfigStore interface {
	Get(ctx context.Context, name string) (StrategyConfig, error)
	Upsert(ctx context.Context, cfg StrategyConfig) error
	List(ctx context.Context) ([]StrategyConfig, error)
}

// ConditionGroupStore persists condition groups and their market links.
type ConditionGroupStore interface {
	Upsert(ctx context.Context, g ConditionGroup) error
	GetByID(ctx context.Context, id string) (ConditionGroup, error)
	ListMarkets(ctx context.Context, groupID string) ([]string, error)
	LinkMarket(ctx context.Context, groupID, marketID string) error
	List(ctx context.Context) ([]ConditionGroup, error)
}

// BondPositionStore persists bond strategy positions.
type BondPositionStore interface {
	Create(ctx context.Context, pos BondPosition) error
	Update(ctx context.Context, pos BondPosition) error
	GetOpen(ctx context.Context) ([]BondPosition, error)
	GetByID(ctx context.Context, id string) (BondPosition, error)
}

// MarketRelationStore persists relations between condition groups.
type MarketRelationStore interface {
	Create(ctx context.Context, r MarketRelation) error
	GetByID(ctx context.Context, id string) (MarketRelation, error)
	ListBySource(ctx context.Context, sourceGroupID string) ([]MarketRelation, error)
	ListByTarget(ctx context.Context, targetGroupID string) ([]MarketRelation, error)
	List(ctx context.Context) ([]MarketRelation, error)
}

// ArbExecutionStore persists arb executions and legs for PnL tracking.
type ArbExecutionStore interface {
	Create(ctx context.Context, exec ArbExecution) error
	GetByID(ctx context.Context, id string) (ArbExecution, error)
	ListRecent(ctx context.Context, limit int) ([]ArbExecution, error)
	SumPnL(ctx context.Context, since time.Time) (float64, error)
	SumPnLByType(ctx context.Context, arbType ArbType, since time.Time) (float64, error)
}
