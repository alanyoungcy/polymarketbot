package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	s3blob "github.com/alanyoungcy/polymarketbot/internal/blob/s3"
	"github.com/alanyoungcy/polymarketbot/internal/cache/redis"
	"github.com/alanyoungcy/polymarketbot/internal/config"
	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/notify"
	"github.com/alanyoungcy/polymarketbot/internal/store/postgres"
)

// Dependencies bundles every domain-level dependency that the application modes
// need to operate. It is constructed by Wire and torn down by the returned
// cleanup function.
type Dependencies struct {
	// Stores
	MarketStore          domain.MarketStore
	OrderStore           domain.OrderStore
	PositionStore        domain.PositionStore
	TradeStore           domain.TradeStore
	ArbStore             domain.ArbStore
	ArbExecutionStore    domain.ArbExecutionStore
	AuditStore           domain.AuditStore
	StratCfgStore        domain.StrategyConfigStore
	ConditionGroupStore  domain.ConditionGroupStore
	BondPositionStore    domain.BondPositionStore
	MarketRelationStore  domain.MarketRelationStore

	// Caches
	PriceCache           domain.PriceCache
	BookCache            domain.OrderbookCache
	MarketCache          domain.MarketCache
	ConditionGroupCache  domain.ConditionGroupCache
	RateLimiter          domain.RateLimiter
	LockManager          domain.LockManager
	SignalBus            domain.SignalBus

	// Blob storage
	BlobWriter  domain.BlobWriter
	BlobReader  domain.BlobReader
	BlobDeleter domain.BlobDeleter
	Archiver    domain.Archiver

	// Notifications
	Notifier *notify.Notifier
}

// needsPostgres returns true for modes that require a database connection.
func needsPostgres(mode string) bool {
	switch mode {
	case "trade", "arbitrage", "scrape", "backtest", "full":
		return true
	default:
		return false
	}
}

// needsS3 returns true for modes that require object storage.
func needsS3(mode string) bool {
	switch mode {
	case "scrape", "backtest", "full":
		return true
	default:
		return false
	}
}

// Wire constructs all concrete dependency implementations from the given
// configuration and returns them together with a cleanup function that should
// be called on shutdown to release resources.
func Wire(ctx context.Context, cfg *config.Config) (*Dependencies, func(), error) {
	logger := slog.Default()

	var closers []func()
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	deps := &Dependencies{}

	// --- PostgreSQL (only for modes that need persistence) ---
	if needsPostgres(cfg.Mode) {
		pgClient, err := postgres.New(ctx, postgres.ClientConfig{
			DSN:      cfg.Supabase.DSN,
			Host:     cfg.Supabase.Host,
			Port:     cfg.Supabase.Port,
			Database: cfg.Supabase.Database,
			User:     cfg.Supabase.User,
			Password: cfg.Supabase.Password,
			SSLMode:  cfg.Supabase.SSLMode,
			MaxConns: cfg.Supabase.PoolMaxConns,
			MinConns: cfg.Supabase.PoolMinConns,
		})
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("wire: postgres: %w", err)
		}
		closers = append(closers, pgClient.Close)

		// Run migrations if enabled.
		if cfg.Supabase.RunMigrations {
			if err := pgClient.RunMigrations(ctx); err != nil {
				cleanup()
				return nil, nil, fmt.Errorf("wire: postgres migrations: %w", err)
			}
		}

		pool := pgClient.Pool()
		deps.MarketStore = postgres.NewMarketStore(pool)
		deps.OrderStore = postgres.NewOrderStore(pool)
		deps.PositionStore = postgres.NewPositionStore(pool)
		deps.TradeStore = postgres.NewTradeStore(pool)
		deps.ArbStore = postgres.NewArbStore(pool)
		deps.ArbExecutionStore = postgres.NewArbExecutionStore(pool)
		deps.AuditStore = postgres.NewAuditStore(pool)
		deps.StratCfgStore = postgres.NewStrategyConfigStore(pool)
		deps.ConditionGroupStore = postgres.NewConditionGroupStore(pool)
		deps.BondPositionStore = postgres.NewBondPositionStore(pool)
		deps.MarketRelationStore = postgres.NewMarketRelationStore(pool)
	}

	// --- Redis ---
	redisClient, err := redis.New(ctx, redis.ClientConfig{
		Addr:       cfg.Redis.Addr,
		Password:   cfg.Redis.Password,
		DB:         cfg.Redis.DB,
		PoolSize:   cfg.Redis.PoolSize,
		MaxRetries: cfg.Redis.MaxRetries,
		TLSEnabled: cfg.Redis.TLSEnabled,
	})
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("wire: redis: %w", err)
	}
	closers = append(closers, func() { _ = redisClient.Close() })

	redisTTL := time.Duration(0)
	if cfg.Redis.CacheTTLMinutes > 0 {
		redisTTL = time.Duration(cfg.Redis.CacheTTLMinutes) * time.Minute
	}
	streamMaxLen := int64(10000)
	if cfg.Redis.StreamMaxLen > 0 {
		streamMaxLen = int64(cfg.Redis.StreamMaxLen)
	}

	deps.PriceCache = redis.NewPriceCache(redisClient, redisTTL)
	deps.BookCache = redis.NewOrderbookCache(redisClient, redisTTL)
	deps.MarketCache = redis.NewMarketCache(redisClient)
	deps.ConditionGroupCache = redis.NewConditionGroupCache(redisClient)
	deps.RateLimiter = redis.NewRateLimiter(redisClient)
	deps.LockManager = redis.NewLockManager(redisClient)
	deps.SignalBus = redis.NewSignalBusWithMaxLen(redisClient, streamMaxLen)

	// --- S3 blob storage (only for modes that need object storage) ---
	if needsS3(cfg.Mode) {
		s3Client, err := s3blob.New(ctx, s3blob.ClientConfig{
			Endpoint:       cfg.S3.Endpoint,
			Region:         cfg.S3.Region,
			Bucket:         cfg.S3.Bucket,
			AccessKey:      cfg.S3.AccessKey,
			SecretKey:      cfg.S3.SecretKey,
			UseSSL:         cfg.S3.UseSSL,
			ForcePathStyle: cfg.S3.ForcePathStyle,
		})
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("wire: s3: %w", err)
		}
		closers = append(closers, func() { _ = s3Client.Close() })

		deps.BlobWriter = s3blob.NewWriter(s3Client)
		reader := s3blob.NewReader(s3Client)
		deps.BlobReader = reader
		deps.BlobDeleter = reader // same type implements BlobDeleter
		// Archiver: only when we also have Postgres (stores with ListBefore + AuditStore)
		if deps.TradeStore != nil && deps.OrderStore != nil && deps.ArbStore != nil && deps.AuditStore != nil {
			deps.Archiver = s3blob.NewArchiver(
				deps.BlobWriter,
				deps.TradeStore,
				deps.OrderStore,
				deps.ArbStore,
				deps.AuditStore,
			)
		}
	}

	// --- Notifications ---
	var senders []notify.Sender
	if cfg.Notify.TelegramToken != "" && cfg.Notify.TelegramChatID != "" {
		senders = append(senders, notify.NewTelegramSender(
			cfg.Notify.TelegramToken,
			cfg.Notify.TelegramChatID,
		))
	}
	if cfg.Notify.DiscordWebhookURL != "" {
		senders = append(senders, notify.NewDiscordSender(cfg.Notify.DiscordWebhookURL))
	}
	deps.Notifier = notify.NewNotifier(senders, cfg.Notify.Events, logger)

	return deps, cleanup, nil
}
