package polymarket

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// flexBool unmarshals from JSON bool or string ("true"/"false") so Gamma API
// responses work whether "active" is sent as bool or string.
type flexBool bool

func (f *flexBool) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*f = flexBool(b)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = flexBool(strings.EqualFold(s, "true") || s == "1")
	return nil
}

// --------------------------------------------------------------------------
// CLOB API DTOs
// --------------------------------------------------------------------------

// APIOrder represents an order as returned by the Polymarket CLOB API.
type APIOrder struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	MarketID        string  `json:"market"`
	AssetID         string  `json:"asset_id"`
	Side            string  `json:"side"`    // "BUY" or "SELL"
	Type            string  `json:"type"`    // "GTC", "GTD", "FOK", "FAK"
	OriginalSize    string  `json:"original_size"`
	SizeMatched     string  `json:"size_matched"`
	Price           string  `json:"price"`
	MakerAmount     string  `json:"maker_amount"`
	TakerAmount     string  `json:"taker_amount"`
	Owner           string  `json:"owner"`
	Signature       string  `json:"signature"`
	Expiration      string  `json:"expiration"`
	Nonce           string  `json:"nonce"`
	FeeRateBps      string  `json:"fee_rate_bps"`
	SignatureType   int     `json:"signature_type"`
	AssociateTradeS []any   `json:"associate_trades"`
	CreatedAt       string  `json:"created_at"`
	FilledAt        *string `json:"filled_at,omitempty"`
	CancelledAt     *string `json:"cancelled_at,omitempty"`
}

// APIOrderResult is the response from placing an order via the CLOB API.
type APIOrderResult struct {
	Success     bool   `json:"success"`
	ErrorMsg    string `json:"errorMsg,omitempty"`
	OrderID     string `json:"orderID,omitempty"`
	Status      string `json:"status,omitempty"`
	TransactID  string `json:"transactID,omitempty"`
	ShouldRetry bool   `json:"shouldRetry,omitempty"`
}

// --------------------------------------------------------------------------
// Gamma API DTOs
// --------------------------------------------------------------------------

// APIEvent represents an event as returned by the Polymarket Gamma API.
// An event groups one or more related markets.
type APIEvent struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Slug        string      `json:"slug"`
	Description string      `json:"description"`
	Active      flexBool    `json:"active"`
	Closed      bool        `json:"closed"`
	Markets     []APIMarket `json:"markets"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
}

// ToDomainConditionGroup converts an APIEvent to a domain.ConditionGroup.
func (e *APIEvent) ToDomainConditionGroup() domain.ConditionGroup {
	cg := domain.ConditionGroup{
		ID:    e.ID,
		Title: e.Title,
	}
	if e.Closed {
		cg.Status = "closed"
	} else if bool(e.Active) {
		cg.Status = "active"
	} else {
		cg.Status = "settled"
	}
	if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
		cg.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, e.UpdatedAt); err == nil {
		cg.UpdatedAt = t
	}
	return cg
}

// APIMarket represents a market as returned by the Polymarket Gamma API.
type APIMarket struct {
	ID                     string  `json:"id"`
	Question               string  `json:"question"`
	ConditionID            string  `json:"condition_id"`
	Slug                   string   `json:"slug"`
	ActiveFromAPI          flexBool `json:"active"` // API may send bool or "true"/"false" string
	Closed                 bool     `json:"closed"`
	Outcomes               string  `json:"outcomes"`               // JSON-encoded: e.g. "[\"Yes\",\"No\"]"
	OutcomePrices          string  `json:"outcomePrices"`          // JSON-encoded: e.g. "[\"0.5\",\"0.5\"]"
	Tokens                 []Token `json:"tokens"`
	Volume                 string  `json:"volume"`
	NegRisk                bool    `json:"neg_risk"`
	EndDateISO             string  `json:"end_date_iso"`
	GameStartTime          string  `json:"game_start_time"`
	HasReviewedDates       bool    `json:"has_reviewed_dates"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
	Description            string  `json:"description"`
	MarketMakerAddress     string  `json:"market_maker_address"`
	EnableOrderBook        bool    `json:"enable_order_book"`
	ClobTokenIDs           string  `json:"clob_token_ids"` // JSON-encoded: e.g. "[\"123\",\"456\"]"
	RewardsMinSize         float64 `json:"rewards_min_size"`
	RewardsMaxSpread       float64 `json:"rewards_max_spread"`
	SpreadBenefitBasisPts  float64 `json:"spread"`
	Active                 bool    `json:"is_active"`
}

// Token represents a token entry inside the Gamma API market response.
type Token struct {
	TokenID  string `json:"token_id"`
	Outcome  string `json:"outcome"`
	Winner   bool   `json:"winner"`
}

// --------------------------------------------------------------------------
// WebSocket DTOs
// --------------------------------------------------------------------------

// WSMessage is the outer envelope of every WebSocket frame from the
// Polymarket CLOB WebSocket API.
type WSMessage struct {
	MsgType   string `json:"msg_type"` // "book", "price_change", "last_trade_price", "error"
	AssetID   string `json:"asset_id,omitempty"`
	Market    string `json:"market,omitempty"`
	EventType string `json:"event_type,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`

	// For "book" messages
	Book *BookMessage `json:"-"`
	// For "price_change" messages
	PriceChange *PriceChangeMessage `json:"-"`
	// For "last_trade_price" messages
	LastTradePrice *PriceMessage `json:"-"`
}

// BookMessage represents a full orderbook snapshot delivered over WebSocket.
type BookMessage struct {
	AssetID   string          `json:"asset_id"`
	Market    string          `json:"market"`
	Bids      []WSPriceLevel  `json:"bids"`
	Asks      []WSPriceLevel  `json:"asks"`
	Timestamp string          `json:"timestamp"`
	Hash      string          `json:"hash"`
}

// WSPriceLevel is a single bid/ask level in the WebSocket orderbook data.
type WSPriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// PriceChangeMessage represents an incremental orderbook price-level update.
type PriceChangeMessage struct {
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
	Side      string `json:"side"`  // "BUY" or "SELL"
	Price     string `json:"price"`
	Size      string `json:"size"` // "0" means level removed
	Timestamp string `json:"timestamp"`
}

// PriceMessage represents the most recent trade price for an asset.
type PriceMessage struct {
	AssetID   string `json:"asset_id"`
	Market    string `json:"market"`
	Price     string `json:"price"`
	Size      string `json:"size"`
	Timestamp string `json:"timestamp"`
}

// --------------------------------------------------------------------------
// WebSocket subscription commands
// --------------------------------------------------------------------------

// WSCommand is the JSON payload sent to the WebSocket to subscribe/unsubscribe.
type WSCommand struct {
	Type     string   `json:"type"`                // "subscribe" or "unsubscribe"
	Channel  string   `json:"channel,omitempty"`
	Assets   []string `json:"assets_ids,omitempty"`
	Markets  []string `json:"markets,omitempty"`
}

// --------------------------------------------------------------------------
// Conversion helpers: API types -> domain types
// --------------------------------------------------------------------------

// ToDomainOrder converts an APIOrder to a domain.Order.
func (a *APIOrder) ToDomainOrder() domain.Order {
	o := domain.Order{
		ID:        a.ID,
		MarketID:  a.MarketID,
		TokenID:   a.AssetID,
		Wallet:    a.Owner,
		Signature: a.Signature,
	}

	// Side
	switch a.Side {
	case "BUY":
		o.Side = domain.OrderSideBuy
	case "SELL":
		o.Side = domain.OrderSideSell
	}

	// Type
	switch a.Type {
	case "GTC":
		o.Type = domain.OrderTypeGTC
	case "GTD":
		o.Type = domain.OrderTypeGTD
	case "FOK":
		o.Type = domain.OrderTypeFOK
	case "FAK":
		o.Type = domain.OrderTypeFAK
	}

	// Status
	switch a.Status {
	case "live", "open":
		o.Status = domain.OrderStatusOpen
	case "matched", "filled":
		o.Status = domain.OrderStatusMatched
	case "cancelled":
		o.Status = domain.OrderStatusCancelled
	default:
		o.Status = domain.OrderStatusPending
	}

	// Price -> PriceTicks (fixed-point * 1e6)
	if price, err := strconv.ParseFloat(a.Price, 64); err == nil {
		o.PriceTicks = int64(price * 1e6)
	}

	// Sizes
	if orig, err := strconv.ParseFloat(a.OriginalSize, 64); err == nil {
		o.SizeUnits = int64(orig * 1e6)
	}
	if matched, err := strconv.ParseFloat(a.SizeMatched, 64); err == nil {
		o.FilledSize = matched
	}

	// MakerAmount/TakerAmount as big.Int
	if ma, ok := new(big.Int).SetString(a.MakerAmount, 10); ok {
		o.MakerAmount = ma
	}
	if ta, ok := new(big.Int).SetString(a.TakerAmount, 10); ok {
		o.TakerAmount = ta
	}

	// Timestamps
	if t, err := time.Parse(time.RFC3339, a.CreatedAt); err == nil {
		o.CreatedAt = t
	}
	if a.FilledAt != nil {
		if t, err := time.Parse(time.RFC3339, *a.FilledAt); err == nil {
			o.FilledAt = &t
		}
	}
	if a.CancelledAt != nil {
		if t, err := time.Parse(time.RFC3339, *a.CancelledAt); err == nil {
			o.CancelledAt = &t
		}
	}

	return o
}

// ToDomainOrderResult converts an APIOrderResult to a domain.OrderResult.
func (r *APIOrderResult) ToDomainOrderResult() domain.OrderResult {
	result := domain.OrderResult{
		Success:     r.Success,
		OrderID:     r.OrderID,
		Message:     r.ErrorMsg,
		ShouldRetry: r.ShouldRetry,
	}

	switch r.Status {
	case "live", "open":
		result.Status = domain.OrderStatusOpen
	case "matched":
		result.Status = domain.OrderStatusMatched
	case "delayed":
		result.Status = domain.OrderStatusPending
	default:
		if r.Success {
			result.Status = domain.OrderStatusPending
		} else {
			result.Status = domain.OrderStatusFailed
		}
	}

	return result
}

// ToDomainMarket converts a Gamma APIMarket to a domain.Market. Safe for event-scraper
// upserts: defaults Question to "Unknown" and Outcomes to "Yes"/"No" when missing so
// markets(id) exists before linking in condition_group_markets.
func (m *APIMarket) ToDomainMarket() domain.Market {
	dm := domain.Market{
		ID:          m.ID,
		Question:    m.Question,
		Slug:        m.Slug,
		ConditionID: m.ConditionID,
		NegRisk:     m.NegRisk,
		Outcomes:    [2]string{"Yes", "No"},
	}
	if dm.Question == "" {
		dm.Question = "Unknown"
	}

	// Parse volume
	if v, err := strconv.ParseFloat(m.Volume, 64); err == nil {
		dm.Volume = v
	}

	// Status (support both Active and ActiveFromAPI from Gamma)
	if m.Closed {
		dm.Status = domain.MarketStatusClosed
	} else if m.Active || bool(m.ActiveFromAPI) {
		dm.Status = domain.MarketStatusActive
	} else {
		dm.Status = domain.MarketStatusSettled
	}

	// Tokens: extract up to 2 token IDs and outcomes
	for i, tok := range m.Tokens {
		if i >= 2 {
			break
		}
		dm.TokenIDs[i] = tok.TokenID
		if tok.Outcome != "" {
			dm.Outcomes[i] = tok.Outcome
		}
	}

	// Timestamps
	if t, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
		dm.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, m.UpdatedAt); err == nil {
		dm.UpdatedAt = t
	}
	if m.EndDateISO != "" {
		if t, err := time.Parse(time.RFC3339, m.EndDateISO); err == nil {
			dm.ClosedAt = &t
		}
	}

	return dm
}

// BookToDomainSnapshot converts a BookMessage to a domain.OrderbookSnapshot.
func BookToDomainSnapshot(b *BookMessage) domain.OrderbookSnapshot {
	snap := domain.OrderbookSnapshot{
		AssetID: b.AssetID,
	}

	for _, lvl := range b.Bids {
		p, _ := strconv.ParseFloat(lvl.Price, 64)
		s, _ := strconv.ParseFloat(lvl.Size, 64)
		snap.Bids = append(snap.Bids, domain.PriceLevel{Price: p, Size: s})
		if p > snap.BestBid {
			snap.BestBid = p
		}
	}
	for _, lvl := range b.Asks {
		p, _ := strconv.ParseFloat(lvl.Price, 64)
		s, _ := strconv.ParseFloat(lvl.Size, 64)
		snap.Asks = append(snap.Asks, domain.PriceLevel{Price: p, Size: s})
		if snap.BestAsk == 0 || p < snap.BestAsk {
			snap.BestAsk = p
		}
	}

	if snap.BestBid > 0 && snap.BestAsk > 0 {
		snap.MidPrice = (snap.BestBid + snap.BestAsk) / 2
	}

	if ts, err := strconv.ParseInt(b.Timestamp, 10, 64); err == nil {
		snap.Timestamp = time.Unix(ts, 0)
	} else if t, err := time.Parse(time.RFC3339, b.Timestamp); err == nil {
		snap.Timestamp = t
	} else {
		snap.Timestamp = time.Now()
	}

	return snap
}

// PriceChangeToDomain converts a PriceChangeMessage to a domain.PriceChange.
func PriceChangeToDomain(p *PriceChangeMessage) domain.PriceChange {
	pc := domain.PriceChange{
		AssetID: p.AssetID,
		Side:    p.Side,
	}
	pc.Price, _ = strconv.ParseFloat(p.Price, 64)
	pc.Size, _ = strconv.ParseFloat(p.Size, 64)

	if ts, err := strconv.ParseInt(p.Timestamp, 10, 64); err == nil {
		pc.Timestamp = time.Unix(ts, 0)
	} else {
		pc.Timestamp = time.Now()
	}

	return pc
}

// PriceToDomainLastTrade converts a PriceMessage to a domain.LastTradePrice.
func PriceToDomainLastTrade(p *PriceMessage) domain.LastTradePrice {
	ltp := domain.LastTradePrice{
		AssetID: p.AssetID,
	}
	ltp.Price, _ = strconv.ParseFloat(p.Price, 64)
	ltp.Size, _ = strconv.ParseFloat(p.Size, 64)

	if ts, err := strconv.ParseInt(p.Timestamp, 10, 64); err == nil {
		ltp.Timestamp = time.Unix(ts, 0)
	} else {
		ltp.Timestamp = time.Now()
	}

	return ltp
}
