package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// USDC asset ID used to identify the collateral side of a fill. When the maker
// asset is USDC, the maker is buying tokens (the taker is selling). When the
// taker asset is USDC, the taker is buying tokens (the maker is selling).
const usdcAssetID = "0"

// TradeIngester persists enriched trades and provides timestamp tracking.
type TradeIngester interface {
	IngestTrades(ctx context.Context, trades []domain.Trade) error
	GetLastTimestamp(ctx context.Context) (time.Time, error)
}

// MarketLookup resolves a market from a token ID.
type MarketLookup interface {
	GetMarketByToken(ctx context.Context, tokenID string) (domain.Market, error)
}

// TradeProcessor converts raw fills into enriched trades and stores them.
type TradeProcessor struct {
	tradeSvc  TradeIngester
	marketSvc MarketLookup
	logger    *slog.Logger
}

// NewTradeProcessor creates a new TradeProcessor.
func NewTradeProcessor(tradeSvc TradeIngester, marketSvc MarketLookup, logger *slog.Logger) *TradeProcessor {
	return &TradeProcessor{
		tradeSvc:  tradeSvc,
		marketSvc: marketSvc,
		logger:    logger,
	}
}

// ProcessFills converts raw fills into domain.Trade structs and batch-inserts
// them. For each fill it looks up the associated market by token ID and enriches
// the trade with market metadata and direction information.
//
// It returns the number of trades successfully processed.
func (p *TradeProcessor) ProcessFills(ctx context.Context, fills []domain.RawFill) (int, error) {
	if len(fills) == 0 {
		return 0, nil
	}

	trades := make([]domain.Trade, 0, len(fills))

	for i, fill := range fills {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("trade processor context cancelled at fill %d: %w", i, err)
		}

		// Determine which asset ID is the token (non-USDC) to look up the market.
		tokenID := fill.MakerAssetID
		if tokenID == usdcAssetID {
			tokenID = fill.TakerAssetID
		}

		market, err := p.marketSvc.GetMarketByToken(ctx, tokenID)
		if err != nil {
			p.logger.Warn("skipping fill: market lookup failed",
				slog.String("token_id", tokenID),
				slog.String("tx_hash", fill.TransactionHash),
				slog.String("error", err.Error()),
			)
			continue
		}

		// Determine token side (token1 or token2).
		tokenSide := "token1"
		if tokenID == market.TokenIDs[1] {
			tokenSide = "token2"
		}

		// Determine maker/taker directions based on which side holds USDC.
		// If the maker's asset is USDC, the maker is buying tokens and the
		// taker is selling. Otherwise, the maker is selling and the taker is
		// buying.
		makerDir := "sell"
		takerDir := "buy"
		if fill.MakerAssetID == usdcAssetID {
			makerDir = "buy"
			takerDir = "sell"
		}

		// Calculate price: USDC amount / token amount.
		var usdAmount, tokenAmount float64
		if fill.MakerAssetID == usdcAssetID {
			usdAmount = float64(fill.MakerAmountFilled)
			tokenAmount = float64(fill.TakerAmountFilled)
		} else {
			usdAmount = float64(fill.TakerAmountFilled)
			tokenAmount = float64(fill.MakerAmountFilled)
		}

		var price float64
		if tokenAmount > 0 {
			price = usdAmount / tokenAmount
		}

		trade := domain.Trade{
			Source:         "goldsky",
			SourceTradeID:  fill.TransactionHash,
			Timestamp:      time.Unix(fill.Timestamp, 0),
			MarketID:       market.ID,
			Maker:          fill.Maker,
			Taker:          fill.Taker,
			TokenSide:      tokenSide,
			MakerDirection: makerDir,
			TakerDirection: takerDir,
			Price:          price,
			USDAmount:      usdAmount,
			TokenAmount:    tokenAmount,
			TxHash:         fill.TransactionHash,
		}

		trades = append(trades, trade)
	}

	if len(trades) == 0 {
		p.logger.Info("no trades to ingest after processing fills")
		return 0, nil
	}

	if err := p.tradeSvc.IngestTrades(ctx, trades); err != nil {
		return 0, fmt.Errorf("ingesting %d trades: %w", len(trades), err)
	}

	p.logger.Info("trades processed and ingested",
		slog.Int("fills_input", len(fills)),
		slog.Int("trades_ingested", len(trades)),
	)

	return len(trades), nil
}
