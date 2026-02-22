package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// MarketRelationStore implements domain.MarketRelationStore using PostgreSQL.
type MarketRelationStore struct {
	pool *pgxpool.Pool
}

// NewMarketRelationStore creates a new MarketRelationStore.
func NewMarketRelationStore(pool *pgxpool.Pool) *MarketRelationStore {
	return &MarketRelationStore{pool: pool}
}

// Create inserts a new market relation.
func (s *MarketRelationStore) Create(ctx context.Context, r domain.MarketRelation) error {
	configJSON, _ := json.Marshal(r.Config)
	const query = `
		INSERT INTO market_relations (id, source_group_id, target_group_id, relation_type, confidence, config, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.pool.Exec(ctx, query,
		r.ID, r.SourceGroupID, r.TargetGroupID, string(r.RelationType), r.Confidence, configJSON, r.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: create market_relation %s: %w", r.ID, err)
	}
	return nil
}

// GetByID returns a market relation by id.
func (s *MarketRelationStore) GetByID(ctx context.Context, id string) (domain.MarketRelation, error) {
	const query = `SELECT id, source_group_id, target_group_id, relation_type, confidence, config, created_at FROM market_relations WHERE id = $1`
	var r domain.MarketRelation
	var configJSON []byte
	var relType string
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&r.ID, &r.SourceGroupID, &r.TargetGroupID, &relType, &r.Confidence, &configJSON, &r.CreatedAt,
	)
	if err != nil {
		return domain.MarketRelation{}, fmt.Errorf("postgres: get market_relation %s: %w", id, err)
	}
	r.RelationType = domain.RelationType(relType)
	if len(configJSON) > 0 {
		_ = json.Unmarshal(configJSON, &r.Config)
	}
	return r, nil
}

// ListBySource returns relations where source_group_id = id.
func (s *MarketRelationStore) ListBySource(ctx context.Context, sourceGroupID string) ([]domain.MarketRelation, error) {
	const query = `SELECT id, source_group_id, target_group_id, relation_type, confidence, config, created_at FROM market_relations WHERE source_group_id = $1`
	return s.queryRelations(ctx, query, sourceGroupID)
}

// ListByTarget returns relations where target_group_id = id.
func (s *MarketRelationStore) ListByTarget(ctx context.Context, targetGroupID string) ([]domain.MarketRelation, error) {
	const query = `SELECT id, source_group_id, target_group_id, relation_type, confidence, config, created_at FROM market_relations WHERE target_group_id = $1`
	return s.queryRelations(ctx, query, targetGroupID)
}

// List returns all market relations.
func (s *MarketRelationStore) List(ctx context.Context) ([]domain.MarketRelation, error) {
	const query = `SELECT id, source_group_id, target_group_id, relation_type, confidence, config, created_at FROM market_relations ORDER BY id`
	return s.queryRelations(ctx, query)
}

func (s *MarketRelationStore) queryRelations(ctx context.Context, query string, args ...any) ([]domain.MarketRelation, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []domain.MarketRelation
	for rows.Next() {
		var r domain.MarketRelation
		var configJSON []byte
		var relType string
		if err := rows.Scan(&r.ID, &r.SourceGroupID, &r.TargetGroupID, &relType, &r.Confidence, &configJSON, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.RelationType = domain.RelationType(relType)
		if len(configJSON) > 0 {
			_ = json.Unmarshal(configJSON, &r.Config)
		}
		list = append(list, r)
	}
	return list, rows.Err()
}
