package domain

import "time"

// SignalUrgency indicates how quickly a signal should be acted upon.
type SignalUrgency int

const (
	SignalUrgencyLow       SignalUrgency = iota
	SignalUrgencyMedium
	SignalUrgencyHigh
	SignalUrgencyImmediate
)

// TradeSignal is emitted by a strategy to request order execution.
type TradeSignal struct {
	ID         string // UUID for dedup
	Source     string // strategy name or "arb_detector"
	MarketID   string
	TokenID    string
	Side       OrderSide
	PriceTicks int64             // fixed-point price, 1e6 ticks
	SizeUnits  int64             // fixed-point size, 1e6 units
	Urgency    SignalUrgency
	Reason     string
	Metadata   map[string]string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// Price returns the display price from fixed-point ticks.
func (s TradeSignal) Price() float64 {
	return float64(s.PriceTicks) / 1e6
}

// Size returns the display size from fixed-point units.
func (s TradeSignal) Size() float64 {
	return float64(s.SizeUnits) / 1e6
}

// ArbOpportunity represents a detected cross-platform arbitrage.
type ArbOpportunity struct {
	ID              string
	PolyMarketID    string
	PolyTokenID     string
	PolyPrice       float64
	KalshiMarketID  string
	KalshiPrice     float64
	GrossEdgeBps    float64
	Direction       string // "poly_yes_kalshi_no" or "poly_no_kalshi_yes"
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

// BotStatus is a summary of the bot's current operational state.
type BotStatus struct {
	Mode          string
	WSConnected   bool
	UptimeSeconds int64
	OpenPositions int32
	OpenOrders    int32
	StrategyName  string
}
