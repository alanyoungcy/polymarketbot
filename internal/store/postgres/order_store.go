package postgres

import (
	"context"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// OrderStore implements domain.OrderStore using PostgreSQL.
type OrderStore struct {
	pool *pgxpool.Pool
}

// NewOrderStore creates a new OrderStore backed by the given connection pool.
func NewOrderStore(pool *pgxpool.Pool) *OrderStore {
	return &OrderStore{pool: pool}
}

// Create inserts a new order into the database.
func (s *OrderStore) Create(ctx context.Context, o domain.Order) error {
	var makerAmountStr, takerAmountStr *string
	if o.MakerAmount != nil {
		v := o.MakerAmount.String()
		makerAmountStr = &v
	}
	if o.TakerAmount != nil {
		v := o.TakerAmount.String()
		takerAmountStr = &v
	}

	const query = `
		INSERT INTO orders (
			id, market_id, token_id, wallet, side, order_type,
			price_ticks, size_units, maker_amount, taker_amount,
			price, size, filled_size, status, signature, strategy_name,
			created_at, filled_at, cancelled_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16,
			$17, $18, $19, NOW()
		)`

	_, err := s.pool.Exec(ctx, query,
		o.ID, o.MarketID, o.TokenID, o.Wallet,
		string(o.Side), string(o.Type),
		o.PriceTicks, o.SizeUnits,
		makerAmountStr, takerAmountStr,
		o.Price(), o.Size(), o.FilledSize,
		string(o.Status), o.Signature, o.Strategy,
		o.CreatedAt, o.FilledAt, o.CancelledAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: create order %s: %w", o.ID, err)
	}
	return nil
}

// UpdateStatus changes the status of an existing order and sets the
// corresponding timestamp field if applicable.
func (s *OrderStore) UpdateStatus(ctx context.Context, id string, status domain.OrderStatus) error {
	var query string
	switch status {
	case domain.OrderStatusMatched:
		query = `UPDATE orders SET status = $1, filled_at = NOW(), updated_at = NOW() WHERE id = $2`
	case domain.OrderStatusCancelled:
		query = `UPDATE orders SET status = $1, cancelled_at = NOW(), updated_at = NOW() WHERE id = $2`
	default:
		query = `UPDATE orders SET status = $1, updated_at = NOW() WHERE id = $2`
	}

	tag, err := s.pool.Exec(ctx, query, string(status), id)
	if err != nil {
		return fmt.Errorf("postgres: update order status %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// orderSelectCols lists the columns selected when reading orders.
// The price and size columns are derived (stored redundantly for queries)
// but we still need to scan them to satisfy the column list.
const orderSelectCols = `id, market_id, token_id, wallet, side, order_type,
	price_ticks, size_units, maker_amount, taker_amount,
	price, size, filled_size, status, signature, strategy_name,
	created_at, filled_at, cancelled_at`

func scanOrderFromRow(
	scanner interface{ Scan(dest ...any) error },
) (domain.Order, error) {
	var o domain.Order
	var side, orderType, status string
	// price and size are stored redundantly in the DB; we scan but ignore them
	// since domain.Order derives them from PriceTicks/SizeUnits.
	var dbPrice, dbSize float64
	var makerAmountStr, takerAmountStr *string

	err := scanner.Scan(
		&o.ID, &o.MarketID, &o.TokenID, &o.Wallet,
		&side, &orderType,
		&o.PriceTicks, &o.SizeUnits,
		&makerAmountStr, &takerAmountStr,
		&dbPrice, &dbSize,
		&o.FilledSize, &status, &o.Signature, &o.Strategy,
		&o.CreatedAt, &o.FilledAt, &o.CancelledAt,
	)
	if err != nil {
		return domain.Order{}, err
	}

	o.Side = domain.OrderSide(side)
	o.Type = domain.OrderType(orderType)
	o.Status = domain.OrderStatus(status)

	if makerAmountStr != nil {
		o.MakerAmount = new(big.Int)
		o.MakerAmount.SetString(*makerAmountStr, 10)
	}
	if takerAmountStr != nil {
		o.TakerAmount = new(big.Int)
		o.TakerAmount.SetString(*takerAmountStr, 10)
	}

	return o, nil
}

func scanOrderRows(rows pgx.Rows) ([]domain.Order, error) {
	var orders []domain.Order
	for rows.Next() {
		o, err := scanOrderFromRow(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// GetByID retrieves a single order by ID.
func (s *OrderStore) GetByID(ctx context.Context, id string) (domain.Order, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+orderSelectCols+` FROM orders WHERE id = $1`, id)

	o, err := scanOrderFromRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Order{}, domain.ErrNotFound
		}
		return domain.Order{}, fmt.Errorf("postgres: get order %s: %w", id, err)
	}
	return o, nil
}

// ListOpen returns all orders in open/pending status for the given wallet.
func (s *OrderStore) ListOpen(ctx context.Context, wallet string) ([]domain.Order, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+orderSelectCols+` FROM orders
		 WHERE wallet = $1 AND status IN ('pending', 'open')
		 ORDER BY created_at DESC`, wallet)
	if err != nil {
		return nil, fmt.Errorf("postgres: list open orders: %w", err)
	}
	defer rows.Close()

	orders, err := scanOrderRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan open orders: %w", err)
	}
	return orders, nil
}

// ListByMarket returns orders for a given market with pagination.
func (s *OrderStore) ListByMarket(ctx context.Context, marketID string, opts domain.ListOpts) ([]domain.Order, error) {
	query := `SELECT ` + orderSelectCols + ` FROM orders WHERE market_id = $1`
	args := []any{marketID}
	argIdx := 2

	if opts.Since != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, *opts.Since)
		argIdx++
	}
	if opts.Until != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argIdx)
		args = append(args, *opts.Until)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list orders by market: %w", err)
	}
	defer rows.Close()

	orders, err := scanOrderRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan orders by market: %w", err)
	}
	return orders, nil
}
