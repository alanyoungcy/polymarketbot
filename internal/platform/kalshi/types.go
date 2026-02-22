package kalshi

import (
	"encoding/json"
	"time"
)

// --------------------------------------------------------------------------
// Kalshi API DTOs
// --------------------------------------------------------------------------

// KalshiMarket represents a market as returned by the Kalshi REST API.
type KalshiMarket struct {
	Ticker           string  `json:"ticker"`
	EventTicker      string  `json:"event_ticker"`
	Title            string  `json:"title"`
	Subtitle         string  `json:"subtitle"`
	Status           string  `json:"status"` // "open", "closed", "settled"
	YesBid           float64 `json:"yes_bid"`
	YesAsk           float64 `json:"yes_ask"`
	NoBid            float64 `json:"no_bid"`
	NoAsk            float64 `json:"no_ask"`
	LastPrice        float64 `json:"last_price"`
	Volume           int64   `json:"volume"`
	Volume24H        int64   `json:"volume_24h"`
	OpenInterest     int64   `json:"open_interest"`
	PreviousYesBid   float64 `json:"previous_yes_bid"`
	PreviousYesAsk   float64 `json:"previous_yes_ask"`
	PreviousPrice    float64 `json:"previous_price"`
	ExpirationTime   string  `json:"expiration_time"`
	Category         string  `json:"category"`
	RiskLimitCents   int64   `json:"risk_limit_cents"`
	StrikeType       string  `json:"strike_type"`
	FloorStrike      float64 `json:"floor_strike"`
	CapStrike        float64 `json:"cap_strike"`
	Result           string  `json:"result"` // "yes", "no", "" (unsettled)
	CanCloseEarly    bool    `json:"can_close_early"`
	ExpirationValue  string  `json:"expiration_value"`
	FunctionalStrike string  `json:"functional_strike"`
	OpenTime         string  `json:"open_time"`
	CloseTime        string  `json:"close_time"`
	SettlementTimer  int64   `json:"settlement_timer_seconds"`
}

// KalshiOrderbook represents the orderbook for a Kalshi market.
type KalshiOrderbook struct {
	Ticker    string             `json:"ticker"`
	YesBids  []KalshiPriceLevel `json:"yes"`
	NoBids   []KalshiPriceLevel `json:"no"`
	Timestamp time.Time         `json:"-"`
}

// KalshiPriceLevel is a single price+quantity entry in the Kalshi orderbook.
type KalshiPriceLevel struct {
	Price    int64 `json:"price"`    // in cents (1-99)
	Quantity int64 `json:"quantity"` // number of contracts
}

// KalshiOrder represents an order to be placed on the Kalshi exchange.
type KalshiOrder struct {
	Ticker      string `json:"ticker"`
	Action      string `json:"action"`       // "buy" or "sell"
	Side        string `json:"side"`         // "yes" or "no"
	Type        string `json:"type"`         // "market" or "limit"
	Count       int64  `json:"count"`        // number of contracts
	YesPrice    *int64 `json:"yes_price,omitempty"`    // limit price in cents (1-99), required for limit orders
	NoPrice     *int64 `json:"no_price,omitempty"`     // limit price in cents (1-99)
	Expiration  *int64 `json:"expiration_ts,omitempty"` // Unix timestamp for GTD orders
	SellPositionFloor *int64 `json:"sell_position_floor,omitempty"`
	BuyMaxCost  *int64 `json:"buy_max_cost,omitempty"` // max cost in cents
}

// KalshiOrderResponse represents the API response after placing an order.
type KalshiOrderResponse struct {
	Order struct {
		OrderID         string `json:"order_id"`
		UserID          string `json:"user_id"`
		Ticker          string `json:"ticker"`
		Status          string `json:"status"` // "resting", "canceled", "executed", "pending"
		Action          string `json:"action"`
		Side            string `json:"side"`
		Type            string `json:"type"`
		YesPrice        int64  `json:"yes_price"`
		NoPrice         int64  `json:"no_price"`
		ExpirationTime  string `json:"expiration_time"`
		PlacedTime      string `json:"placed_time"`
		RemainingCount  int64  `json:"remaining_count"`
		TakerFillCount  int64  `json:"taker_fill_count"`
		TakerFillCost   int64  `json:"taker_fill_cost"`
		MakerFillCount  int64  `json:"maker_fill_count"`
		QueuePosition   int64  `json:"queue_position"`
		LastUpdateTime  string `json:"last_update_time"`
	} `json:"order"`
}

// KalshiErrorResponse represents a Kalshi API error response.
type KalshiErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --------------------------------------------------------------------------
// Kalshi WebSocket DTOs
// --------------------------------------------------------------------------

// KalshiWSMessage is the envelope for Kalshi WebSocket messages.
type KalshiWSMessage struct {
	Type string          `json:"type"` // "orderbook_snapshot", "orderbook_delta", "trade", etc.
	Msg  json.RawMessage `json:"msg"`
	SID  int64           `json:"sid"`
}

// KalshiWSOrderbook is the orderbook data received via WebSocket.
type KalshiWSOrderbook struct {
	Ticker string             `json:"market_ticker"`
	Yes    []KalshiPriceLevel `json:"yes"`
	No     []KalshiPriceLevel `json:"no"`
}

// KalshiWSSubscribeCmd is the command sent to subscribe to Kalshi WebSocket channels.
type KalshiWSSubscribeCmd struct {
	ID      int64                  `json:"id"`
	Cmd     string                 `json:"cmd"` // "subscribe" or "unsubscribe"
	Params  KalshiWSSubscribeParams `json:"params"`
}

// KalshiWSSubscribeParams defines the subscription parameters.
type KalshiWSSubscribeParams struct {
	Channels []string `json:"channels"` // e.g. ["orderbook_delta", "ticker"]
	Tickers  []string `json:"market_tickers"`
}

// --------------------------------------------------------------------------
// Conversion helpers
// --------------------------------------------------------------------------

// ToWSOrderbook converts a KalshiWSOrderbook to a KalshiOrderbook.
func (w *KalshiWSOrderbook) ToOrderbook() KalshiOrderbook {
	return KalshiOrderbook{
		Ticker:    w.Ticker,
		YesBids:  w.Yes,
		NoBids:   w.No,
		Timestamp: time.Now(),
	}
}
