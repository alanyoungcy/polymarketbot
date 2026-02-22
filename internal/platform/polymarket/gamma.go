package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// GammaClient is the REST client for the Polymarket Gamma API, which
// provides market discovery, metadata, and search.
type GammaClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewGammaClient creates a new Gamma API client.
//
// baseURL is the Gamma API root, e.g. "https://gamma-api.polymarket.com".
func NewGammaClient(baseURL string) *GammaClient {
	return &GammaClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetMarkets returns a paginated list of markets.
func (g *GammaClient) GetMarkets(ctx context.Context, limit, offset int) ([]domain.Market, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))

	path := "/markets?" + params.Encode()

	body, err := g.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("polymarket/gamma: get markets: %w", err)
	}

	var apiMarkets []APIMarket
	if err := json.Unmarshal(body, &apiMarkets); err != nil {
		return nil, fmt.Errorf("polymarket/gamma: decode markets: %w", err)
	}

	markets := make([]domain.Market, 0, len(apiMarkets))
	for i := range apiMarkets {
		markets = append(markets, apiMarkets[i].ToDomainMarket())
	}

	return markets, nil
}

// GetMarket returns a single market by its ID.
func (g *GammaClient) GetMarket(ctx context.Context, id string) (domain.Market, error) {
	path := fmt.Sprintf("/markets/%s", url.PathEscape(id))

	body, err := g.doGet(ctx, path)
	if err != nil {
		return domain.Market{}, fmt.Errorf("polymarket/gamma: get market %s: %w", id, err)
	}

	var apiMarket APIMarket
	if err := json.Unmarshal(body, &apiMarket); err != nil {
		return domain.Market{}, fmt.Errorf("polymarket/gamma: decode market: %w", err)
	}

	return apiMarket.ToDomainMarket(), nil
}

// MarketResolution holds resolution state for a market (for bond tracking).
type MarketResolution struct {
	Closed   bool // market is closed/settled
	YesWon   bool // the Yes outcome won (only meaningful when Closed)
}

// GetMarketResolution fetches market by ID and returns whether it is closed and whether Yes won.
// Used by BondTracker to update bond positions on resolution.
func (g *GammaClient) GetMarketResolution(ctx context.Context, marketID string) (MarketResolution, error) {
	path := fmt.Sprintf("/markets/%s", url.PathEscape(marketID))
	body, err := g.doGet(ctx, path)
	if err != nil {
		return MarketResolution{}, fmt.Errorf("polymarket/gamma: get market %s: %w", marketID, err)
	}
	var apiMarket APIMarket
	if err := json.Unmarshal(body, &apiMarket); err != nil {
		return MarketResolution{}, fmt.Errorf("polymarket/gamma: decode market: %w", err)
	}
	res := MarketResolution{Closed: apiMarket.Closed}
	for _, t := range apiMarket.Tokens {
		if t.Outcome == "Yes" && t.Winner {
			res.YesWon = true
			break
		}
	}
	return res, nil
}

// GetMarketBySlug returns a single market looked up by its URL slug.
func (g *GammaClient) GetMarketBySlug(ctx context.Context, slug string) (domain.Market, error) {
	params := url.Values{}
	params.Set("slug", slug)

	path := "/markets?" + params.Encode()

	body, err := g.doGet(ctx, path)
	if err != nil {
		return domain.Market{}, fmt.Errorf("polymarket/gamma: get market by slug %s: %w", slug, err)
	}

	var apiMarkets []APIMarket
	if err := json.Unmarshal(body, &apiMarkets); err != nil {
		return domain.Market{}, fmt.Errorf("polymarket/gamma: decode markets: %w", err)
	}

	if len(apiMarkets) == 0 {
		return domain.Market{}, fmt.Errorf("polymarket/gamma: %w: slug=%s", domain.ErrNotFound, slug)
	}

	return apiMarkets[0].ToDomainMarket(), nil
}

// SearchMarkets searches for markets matching the given query string.
func (g *GammaClient) SearchMarkets(ctx context.Context, query string) ([]domain.Market, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", "50")

	path := "/markets?" + params.Encode()

	body, err := g.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("polymarket/gamma: search markets: %w", err)
	}

	var apiMarkets []APIMarket
	if err := json.Unmarshal(body, &apiMarkets); err != nil {
		return nil, fmt.Errorf("polymarket/gamma: decode search results: %w", err)
	}

	markets := make([]domain.Market, 0, len(apiMarkets))
	for i := range apiMarkets {
		markets = append(markets, apiMarkets[i].ToDomainMarket())
	}

	return markets, nil
}

// RewardEligibleMarket holds market ID and reward-related fields for LP strategy.
type RewardEligibleMarket struct {
	MarketID       string
	RewardsMinSize float64
	RewardsMaxSpread float64
	Volume         float64
}

// ListRewardEligibleMarkets returns markets that offer maker/LP rewards.
// minVolume filters out markets with volume below the given USD threshold.
func (g *GammaClient) ListRewardEligibleMarkets(ctx context.Context, minVolume float64, limit int) ([]RewardEligibleMarket, error) {
	if limit <= 0 {
		limit = 100
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", "0")
	path := "/markets?" + params.Encode()
	body, err := g.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("polymarket/gamma: list markets: %w", err)
	}
	var apiMarkets []APIMarket
	if err := json.Unmarshal(body, &apiMarkets); err != nil {
		return nil, fmt.Errorf("polymarket/gamma: decode markets: %w", err)
	}
	var out []RewardEligibleMarket
	for i := range apiMarkets {
		m := &apiMarkets[i]
		if m.RewardsMinSize <= 0 {
			continue
		}
		vol, _ := strconv.ParseFloat(m.Volume, 64)
		if vol < minVolume {
			continue
		}
		out = append(out, RewardEligibleMarket{
			MarketID:         m.ID,
			RewardsMinSize:   m.RewardsMinSize,
			RewardsMaxSpread: m.RewardsMaxSpread,
			Volume:           vol,
		})
	}
	return out, nil
}

// GetEvents returns a paginated list of events from the Gamma API.
func (g *GammaClient) GetEvents(ctx context.Context, limit, offset int) ([]APIEvent, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))

	path := "/events?" + params.Encode()

	body, err := g.doGet(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("polymarket/gamma: get events: %w", err)
	}

	var events []APIEvent
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("polymarket/gamma: decode events: %w", err)
	}

	return events, nil
}

// GetEvent returns a single event by its ID from the Gamma API.
func (g *GammaClient) GetEvent(ctx context.Context, id string) (APIEvent, error) {
	path := fmt.Sprintf("/events/%s", url.PathEscape(id))

	body, err := g.doGet(ctx, path)
	if err != nil {
		return APIEvent{}, fmt.Errorf("polymarket/gamma: get event %s: %w", id, err)
	}

	var event APIEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return APIEvent{}, fmt.Errorf("polymarket/gamma: decode event: %w", err)
	}

	return event, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// doGet sends an unauthenticated GET request to the Gamma API.
func (g *GammaClient) doGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if err := checkHTTPStatus(resp.StatusCode, body); err != nil {
		return nil, err
	}

	return body, nil
}
