package goldsky

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Client is a GraphQL client for the Goldsky subgraph indexer, used to
// query on-chain order fill events from the Polymarket CTF Exchange contract.
type Client struct {
	graphqlURL string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Goldsky GraphQL client.
//
// graphqlURL is the Goldsky subgraph endpoint, e.g.
// "https://api.goldsky.com/api/public/.../subgraphs/polymarket-orderbook-resync/gn".
func NewClient(graphqlURL, apiKey string) *Client {
	return &Client{
		graphqlURL: graphqlURL,
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// graphqlRequest is the standard GraphQL request envelope.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the standard GraphQL response envelope.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// FetchOrderFills queries on-chain order fill events from the Goldsky
// subgraph. It returns fills that occurred at or after the given timestamp,
// limited by the 'first' parameter.
func (c *Client) FetchOrderFills(ctx context.Context, since time.Time, first int) ([]domain.RawFill, error) {
	sinceUnix := since.Unix()

	query := `
		query OrderFills($since: BigInt!, $first: Int!) {
			orderFilledEvents(
				first: $first
				orderBy: timestamp
				orderDirection: asc
				where: { timestamp_gte: $since }
			) {
				transactionHash
				timestamp
				maker
				makerAssetId
				makerAmountFilled
				taker
				takerAssetId
				takerAmountFilled
			}
		}
	`

	variables := map[string]any{
		"since": fmt.Sprintf("%d", sinceUnix),
		"first": first,
	}

	respData, err := c.doQuery(ctx, query, variables)
	if err != nil {
		return nil, fmt.Errorf("goldsky: fetch order fills: %w", err)
	}

	var result struct {
		OrderFilledEvents []struct {
			TransactionHash   string `json:"transactionHash"`
			Timestamp         string `json:"timestamp"`
			Maker             string `json:"maker"`
			MakerAssetID      string `json:"makerAssetId"`
			MakerAmountFilled string `json:"makerAmountFilled"`
			Taker             string `json:"taker"`
			TakerAssetID      string `json:"takerAssetId"`
			TakerAmountFilled string `json:"takerAmountFilled"`
		} `json:"orderFilledEvents"`
	}

	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("goldsky: decode order fills: %w", err)
	}

	fills := make([]domain.RawFill, 0, len(result.OrderFilledEvents))
	for _, e := range result.OrderFilledEvents {
		var ts int64
		fmt.Sscanf(e.Timestamp, "%d", &ts)

		var makerAmt, takerAmt int64
		fmt.Sscanf(e.MakerAmountFilled, "%d", &makerAmt)
		fmt.Sscanf(e.TakerAmountFilled, "%d", &takerAmt)

		fills = append(fills, domain.RawFill{
			TransactionHash:   e.TransactionHash,
			Timestamp:         ts,
			Maker:             e.Maker,
			MakerAssetID:      e.MakerAssetID,
			MakerAmountFilled: makerAmt,
			Taker:             e.Taker,
			TakerAssetID:      e.TakerAssetID,
			TakerAmountFilled: takerAmt,
		})
	}

	return fills, nil
}

// FetchLatestBlock returns the latest block number indexed by the Goldsky
// subgraph. This is useful for monitoring indexing lag.
func (c *Client) FetchLatestBlock(ctx context.Context) (int64, error) {
	query := `
		query LatestBlock {
			_meta {
				block {
					number
				}
			}
		}
	`

	respData, err := c.doQuery(ctx, query, nil)
	if err != nil {
		return 0, fmt.Errorf("goldsky: fetch latest block: %w", err)
	}

	var result struct {
		Meta struct {
			Block struct {
				Number int64 `json:"number"`
			} `json:"block"`
		} `json:"_meta"`
	}

	if err := json.Unmarshal(respData, &result); err != nil {
		return 0, fmt.Errorf("goldsky: decode latest block: %w", err)
	}

	return result.Meta.Block.Number, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// doQuery executes a GraphQL query against the Goldsky endpoint and returns
// the raw "data" field from the response.
func (c *Client) doQuery(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	reqBody := graphqlRequest{
		Query:     query,
		Variables: variables,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}
