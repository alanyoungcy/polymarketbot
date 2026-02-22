package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// MarketStore implements domain.MarketStore using PostgreSQL.
type MarketStore struct {
	pool *pgxpool.Pool
}

// NewMarketStore creates a new MarketStore backed by the given connection pool.
func NewMarketStore(pool *pgxpool.Pool) *MarketStore {
	return &MarketStore{pool: pool}
}

// Upsert inserts or updates a single market.
func (s *MarketStore) Upsert(ctx context.Context, m domain.Market) error {
	const query = `
		INSERT INTO markets (
			id, question, slug, outcome_1, outcome_2,
			token_id_1, token_id_2, condition_id, neg_risk,
			volume, status, closed_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, NOW()
		)
		ON CONFLICT (id) DO UPDATE SET
			question     = EXCLUDED.question,
			slug         = EXCLUDED.slug,
			outcome_1    = EXCLUDED.outcome_1,
			outcome_2    = EXCLUDED.outcome_2,
			token_id_1   = EXCLUDED.token_id_1,
			token_id_2   = EXCLUDED.token_id_2,
			condition_id = EXCLUDED.condition_id,
			neg_risk     = EXCLUDED.neg_risk,
			volume       = EXCLUDED.volume,
			status       = EXCLUDED.status,
			closed_at    = EXCLUDED.closed_at,
			updated_at   = NOW()`

	_, err := s.pool.Exec(ctx, query,
		m.ID, m.Question, m.Slug,
		m.Outcomes[0], m.Outcomes[1],
		m.TokenIDs[0], m.TokenIDs[1],
		m.ConditionID, m.NegRisk,
		m.Volume, string(m.Status), m.ClosedAt, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: upsert market %s: %w", m.ID, err)
	}
	return nil
}

// UpsertBatch inserts or updates multiple markets in a single batch operation.
func (s *MarketStore) UpsertBatch(ctx context.Context, markets []domain.Market) error {
	if len(markets) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	const query = `
		INSERT INTO markets (
			id, question, slug, outcome_1, outcome_2,
			token_id_1, token_id_2, condition_id, neg_risk,
			volume, status, closed_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, NOW()
		)
		ON CONFLICT (id) DO UPDATE SET
			question     = EXCLUDED.question,
			slug         = EXCLUDED.slug,
			outcome_1    = EXCLUDED.outcome_1,
			outcome_2    = EXCLUDED.outcome_2,
			token_id_1   = EXCLUDED.token_id_1,
			token_id_2   = EXCLUDED.token_id_2,
			condition_id = EXCLUDED.condition_id,
			neg_risk     = EXCLUDED.neg_risk,
			volume       = EXCLUDED.volume,
			status       = EXCLUDED.status,
			closed_at    = EXCLUDED.closed_at,
			updated_at   = NOW()`

	for _, m := range markets {
		batch.Queue(query,
			m.ID, m.Question, m.Slug,
			m.Outcomes[0], m.Outcomes[1],
			m.TokenIDs[0], m.TokenIDs[1],
			m.ConditionID, m.NegRisk,
			m.Volume, string(m.Status), m.ClosedAt, m.CreatedAt,
		)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := range markets {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("postgres: upsert market batch item %d: %w", i, err)
		}
	}
	return nil
}

// scanMarket scans a single market row into a domain.Market.
func scanMarket(row pgx.Row) (domain.Market, error) {
	var m domain.Market
	var status string
	err := row.Scan(
		&m.ID, &m.Question, &m.Slug,
		&m.Outcomes[0], &m.Outcomes[1],
		&m.TokenIDs[0], &m.TokenIDs[1],
		&m.ConditionID, &m.NegRisk,
		&m.Volume, &status, &m.ClosedAt,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return domain.Market{}, err
	}
	m.Status = domain.MarketStatus(status)
	return m, nil
}

const marketCols = `id, question, slug, outcome_1, outcome_2,
	token_id_1, token_id_2, condition_id, neg_risk,
	volume, status, closed_at, created_at, updated_at`

// GetByID retrieves a market by its primary key.
func (s *MarketStore) GetByID(ctx context.Context, id string) (domain.Market, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+marketCols+` FROM markets WHERE id = $1`, id)
	m, err := scanMarket(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Market{}, domain.ErrNotFound
		}
		return domain.Market{}, fmt.Errorf("postgres: get market %s: %w", id, err)
	}
	return m, nil
}

// GetByTokenID retrieves a market by either token ID.
func (s *MarketStore) GetByTokenID(ctx context.Context, tokenID string) (domain.Market, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+marketCols+` FROM markets WHERE token_id_1 = $1 OR token_id_2 = $1`, tokenID)
	m, err := scanMarket(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Market{}, domain.ErrNotFound
		}
		return domain.Market{}, fmt.Errorf("postgres: get market by token %s: %w", tokenID, err)
	}
	return m, nil
}

// GetBySlug retrieves a market by its URL slug.
func (s *MarketStore) GetBySlug(ctx context.Context, slug string) (domain.Market, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+marketCols+` FROM markets WHERE slug = $1`, slug)
	m, err := scanMarket(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Market{}, domain.ErrNotFound
		}
		return domain.Market{}, fmt.Errorf("postgres: get market by slug %s: %w", slug, err)
	}
	return m, nil
}

// ListActive returns active markets with pagination and optional time filtering.
func (s *MarketStore) ListActive(ctx context.Context, opts domain.ListOpts) ([]domain.Market, error) {
	query := `SELECT ` + marketCols + ` FROM markets WHERE status = 'active'`
	args := []any{}
	argIdx := 1

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
		return nil, fmt.Errorf("postgres: list active markets: %w", err)
	}
	defer rows.Close()

	var markets []domain.Market
	for rows.Next() {
		var m domain.Market
		var status string
		if err := rows.Scan(
			&m.ID, &m.Question, &m.Slug,
			&m.Outcomes[0], &m.Outcomes[1],
			&m.TokenIDs[0], &m.TokenIDs[1],
			&m.ConditionID, &m.NegRisk,
			&m.Volume, &status, &m.ClosedAt,
			&m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan active market: %w", err)
		}
		m.Status = domain.MarketStatus(status)
		markets = append(markets, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list active markets rows: %w", err)
	}
	return markets, nil
}

// Count returns the total number of markets in the database.
func (s *MarketStore) Count(ctx context.Context) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM markets").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: count markets: %w", err)
	}
	return count, nil
}
