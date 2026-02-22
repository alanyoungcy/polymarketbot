package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// StrategyConfigStore implements domain.StrategyConfigStore using PostgreSQL.
type StrategyConfigStore struct {
	pool *pgxpool.Pool
}

// NewStrategyConfigStore creates a new StrategyConfigStore backed by the given connection pool.
func NewStrategyConfigStore(pool *pgxpool.Pool) *StrategyConfigStore {
	return &StrategyConfigStore{pool: pool}
}

// Get retrieves a single strategy configuration by name.
func (s *StrategyConfigStore) Get(ctx context.Context, name string) (domain.StrategyConfig, error) {
	const query = `SELECT name, config_json, enabled, updated_at FROM strategy_configs WHERE name = $1`

	var cfg domain.StrategyConfig
	var configJSON []byte

	err := s.pool.QueryRow(ctx, query, name).Scan(
		&cfg.Name, &configJSON, &cfg.Enabled, &cfg.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.StrategyConfig{}, domain.ErrNotFound
		}
		return domain.StrategyConfig{}, fmt.Errorf("postgres: get strategy config %s: %w", name, err)
	}

	if configJSON != nil {
		if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
			return domain.StrategyConfig{}, fmt.Errorf("postgres: unmarshal strategy config %s: %w", name, err)
		}
	}

	return cfg, nil
}

// Upsert inserts or updates a strategy configuration. The Config map is stored as JSONB.
func (s *StrategyConfigStore) Upsert(ctx context.Context, cfg domain.StrategyConfig) error {
	configJSON, err := json.Marshal(cfg.Config)
	if err != nil {
		return fmt.Errorf("postgres: marshal strategy config %s: %w", cfg.Name, err)
	}

	const query = `
		INSERT INTO strategy_configs (name, config_json, enabled, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (name) DO UPDATE SET
			config_json = EXCLUDED.config_json,
			enabled     = EXCLUDED.enabled,
			updated_at  = NOW()`

	_, err = s.pool.Exec(ctx, query, cfg.Name, configJSON, cfg.Enabled)
	if err != nil {
		return fmt.Errorf("postgres: upsert strategy config %s: %w", cfg.Name, err)
	}
	return nil
}

// List returns all strategy configurations.
func (s *StrategyConfigStore) List(ctx context.Context) ([]domain.StrategyConfig, error) {
	const query = `SELECT name, config_json, enabled, updated_at FROM strategy_configs ORDER BY name`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: list strategy configs: %w", err)
	}
	defer rows.Close()

	var configs []domain.StrategyConfig
	for rows.Next() {
		var cfg domain.StrategyConfig
		var configJSON []byte

		if err := rows.Scan(&cfg.Name, &configJSON, &cfg.Enabled, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan strategy config: %w", err)
		}

		if configJSON != nil {
			if err := json.Unmarshal(configJSON, &cfg.Config); err != nil {
				return nil, fmt.Errorf("postgres: unmarshal strategy config: %w", err)
			}
		}

		configs = append(configs, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list strategy configs rows: %w", err)
	}
	return configs, nil
}
