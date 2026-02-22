package domain

import "time"

// MarketStatus represents the lifecycle state of a market.
type MarketStatus string

const (
	MarketStatusActive  MarketStatus = "active"
	MarketStatusClosed  MarketStatus = "closed"
	MarketStatusSettled MarketStatus = "settled"
)

// Market represents a Polymarket prediction market.
type Market struct {
	ID          string
	Question    string
	Slug        string
	Outcomes    [2]string    // e.g. ["Yes","No"] or ["Up","Down"]
	TokenIDs    [2]string    // ERC-1155 token IDs (76-digit strings)
	ConditionID string
	NegRisk     bool
	Volume      float64
	Status      MarketStatus
	ClosedAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
