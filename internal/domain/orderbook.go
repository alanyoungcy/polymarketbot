package domain

import "time"

// PriceLevel is a single price+size entry in an orderbook.
type PriceLevel struct {
	Price float64
	Size  float64
}

// OrderbookSnapshot is a full snapshot of bids and asks for an asset.
type OrderbookSnapshot struct {
	AssetID   string
	Bids      []PriceLevel
	Asks      []PriceLevel
	BestBid   float64
	BestAsk   float64
	MidPrice  float64
	Timestamp time.Time
}

// PriceChange is an incremental orderbook level update.
type PriceChange struct {
	AssetID   string
	Side      string // "BUY" or "SELL"
	Price     float64
	Size      float64 // 0 means remove level
	Timestamp time.Time
}

// LastTradePrice is the most recent trade execution for an asset.
type LastTradePrice struct {
	AssetID   string
	Price     float64
	Size      float64
	Timestamp time.Time
}

// PriceSnapshot bundles current price data for strategy evaluation.
type PriceSnapshot struct {
	AssetID  string
	BestBid  float64
	BestAsk  float64
	MidPrice float64
	Spread   float64
	Time     time.Time
}
