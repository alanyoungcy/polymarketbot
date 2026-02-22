package redis

import (
	"context"
	_ "embed"
	"fmt"
	"strconv"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/redis/go-redis/v9"
)

//go:embed scripts/orderbook_update.lua
var orderbookUpdateLua string

// OrderbookCache implements domain.OrderbookCache using Redis sorted sets and
// hashes for each asset's orderbook.
//
// Key schema:
//
//	book:{assetID}:bids   - sorted set of bid prices (score = price)
//	book:{assetID}:asks   - sorted set of ask prices (score = price)
//	book:{assetID}:bid:size - hash mapping price -> size for bids
//	book:{assetID}:ask:size - hash mapping price -> size for asks
//	book:{assetID}:bbo    - hash with fields "bid" and "ask" (best prices)
//	book:{assetID}:meta   - hash with "ts" field (snapshot timestamp)
type OrderbookCache struct {
	rdb              *redis.Client
	orderbookUpdate  *redis.Script
}

// NewOrderbookCache creates an OrderbookCache backed by the given Client.
func NewOrderbookCache(c *Client) *OrderbookCache {
	return &OrderbookCache{
		rdb:             c.Underlying(),
		orderbookUpdate: redis.NewScript(orderbookUpdateLua),
	}
}

func bookBidsKey(assetID string) string    { return "book:" + assetID + ":bids" }
func bookAsksKey(assetID string) string    { return "book:" + assetID + ":asks" }
func bookBidSizeKey(assetID string) string { return "book:" + assetID + ":bid:size" }
func bookAskSizeKey(assetID string) string { return "book:" + assetID + ":ask:size" }
func bookBBOKey(assetID string) string     { return "book:" + assetID + ":bbo" }
func bookMetaKey(assetID string) string    { return "book:" + assetID + ":meta" }

// SetSnapshot atomically replaces the entire orderbook snapshot for an asset.
// It clears existing data and repopulates all sorted sets, size hashes, the BBO
// hash, and the metadata hash.
func (oc *OrderbookCache) SetSnapshot(ctx context.Context, assetID string, snap domain.OrderbookSnapshot) error {
	bidsKey := bookBidsKey(assetID)
	asksKey := bookAsksKey(assetID)
	bidSizeKey := bookBidSizeKey(assetID)
	askSizeKey := bookAskSizeKey(assetID)
	bboKey := bookBBOKey(assetID)
	metaKey := bookMetaKey(assetID)

	pipe := oc.rdb.TxPipeline()

	// Clear existing keys.
	pipe.Del(ctx, bidsKey, asksKey, bidSizeKey, askSizeKey, bboKey, metaKey)

	// Populate bids.
	for _, lvl := range snap.Bids {
		priceStr := strconv.FormatFloat(lvl.Price, 'f', -1, 64)
		sizeStr := strconv.FormatFloat(lvl.Size, 'f', -1, 64)
		pipe.ZAdd(ctx, bidsKey, redis.Z{Score: lvl.Price, Member: priceStr})
		pipe.HSet(ctx, bidSizeKey, priceStr, sizeStr)
	}

	// Populate asks.
	for _, lvl := range snap.Asks {
		priceStr := strconv.FormatFloat(lvl.Price, 'f', -1, 64)
		sizeStr := strconv.FormatFloat(lvl.Size, 'f', -1, 64)
		pipe.ZAdd(ctx, asksKey, redis.Z{Score: lvl.Price, Member: priceStr})
		pipe.HSet(ctx, askSizeKey, priceStr, sizeStr)
	}

	// Set BBO.
	if snap.BestBid > 0 {
		pipe.HSet(ctx, bboKey, "bid", strconv.FormatFloat(snap.BestBid, 'f', -1, 64))
	}
	if snap.BestAsk > 0 {
		pipe.HSet(ctx, bboKey, "ask", strconv.FormatFloat(snap.BestAsk, 'f', -1, 64))
	}

	// Set metadata.
	pipe.HSet(ctx, metaKey, "ts", strconv.FormatInt(snap.Timestamp.UnixNano(), 10))

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: set orderbook snapshot %s: %w", assetID, err)
	}
	return nil
}

// GetSnapshot reconstructs a full OrderbookSnapshot from Redis.
// It returns domain.ErrNotFound if no snapshot data exists for the asset.
func (oc *OrderbookCache) GetSnapshot(ctx context.Context, assetID string) (domain.OrderbookSnapshot, error) {
	bidsKey := bookBidsKey(assetID)
	asksKey := bookAsksKey(assetID)
	bidSizeKey := bookBidSizeKey(assetID)
	askSizeKey := bookAskSizeKey(assetID)
	bboKey := bookBBOKey(assetID)
	metaKey := bookMetaKey(assetID)

	pipe := oc.rdb.Pipeline()

	// Read bids sorted descending (highest first).
	bidsCmd := pipe.ZRevRangeWithScores(ctx, bidsKey, 0, -1)
	// Read asks sorted ascending (lowest first).
	asksCmd := pipe.ZRangeWithScores(ctx, asksKey, 0, -1)
	// Read size hashes.
	bidSizeCmd := pipe.HGetAll(ctx, bidSizeKey)
	askSizeCmd := pipe.HGetAll(ctx, askSizeKey)
	// Read BBO.
	bboCmd := pipe.HGetAll(ctx, bboKey)
	// Read metadata.
	metaCmd := pipe.HGetAll(ctx, metaKey)

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return domain.OrderbookSnapshot{}, fmt.Errorf("redis: get orderbook snapshot %s: %w", assetID, err)
	}

	metaVals, _ := metaCmd.Result()
	if len(metaVals) == 0 {
		return domain.OrderbookSnapshot{}, domain.ErrNotFound
	}

	snap := domain.OrderbookSnapshot{
		AssetID: assetID,
	}

	// Parse timestamp.
	if tsStr, ok := metaVals["ts"]; ok {
		tsNano, err := strconv.ParseInt(tsStr, 10, 64)
		if err == nil {
			snap.Timestamp = time.Unix(0, tsNano)
		}
	}

	// Build bid levels.
	bidSizes, _ := bidSizeCmd.Result()
	bidsZ, _ := bidsCmd.Result()
	snap.Bids = make([]domain.PriceLevel, 0, len(bidsZ))
	for _, z := range bidsZ {
		priceStr, ok := z.Member.(string)
		if !ok {
			continue
		}
		size := 0.0
		if sizeStr, exists := bidSizes[priceStr]; exists {
			size, _ = strconv.ParseFloat(sizeStr, 64)
		}
		snap.Bids = append(snap.Bids, domain.PriceLevel{
			Price: z.Score,
			Size:  size,
		})
	}

	// Build ask levels.
	askSizes, _ := askSizeCmd.Result()
	asksZ, _ := asksCmd.Result()
	snap.Asks = make([]domain.PriceLevel, 0, len(asksZ))
	for _, z := range asksZ {
		priceStr, ok := z.Member.(string)
		if !ok {
			continue
		}
		size := 0.0
		if sizeStr, exists := askSizes[priceStr]; exists {
			size, _ = strconv.ParseFloat(sizeStr, 64)
		}
		snap.Asks = append(snap.Asks, domain.PriceLevel{
			Price: z.Score,
			Size:  size,
		})
	}

	// Parse BBO.
	bboVals, _ := bboCmd.Result()
	if bidStr, ok := bboVals["bid"]; ok {
		snap.BestBid, _ = strconv.ParseFloat(bidStr, 64)
	}
	if askStr, ok := bboVals["ask"]; ok {
		snap.BestAsk, _ = strconv.ParseFloat(askStr, 64)
	}
	if snap.BestBid > 0 && snap.BestAsk > 0 {
		snap.MidPrice = (snap.BestBid + snap.BestAsk) / 2
	}

	return snap, nil
}

// UpdateLevel applies an incremental orderbook level update using an atomic Lua
// script. If size > 0 the level is added/updated; if size == 0 the level is
// removed. The script recomputes the BBO after the update.
func (oc *OrderbookCache) UpdateLevel(ctx context.Context, assetID string, side string, price, size float64) error {
	var zKey, hKey string
	var sideArg string

	switch side {
	case "bids", "BUY":
		zKey = bookBidsKey(assetID)
		hKey = bookBidSizeKey(assetID)
		sideArg = "bids"
	case "asks", "SELL":
		zKey = bookAsksKey(assetID)
		hKey = bookAskSizeKey(assetID)
		sideArg = "asks"
	default:
		return fmt.Errorf("redis: update level: unknown side %q", side)
	}

	bboKey := bookBBOKey(assetID)
	priceStr := strconv.FormatFloat(price, 'f', -1, 64)
	sizeStr := strconv.FormatFloat(size, 'f', -1, 64)

	keys := []string{zKey, hKey, bboKey}
	args := []interface{}{priceStr, sizeStr, sideArg}

	if err := oc.orderbookUpdate.Run(ctx, oc.rdb, keys, args...).Err(); err != nil {
		return fmt.Errorf("redis: update level %s %s@%s: %w", assetID, sideArg, priceStr, err)
	}
	return nil
}

// GetBBO retrieves the current best bid and best ask from the BBO hash.
// It returns domain.ErrNotFound if no BBO data exists.
func (oc *OrderbookCache) GetBBO(ctx context.Context, assetID string) (bestBid, bestAsk float64, err error) {
	bboKey := bookBBOKey(assetID)
	vals, err := oc.rdb.HGetAll(ctx, bboKey).Result()
	if err != nil {
		return 0, 0, fmt.Errorf("redis: get bbo %s: %w", assetID, err)
	}
	if len(vals) == 0 {
		return 0, 0, domain.ErrNotFound
	}

	if bidStr, ok := vals["bid"]; ok {
		bestBid, _ = strconv.ParseFloat(bidStr, 64)
	}
	if askStr, ok := vals["ask"]; ok {
		bestAsk, _ = strconv.ParseFloat(askStr, 64)
	}
	return bestBid, bestAsk, nil
}

// Compile-time interface check.
var _ domain.OrderbookCache = (*OrderbookCache)(nil)
