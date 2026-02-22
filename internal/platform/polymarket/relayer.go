package polymarket

import (
	"context"
	"fmt"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// RelayerClient provides gasless order submission by wrapping the CLOB
// client with the correct headers and configuration for relayed execution.
// When using the relayer, the Polymarket infrastructure submits the
// on-chain transaction on the user's behalf, so the user does not need
// to hold MATIC for gas.
type RelayerClient struct {
	clobClient *ClobClient
}

// NewRelayerClient creates a RelayerClient that wraps the given ClobClient.
func NewRelayerClient(clobClient *ClobClient) *RelayerClient {
	return &RelayerClient{
		clobClient: clobClient,
	}
}

// SubmitGasless submits an order via the gasless relayer path. It uses the
// CLOB client's PostOrder under the hood, but ensures the order is
// structured for gasless (relayed) execution by setting the appropriate
// order type and configuration.
//
// The gasless flow works by submitting the signed order to the CLOB API,
// which routes it through the Polymarket relayer for on-chain settlement
// without requiring the maker to pay gas fees.
func (r *RelayerClient) SubmitGasless(ctx context.Context, order domain.Order) (domain.OrderResult, error) {
	// Validate the order has the required fields for gasless submission.
	if order.Signature == "" {
		return domain.OrderResult{}, fmt.Errorf("polymarket/relayer: %w: order must be signed", domain.ErrInvalidOrder)
	}
	if order.TokenID == "" {
		return domain.OrderResult{}, fmt.Errorf("polymarket/relayer: %w: tokenID required", domain.ErrInvalidOrder)
	}
	if order.MakerAmount == nil || order.TakerAmount == nil {
		return domain.OrderResult{}, fmt.Errorf("polymarket/relayer: %w: maker/taker amounts required", domain.ErrInvalidOrder)
	}
	if order.Wallet == "" {
		return domain.OrderResult{}, fmt.Errorf("polymarket/relayer: %w: wallet address required", domain.ErrInvalidOrder)
	}

	// For gasless execution, GTC is the standard order type as it stays
	// on the book until filled or cancelled by the relayer infrastructure.
	if order.Type == "" {
		order.Type = domain.OrderTypeGTC
	}

	result, err := r.clobClient.PostOrder(ctx, order)
	if err != nil {
		return domain.OrderResult{}, fmt.Errorf("polymarket/relayer: gasless submit: %w", err)
	}

	return result, nil
}
