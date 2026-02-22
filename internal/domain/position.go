package domain

import "time"

// PositionStatus tracks whether a position is open or closed.
type PositionStatus string

const (
	PositionStatusOpen   PositionStatus = "open"
	PositionStatusClosed PositionStatus = "closed"
)

// Position represents an open or historical trading position.
type Position struct {
	ID            string
	MarketID      string
	TokenID       string
	Wallet        string
	Side          string         // "token1" or "token2"
	Direction     OrderSide      // Buy or Sell inventory direction
	EntryPrice    float64
	CurrentPrice  float64
	Size          float64
	UnrealizedPnL float64
	RealizedPnL   float64
	TakeProfit    *float64
	StopLoss      *float64
	Status        PositionStatus
	Strategy      string
	OpenedAt      time.Time
	ClosedAt      *time.Time
	ExitPrice     *float64
}
