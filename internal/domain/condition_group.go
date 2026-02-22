package domain

import "time"

// ConditionGroup wraps N binary markets that share one event (e.g. multi-outcome).
type ConditionGroup struct {
	ID        string
	Title     string
	Status    string // active, closed, settled
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConditionGroupMarket is the junction row linking a group to a market.
type ConditionGroupMarket struct {
	GroupID  string
	MarketID string
}
