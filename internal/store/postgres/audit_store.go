package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// AuditStore implements domain.AuditStore using PostgreSQL.
type AuditStore struct {
	pool *pgxpool.Pool
}

// NewAuditStore creates a new AuditStore backed by the given connection pool.
func NewAuditStore(pool *pgxpool.Pool) *AuditStore {
	return &AuditStore{pool: pool}
}

// Log appends a new audit entry with the given event name and detail map.
// The detail map is stored as JSONB in the database.
func (s *AuditStore) Log(ctx context.Context, event string, detail map[string]any) error {
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("postgres: marshal audit detail: %w", err)
	}

	const query = `INSERT INTO audit_log (event, detail) VALUES ($1, $2)`
	_, err = s.pool.Exec(ctx, query, event, detailJSON)
	if err != nil {
		return fmt.Errorf("postgres: log audit event %s: %w", event, err)
	}
	return nil
}

// List returns audit entries with pagination and optional time filtering.
func (s *AuditStore) List(ctx context.Context, opts domain.ListOpts) ([]domain.AuditEntry, error) {
	query := `SELECT id, event, detail, created_at FROM audit_log WHERE 1=1`
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
		return nil, fmt.Errorf("postgres: list audit entries: %w", err)
	}
	defer rows.Close()

	var entries []domain.AuditEntry
	for rows.Next() {
		var e domain.AuditEntry
		var detailJSON []byte

		if err := rows.Scan(&e.ID, &e.Event, &detailJSON, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan audit entry: %w", err)
		}

		if detailJSON != nil {
			if err := json.Unmarshal(detailJSON, &e.Detail); err != nil {
				return nil, fmt.Errorf("postgres: unmarshal audit detail: %w", err)
			}
		}

		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list audit entries rows: %w", err)
	}
	return entries, nil
}
