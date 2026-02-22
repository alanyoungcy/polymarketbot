package domain

import "time"

// BondStatus is the state of a bond position.
type BondStatus string

const (
	BondOpen         BondStatus = "open"
	BondResolvedWin  BondStatus = "resolved_win"
	BondResolvedLoss BondStatus = "resolved_loss"
)

// BondPosition tracks a high-probability YES holding to resolution.
type BondPosition struct {
	ID             string
	MarketID       string
	TokenID        string
	EntryPrice     float64
	ExpectedExpiry time.Time
	ExpectedAPR    float64
	Size           float64
	Status         BondStatus
	RealizedPnL    float64
	CreatedAt      time.Time
	ResolvedAt     *time.Time
}
