package domain

import (
	"math/big"
	"time"
)

// OrderSide indicates whether this is a buy or sell.
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderType indicates the time-in-force policy.
type OrderType string

const (
	OrderTypeGTC OrderType = "GTC" // Good-Till-Cancelled
	OrderTypeGTD OrderType = "GTD" // Good-Till-Date
	OrderTypeFOK OrderType = "FOK" // Fill-Or-Kill
	OrderTypeFAK OrderType = "FAK" // Fill-And-Kill
)

// OrderStatus tracks the order lifecycle.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusOpen      OrderStatus = "open"
	OrderStatusMatched   OrderStatus = "matched"
	OrderStatusCancelled OrderStatus = "cancelled"
	OrderStatusFailed    OrderStatus = "failed"
)

// Order represents a signed trading order.
type Order struct {
	ID          string
	MarketID    string
	TokenID     string
	Wallet      string
	Side        OrderSide
	Type        OrderType
	PriceTicks  int64    // fixed-point: price * 1e6
	SizeUnits   int64    // fixed-point: size  * 1e6
	MakerAmount *big.Int // integer notional used in signed payload
	TakerAmount *big.Int // integer quantity used in signed payload
	FilledSize  float64
	Status      OrderStatus
	Signature   string // EIP-712 hex
	Strategy    string
	CreatedAt   time.Time
	FilledAt    *time.Time
	CancelledAt *time.Time
}

// Price returns the float64 display price from fixed-point ticks.
func (o Order) Price() float64 {
	return float64(o.PriceTicks) / 1e6
}

// Size returns the float64 display size from fixed-point units.
func (o Order) Size() float64 {
	return float64(o.SizeUnits) / 1e6
}

// OrderResult wraps the API response after order submission.
type OrderResult struct {
	Success     bool
	OrderID     string
	Status      OrderStatus
	Message     string
	ShouldRetry bool
	FilledPrice float64 // filled price when matched
	FeeUSD      float64 // fee for this order
}
