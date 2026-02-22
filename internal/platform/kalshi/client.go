package kalshi

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is the REST client for the Kalshi exchange API.
type Client struct {
	baseURL    string
	apiKeyID   string
	privateKey *rsa.PrivateKey
	httpClient *http.Client
}

// NewClient creates a new Kalshi REST client.
//
// baseURL is the API root, e.g. "https://api.elections.kalshi.com/trade-api/v2".
// apiKeyID is the Kalshi API key identifier.
func NewClient(baseURL, apiKeyID string) *Client {
	return &Client{
		baseURL:  baseURL,
		apiKeyID: apiKeyID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetRSAPrivateKey loads an RSA private key from PEM-encoded bytes and
// configures the client for RSA-signed authentication.
func (c *Client) SetRSAPrivateKey(pemBytes []byte) error {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return fmt.Errorf("kalshi: no PEM block found in private key")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 as fallback.
		pkcs1Key, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if pkcs1Err != nil {
			return fmt.Errorf("kalshi: parse private key: %w (pkcs1: %v)", err, pkcs1Err)
		}
		c.privateKey = pkcs1Key
		return nil
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("kalshi: expected RSA private key, got %T", key)
	}
	c.privateKey = rsaKey
	return nil
}

// GetMarkets returns a paginated list of Kalshi markets.
func (c *Client) GetMarkets(ctx context.Context, limit, cursor string) ([]KalshiMarket, error) {
	params := url.Values{}
	if limit != "" {
		params.Set("limit", limit)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	path := "/markets"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	body, err := c.doSignedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("kalshi: get markets: %w", err)
	}

	var resp struct {
		Markets []KalshiMarket `json:"markets"`
		Cursor  string         `json:"cursor"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("kalshi: decode markets: %w", err)
	}

	return resp.Markets, nil
}

// GetMarket returns a single market by its ticker.
func (c *Client) GetMarket(ctx context.Context, ticker string) (KalshiMarket, error) {
	path := fmt.Sprintf("/markets/%s", url.PathEscape(ticker))

	body, err := c.doSignedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return KalshiMarket{}, fmt.Errorf("kalshi: get market %s: %w", ticker, err)
	}

	var resp struct {
		Market KalshiMarket `json:"market"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return KalshiMarket{}, fmt.Errorf("kalshi: decode market: %w", err)
	}

	return resp.Market, nil
}

// GetOrderbook returns the current orderbook for the given market ticker.
func (c *Client) GetOrderbook(ctx context.Context, ticker string) (KalshiOrderbook, error) {
	path := fmt.Sprintf("/markets/%s/orderbook", url.PathEscape(ticker))

	body, err := c.doSignedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return KalshiOrderbook{}, fmt.Errorf("kalshi: get orderbook %s: %w", ticker, err)
	}

	var resp struct {
		Orderbook KalshiOrderbook `json:"orderbook"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return KalshiOrderbook{}, fmt.Errorf("kalshi: decode orderbook: %w", err)
	}

	resp.Orderbook.Ticker = ticker
	resp.Orderbook.Timestamp = time.Now()

	return resp.Orderbook, nil
}

// PlaceOrder submits a new order on the Kalshi exchange.
func (c *Client) PlaceOrder(ctx context.Context, order KalshiOrder) error {
	body, err := c.doSignedRequest(ctx, http.MethodPost, "/portfolio/orders", order)
	if err != nil {
		return fmt.Errorf("kalshi: place order: %w", err)
	}

	var resp KalshiOrderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("kalshi: decode order response: %w", err)
	}

	if resp.Order.Status == "canceled" {
		return fmt.Errorf("kalshi: order was immediately cancelled")
	}

	return nil
}

// CancelOrder cancels an existing order by its ID.
func (c *Client) CancelOrder(ctx context.Context, orderID string) error {
	path := fmt.Sprintf("/portfolio/orders/%s", url.PathEscape(orderID))

	_, err := c.doSignedRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return fmt.Errorf("kalshi: cancel order %s: %w", orderID, err)
	}

	return nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// doSignedRequest builds, signs (RSA), sends, and reads an HTTP request
// against the Kalshi API.
func (c *Client) doSignedRequest(ctx context.Context, method, path string, reqBody any) ([]byte, error) {
	var bodyReader io.Reader
	if reqBody != nil {
		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	fullURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	// Sign the request with RSA.
	if err := c.signRequest(req, method, path); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if err := c.checkStatus(resp.StatusCode, respBody); err != nil {
		return nil, err
	}

	return respBody, nil
}

// signRequest adds RSA authentication headers to the HTTP request.
// Kalshi uses RSA-PSS-SHA256 signatures over the timestamp + method + path
// message string.
func (c *Client) signRequest(req *http.Request, method, path string) error {
	if c.privateKey == nil {
		// If no RSA key is set, we cannot sign. This is a configuration error.
		return fmt.Errorf("kalshi: RSA private key not configured")
	}

	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// The message to sign is: timestamp + method + path
	message := ts + method + path

	hash := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPSS(rand.Reader, c.privateKey, crypto.SHA256, hash[:], &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
	})
	if err != nil {
		return fmt.Errorf("RSA sign: %w", err)
	}

	encodedSig := base64.StdEncoding.EncodeToString(signature)

	req.Header.Set("KALSHI-ACCESS-KEY", c.apiKeyID)
	req.Header.Set("KALSHI-ACCESS-SIGNATURE", encodedSig)
	req.Header.Set("KALSHI-ACCESS-TIMESTAMP", ts)

	return nil
}

// checkStatus maps non-2xx HTTP status codes to appropriate errors.
func (c *Client) checkStatus(statusCode int, body []byte) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}

	var apiErr KalshiErrorResponse
	_ = json.Unmarshal(body, &apiErr)

	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("kalshi: not found: %s (%s)", apiErr.Message, apiErr.Code)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("kalshi: unauthorized: %s (%s)", apiErr.Message, apiErr.Code)
	case http.StatusTooManyRequests:
		return fmt.Errorf("kalshi: rate limited: %s (%s)", apiErr.Message, apiErr.Code)
	case http.StatusBadRequest:
		return fmt.Errorf("kalshi: bad request: %s (%s)", apiErr.Message, apiErr.Code)
	case http.StatusConflict:
		return fmt.Errorf("kalshi: conflict: %s (%s)", apiErr.Message, apiErr.Code)
	default:
		return fmt.Errorf("kalshi: HTTP %d: %s (%s)", statusCode, apiErr.Message, apiErr.Code)
	}
}
