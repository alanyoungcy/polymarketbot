package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// TradeStore implements domain.TradeStore using PostgreSQL.
type TradeStore struct {
	pool *pgxpool.Pool
}

// NewTradeStore creates a new TradeStore backed by the given connection pool.
func NewTradeStore(pool *pgxpool.Pool) *TradeStore {
	return &TradeStore{pool: pool}
}

const tradeSelectCols = `id, source, source_trade_id, source_log_idx, timestamp,
	market_id, maker, taker, token_side, maker_direction, taker_direction,
	price, usd_amount, token_amount, tx_hash`

func scanTradeRows(rows pgx.Rows) ([]domain.Trade, error) {
	var trades []domain.Trade
	for rows.Next() {
		var t domain.Trade
		if err := rows.Scan(
			&t.ID, &t.Source, &t.SourceTradeID, &t.SourceLogIdx,
			&t.Timestamp, &t.MarketID, &t.Maker, &t.Taker,
			&t.TokenSide, &t.MakerDirection, &t.TakerDirection,
			&t.Price, &t.USDAmount, &t.TokenAmount, &t.TxHash,
		); err != nil {
			return nil, err
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

// InsertBatch inserts multiple trades efficiently using pgx Batch.
// Duplicate trades (same source, source_trade_id, source_log_idx) are
// silently skipped via ON CONFLICT DO NOTHING.
func (s *TradeStore) InsertBatch(ctx context.Context, trades []domain.Trade) error {
	if len(trades) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	const query = `
		INSERT INTO trades (
			source, source_trade_id, source_log_idx, timestamp,
			market_id, maker, taker, token_side,
			maker_direction, taker_direction,
			price, usd_amount, token_amount, tx_hash
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10,
			$11, $12, $13, $14
		) ON CONFLICT (source, source_trade_id, COALESCE(source_log_idx, -1)) DO NOTHING`

	for _, t := range trades {
		batch.Queue(query,
			t.Source, t.SourceTradeID, t.SourceLogIdx,
			t.Timestamp, t.MarketID, t.Maker, t.Taker,
			t.TokenSide, t.MakerDirection, t.TakerDirection,
			t.Price, t.USDAmount, t.TokenAmount, t.TxHash,
		)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := range trades {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("postgres: insert trade batch item %d: %w", i, err)
		}
	}
	return nil
}

// GetLastTimestamp returns the most recent trade timestamp, or the zero time
// if no trades exist.
func (s *TradeStore) GetLastTimestamp(ctx context.Context) (time.Time, error) {
	var ts *time.Time
	err := s.pool.QueryRow(ctx,
		"SELECT MAX(timestamp) FROM trades").Scan(&ts)
	if err != nil {
		return time.Time{}, fmt.Errorf("postgres: get last trade timestamp: %w", err)
	}
	if ts == nil {
		return time.Time{}, nil
	}
	return *ts, nil
}

// ListByMarket returns trades for a given market with pagination and optional time filtering.
func (s *TradeStore) ListByMarket(ctx context.Context, marketID string, opts domain.ListOpts) ([]domain.Trade, error) {
	query := `SELECT ` + tradeSelectCols + ` FROM trades WHERE market_id = $1`
	args := []any{marketID}
	argIdx := 2

	if opts.Since != nil {
		query += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, *opts.Since)
		argIdx++
	}
	if opts.Until != nil {
		query += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, *opts.Until)
		argIdx++
	}

	query += " ORDER BY timestamp DESC"

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
		return nil, fmt.Errorf("postgres: list trades by market: %w", err)
	}
	defer rows.Close()

	trades, err := scanTradeRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan trades by market: %w", err)
	}
	return trades, nil
}

// ListByWallet returns trades where the wallet appears as maker or taker,
// with pagination and optional time filtering.
func (s *TradeStore) ListByWallet(ctx context.Context, wallet string, opts domain.ListOpts) ([]domain.Trade, error) {
	query := `SELECT ` + tradeSelectCols + ` FROM trades WHERE (maker = $1 OR taker = $1)`
	args := []any{wallet}
	argIdx := 2

	if opts.Since != nil {
		query += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, *opts.Since)
		argIdx++
	}
	if opts.Until != nil {
		query += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, *opts.Until)
		argIdx++
	}

	query += " ORDER BY timestamp DESC"

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
		return nil, fmt.Errorf("postgres: list trades by wallet: %w", err)
	}
	defer rows.Close()

	trades, err := scanTradeRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan trades by wallet: %w", err)
	}
	return trades, nil
}

// ListBefore returns all trades with timestamp strictly before the given time (for archiving).
func (s *TradeStore) ListBefore(ctx context.Context, before time.Time) ([]domain.Trade, error) {
	query := `SELECT ` + tradeSelectCols + ` FROM trades WHERE timestamp < $1 ORDER BY timestamp ASC`
	rows, err := s.pool.Query(ctx, query, before)
	if err != nil {
		return nil, fmt.Errorf("postgres: list trades before: %w", err)
	}
	defer rows.Close()
	return scanTradeRows(rows)
}

// DeleteBefore deletes all trades with timestamp before the given time. Returns the number deleted.
func (s *TradeStore) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM trades WHERE timestamp < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("postgres: delete trades before: %w", err)
	}
	return tag.RowsAffected(), nil
}
