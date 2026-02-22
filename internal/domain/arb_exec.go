package domain

import "time"

// ArbType classifies the kind of arbitrage execution.
type ArbType string

const (
	ArbTypeRebalancing   ArbType = "rebalancing"
	ArbTypeCombinatorial ArbType = "combinatorial"
	ArbTypeCrossPlatform ArbType = "cross_platform"
)

// ArbExecStatus is the execution state.
type ArbExecStatus string

const (
	ArbExecPending   ArbExecStatus = "pending"
	ArbExecPartial   ArbExecStatus = "partial"
	ArbExecFilled    ArbExecStatus = "filled"
	ArbExecCancelled ArbExecStatus = "cancelled"
	ArbExecFailed    ArbExecStatus = "failed"
)

// ArbExecution records one multi-leg arbitrage execution and its PnL.
type ArbExecution struct {
	ID            string
	OpportunityID string
	ArbType       ArbType
	LegGroupID    string
	Legs          []ArbLeg
	GrossEdgeBps  float64
	TotalFees     float64
	TotalSlippage float64
	NetPnLUSD     float64
	Status        ArbExecStatus
	StartedAt     time.Time
	CompletedAt   *time.Time
}

// ArbLeg is one leg of an arb execution.
type ArbLeg struct {
	OrderID       string
	MarketID      string
	TokenID       string
	Side          OrderSide
	ExpectedPrice float64
	FilledPrice   float64
	Size          float64
	FeeUSD        float64
	SlippageBps   float64
	Status        OrderStatus
}
