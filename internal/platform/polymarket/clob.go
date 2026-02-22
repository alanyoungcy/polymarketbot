package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/crypto"
	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ClobClient is the REST client for the Polymarket CLOB (Central Limit
// Order Book) API. It handles order placement, cancellation, and queries.
type ClobClient struct {
	baseURL    string
	httpClient *http.Client
	signer     *crypto.Signer
	hmacAuth   *crypto.HMACAuth
}

// NewClobClient creates a new CLOB REST client.
//
// baseURL is the CLOB API root, e.g. "https://clob.polymarket.com".
// signer is the EIP-712 signer for order signatures and auth messages.
// hmac is the HMAC authenticator for API requests (obtained after DeriveAPIKey).
func NewClobClient(baseURL string, signer *crypto.Signer, hmac *crypto.HMACAuth) *ClobClient {
	return &ClobClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		signer:   signer,
		hmacAuth: hmac,
	}
}

// PostOrder submits a signed order to the CLOB API and returns the result.
func (c *ClobClient) PostOrder(ctx context.Context, order domain.Order) (domain.OrderResult, error) {
	// Build the CLOB order payload.
	body := map[string]any{
		"order": map[string]any{
			"tokenID":       order.TokenID,
			"makerAmount":   order.MakerAmount.String(),
			"takerAmount":   order.TakerAmount.String(),
			"side":          string(order.Side),
			"feeRateBps":    "0",
			"nonce":         "0",
			"expiration":    "0",
			"signatureType": 0,
			"signature":     order.Signature,
			"maker":         order.Wallet,
			"signer":        order.Wallet,
			"taker":         "0x0000000000000000000000000000000000000000",
		},
		"owner":    order.Wallet,
		"orderType": string(order.Type),
	}

	respBody, err := c.doAuthenticatedRequest(ctx, http.MethodPost, "/order", body)
	if err != nil {
		return domain.OrderResult{}, fmt.Errorf("polymarket/clob: post order: %w", err)
	}

	var apiResult APIOrderResult
	if err := json.Unmarshal(respBody, &apiResult); err != nil {
		return domain.OrderResult{}, fmt.Errorf("polymarket/clob: decode order result: %w", err)
	}

	result := apiResult.ToDomainOrderResult()
	if !result.Success {
		return result, fmt.Errorf("polymarket/clob: order rejected: %s", result.Message)
	}

	return result, nil
}

// CancelOrder cancels a single order by its ID.
func (c *ClobClient) CancelOrder(ctx context.Context, orderID string) error {
	body := map[string]any{
		"orderID": orderID,
	}

	respBody, err := c.doAuthenticatedRequest(ctx, http.MethodDelete, "/order", body)
	if err != nil {
		return fmt.Errorf("polymarket/clob: cancel order %s: %w", orderID, err)
	}

	var result struct {
		Success  bool   `json:"success"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("polymarket/clob: decode cancel response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("polymarket/clob: cancel failed: %s", result.ErrorMsg)
	}

	return nil
}

// CancelAll cancels all open orders for the authenticated wallet.
func (c *ClobClient) CancelAll(ctx context.Context) error {
	respBody, err := c.doAuthenticatedRequest(ctx, http.MethodDelete, "/cancel-all", nil)
	if err != nil {
		return fmt.Errorf("polymarket/clob: cancel all: %w", err)
	}

	var result struct {
		Success  bool   `json:"success"`
		ErrorMsg string `json:"errorMsg"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("polymarket/clob: decode cancel-all response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("polymarket/clob: cancel-all failed: %s", result.ErrorMsg)
	}

	return nil
}

// GetOrder retrieves a single order by ID.
func (c *ClobClient) GetOrder(ctx context.Context, orderID string) (domain.Order, error) {
	path := fmt.Sprintf("/order/%s", orderID)

	respBody, err := c.doAuthenticatedRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return domain.Order{}, fmt.Errorf("polymarket/clob: get order %s: %w", orderID, err)
	}

	var apiOrder APIOrder
	if err := json.Unmarshal(respBody, &apiOrder); err != nil {
		return domain.Order{}, fmt.Errorf("polymarket/clob: decode order: %w", err)
	}

	return apiOrder.ToDomainOrder(), nil
}

// GetOpenOrders returns all open orders for the authenticated wallet.
func (c *ClobClient) GetOpenOrders(ctx context.Context) ([]domain.Order, error) {
	respBody, err := c.doAuthenticatedRequest(ctx, http.MethodGet, "/orders", nil)
	if err != nil {
		return nil, fmt.Errorf("polymarket/clob: get open orders: %w", err)
	}

	var apiOrders []APIOrder
	if err := json.Unmarshal(respBody, &apiOrders); err != nil {
		return nil, fmt.Errorf("polymarket/clob: decode orders: %w", err)
	}

	orders := make([]domain.Order, 0, len(apiOrders))
	for i := range apiOrders {
		orders = append(orders, apiOrders[i].ToDomainOrder())
	}

	return orders, nil
}

// DeriveAPIKey performs the CLOB auth flow to obtain an HMAC API key. It
// signs a ClobAuth EIP-712 message and sends it with L1 headers to the
// derive-api-key endpoint. Per Polymarket docs, L1 requires POLY_ADDRESS,
// POLY_SIGNATURE, POLY_TIMESTAMP, POLY_NONCE. On success it populates the
// client's hmacAuth field.
func (c *ClobClient) DeriveAPIKey(ctx context.Context) error {
	address := c.signer.Address().Hex()
	timestamp := time.Now().Unix()
	nonce := int64(0)

	sig, err := c.signer.SignAuthMessage(address, timestamp, nonce)
	if err != nil {
		return fmt.Errorf("polymarket/clob: sign auth message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth/derive-api-key", nil)
	if err != nil {
		return fmt.Errorf("polymarket/clob: create auth request: %w", err)
	}
	req.Header.Set("POLY_ADDRESS", address)
	req.Header.Set("POLY_SIGNATURE", sig)
	req.Header.Set("POLY_TIMESTAMP", fmt.Sprintf("%d", timestamp))
	req.Header.Set("POLY_NONCE", fmt.Sprintf("%d", nonce))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("polymarket/clob: auth request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("polymarket/clob: read auth response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("polymarket/clob: auth failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var authResp struct {
		APIKey     string `json:"apiKey"`
		Secret     string `json:"secret"`
		Passphrase string `json:"passphrase"`
	}
	if err := json.Unmarshal(respBody, &authResp); err != nil {
		return fmt.Errorf("polymarket/clob: decode auth response: %w", err)
	}

	c.hmacAuth = &crypto.HMACAuth{
		Key:        authResp.APIKey,
		Secret:     authResp.Secret,
		Passphrase: authResp.Passphrase,
	}

	return nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// doAuthenticatedRequest builds, signs (HMAC), sends, and reads an HTTP
// request against the CLOB API. It returns the raw response body.
func (c *ClobClient) doAuthenticatedRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	var bodyStr string

	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyStr = string(jsonBody)
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Apply HMAC authentication headers.
	if c.hmacAuth != nil {
		address := c.signer.Address().Hex()
		headers := c.hmacAuth.L2Headers(address, method, path, bodyStr)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
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

	if err := checkHTTPStatus(resp.StatusCode, respBody); err != nil {
		return nil, err
	}

	return respBody, nil
}

// checkHTTPStatus maps non-2xx status codes to appropriate domain errors.
func checkHTTPStatus(statusCode int, body []byte) error {
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}

	bodyStr := string(body)
	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", domain.ErrNotFound, bodyStr)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", domain.ErrUnauthorized, bodyStr)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", domain.ErrRateLimited, bodyStr)
	default:
		return fmt.Errorf("HTTP %d: %s", statusCode, bodyStr)
	}
}
