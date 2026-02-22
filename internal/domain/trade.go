package domain

import "time"

// Trade represents an enriched, processed trade fill.
type Trade struct {
	ID             int64
	Source         string // "polymarket", "kalshi", "goldsky"
	SourceTradeID  string
	SourceLogIdx   *int64
	Timestamp      time.Time
	MarketID       string
	Maker          string
	Taker          string
	TokenSide      string // "token1" or "token2"
	MakerDirection string // "buy" or "sell"
	TakerDirection string // "buy" or "sell"
	Price          float64
	USDAmount      float64
	TokenAmount    float64
	TxHash         string
}

// RawFill represents a raw on-chain order-filled event from Goldsky.
type RawFill struct {
	Timestamp         int64
	Maker             string
	MakerAssetID      string
	MakerAmountFilled int64
	Taker             string
	TakerAssetID      string
	TakerAmountFilled int64
	TransactionHash   string
}
