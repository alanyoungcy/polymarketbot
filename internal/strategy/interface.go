package strategy

import (
	"context"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Strategy defines the contract for trading strategies.
type Strategy interface {
	Name() string
	Init(ctx context.Context) error
	OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error)
	OnPriceChange(ctx context.Context, change domain.PriceChange) ([]domain.TradeSignal, error)
	OnTrade(ctx context.Context, trade domain.Trade) ([]domain.TradeSignal, error)
	OnSignal(ctx context.Context, signal domain.TradeSignal) ([]domain.TradeSignal, error)
	Close() error
}

// Config holds strategy configuration.
type Config struct {
	Name         string
	Coin         string
	Size         float64
	PriceScale   int
	SizeScale    int
	MaxPositions int
	TakeProfit   float64
	StopLoss     float64
	Params       map[string]any
}
