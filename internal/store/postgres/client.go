// Package postgres implements domain store interfaces using PostgreSQL via pgx.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ClientConfig holds connection parameters for the PostgreSQL client.
type ClientConfig struct {
	DSN      string
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string
	MaxConns int
	MinConns int
}

// DSN builds a PostgreSQL connection string from the given config.
func DSN(cfg ClientConfig) string {
	if strings.TrimSpace(cfg.DSN) != "" {
		return cfg.DSN
	}

	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, port, cfg.Database, sslMode,
	)
}

// Client wraps a pgxpool.Pool and manages migrations.
type Client struct {
	pool *pgxpool.Pool
}

// New creates a new Client with a connection pool configured from cfg.
func New(ctx context.Context, cfg ClientConfig) (*Client, error) {
	dsn := DSN(cfg)

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = int32(cfg.MaxConns)
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = int32(cfg.MinConns)
	}

	// Prefer IPv4 when possible, but gracefully handle IPv6-only endpoints
	// (for example Supabase hosts that resolve to AAAA records).
	poolCfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("postgres: split host/port %q: %w", addr, err)
		}

		dialer := &net.Dialer{}

		// If pgx already passed an IP literal, dial with the matching family.
		if ip := net.ParseIP(host); ip != nil {
			if ip.To4() != nil {
				return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			}
			return dialer.DialContext(ctx, "tcp6", net.JoinHostPort(ip.String(), port))
		}

		// Prefer IPv4 first.
		ipv4s, err4 := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		for _, ip := range ipv4s {
			conn, dialErr := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
		}

		// Fallback: let the system resolver/dialer handle dual-stack targets.
		conn, err := dialer.DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}

		if err4 != nil {
			return nil, fmt.Errorf("postgres: dial %q failed (ipv4 lookup=%v, fallback=%w)", addr, err4, err)
		}
		return nil, fmt.Errorf("postgres: dial %q failed: %w", addr, errors.Join(err4, err))
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return &Client{pool: pool}, nil
}

// Pool returns the underlying connection pool.
func (c *Client) Pool() *pgxpool.Pool {
	return c.pool
}

// Close shuts down the connection pool.
func (c *Client) Close() {
	c.pool.Close()
}

// RunMigrations reads embedded SQL files from the migrations/ directory,
// applies them in lexicographic order, and tracks applied migrations in a
// schema_migrations table.
func (c *Client) RunMigrations(ctx context.Context) error {
	// Ensure the tracking table exists.
	const createTracker = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);`
	if _, err := c.pool.Exec(ctx, createTracker); err != nil {
		return fmt.Errorf("postgres: create schema_migrations table: %w", err)
	}

	// Read all migration files.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("postgres: read migrations dir: %w", err)
	}

	// Sort entries by name to guarantee order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Check if already applied.
		var exists bool
		err := c.pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)",
			entry.Name(),
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("postgres: check migration %s: %w", entry.Name(), err)
		}
		if exists {
			continue
		}

		// Read and execute the migration.
		data, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("postgres: read migration %s: %w", entry.Name(), err)
		}

		tx, err := c.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("postgres: begin tx for %s: %w", entry.Name(), err)
		}

		if _, err := tx.Exec(ctx, string(data)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: exec migration %s: %w", entry.Name(), err)
		}

		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (filename) VALUES ($1)",
			entry.Name(),
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("postgres: record migration %s: %w", entry.Name(), err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("postgres: commit migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}
