// Package config defines the top-level configuration for the polymarket bot
// and provides validation helpers.
package config

import (
	"fmt"
	"strings"
	"time"
)

// Config is the root configuration structure. Fields are populated from a TOML
// file and then optionally overridden by POLYBOT_* environment variables.
type Config struct {
	Wallet     WalletConfig     `toml:"wallet"`
	Polymarket PolymarketConfig `toml:"polymarket"`
	Builder    BuilderConfig    `toml:"builder"`
	Kalshi     KalshiConfig     `toml:"kalshi"`
	Supabase   SupabaseConfig   `toml:"supabase"`
	Redis      RedisConfig      `toml:"redis"`
	S3         S3Config         `toml:"s3"`
	Strategy   StrategyConfig   `toml:"strategy"`
	Arbitrage  ArbitrageConfig  `toml:"arbitrage"`
	Pipeline   PipelineConfig   `toml:"pipeline"`
	Server     ServerConfig     `toml:"server"`
	Notify     NotifyConfig     `toml:"notify"`
	Mode       string           `toml:"mode"`
	LogLevel   string           `toml:"log_level"`
}

// WalletConfig holds Ethereum wallet credentials.
type WalletConfig struct {
	PrivateKey       string `toml:"private_key"`
	SafeAddress      string `toml:"safe_address"`
	EncryptedKeyPath string `toml:"encrypted_key_path"`
	KeyPassword      string `toml:"key_password"`
}

// PolymarketConfig holds Polymarket API endpoints and chain parameters.
type PolymarketConfig struct {
	ClobHost      string `toml:"clob_host"`
	GammaHost     string `toml:"gamma_host"`
	WsHost        string `toml:"ws_host"`
	ChainID       int    `toml:"chain_id"`
	SignatureType int    `toml:"signature_type"`
}

// BuilderConfig holds Polymarket builder-program API credentials.
type BuilderConfig struct {
	ApiKey        string `toml:"api_key"`
	ApiSecret     string `toml:"api_secret"`
	ApiPassphrase string `toml:"api_passphrase"`
}

// KalshiConfig holds Kalshi exchange API credentials.
type KalshiConfig struct {
	ApiKey            string `toml:"api_key"`
	RsaPrivateKeyPath string `toml:"rsa_private_key_path"`
	BaseURL           string `toml:"base_url"`
}

// SupabaseConfig holds PostgreSQL / Supabase connection parameters.
type SupabaseConfig struct {
	DSN           string `toml:"dsn"`
	Host          string `toml:"host"`
	Port          int    `toml:"port"`
	Database      string `toml:"database"`
	User          string `toml:"user"`
	Password      string `toml:"password"`
	SSLMode       string `toml:"ssl_mode"`
	PoolMaxConns  int    `toml:"pool_max_conns"`
	PoolMinConns  int    `toml:"pool_min_conns"`
	ApiURL        string `toml:"api_url"`
	ApiKey        string `toml:"api_key"`
	RunMigrations bool   `toml:"run_migrations"`
}

// RedisConfig holds Redis connection parameters.
type RedisConfig struct {
	Addr       string `toml:"addr"`
	Password   string `toml:"password"`
	DB         int    `toml:"db"`
	PoolSize   int    `toml:"pool_size"`
	MaxRetries int    `toml:"max_retries"`
	TLSEnabled bool   `toml:"tls_enabled"`
}

// S3Config holds S3-compatible object storage parameters.
type S3Config struct {
	Endpoint       string `toml:"endpoint"`
	Region         string `toml:"region"`
	Bucket         string `toml:"bucket"`
	AccessKey      string `toml:"access_key"`
	SecretKey      string `toml:"secret_key"`
	UseSSL         bool   `toml:"use_ssl"`
	ForcePathStyle bool   `toml:"force_path_style"`
}

// StrategyConfig holds trading strategy parameters.
type StrategyConfig struct {
	Name         string         `toml:"name"`
	AutoExecute  bool           `toml:"auto_execute"`
	Coin         string         `toml:"coin"`
	Size         float64        `toml:"size"`
	PriceScale   int            `toml:"price_scale"`
	SizeScale    int            `toml:"size_scale"`
	MaxPositions int            `toml:"max_positions"`
	TakeProfit   float64        `toml:"take_profit"`
	StopLoss     float64        `toml:"stop_loss"`
	Params       map[string]any `toml:"params"`
	// Active is the list of strategy names to run concurrently (multi-strategy mode). If set, engine uses RunAll.
	Active []string `toml:"active"`

	RebalancingArb    RebalancingArbConfig    `toml:"rebalancing_arb"`
	Bond              BondStrategyConfig      `toml:"bond"`
	LiquidityProvider LiquidityProviderConfig `toml:"liquidity_provider"`
	CombinatorialArb  CombinatorialArbConfig  `toml:"combinatorial_arb"`
	YesNoSpread       YesNoSpreadConfig       `toml:"yes_no_spread"`
	CrossPlatformArb  CrossPlatformArbConfig  `toml:"cross_platform_arb"`
	TemporalOverlap   TemporalOverlapConfig   `toml:"temporal_overlap"`
}

// RebalancingArbConfig holds config for rebalancing_arb strategy.
type RebalancingArbConfig struct {
	Enabled      bool    `toml:"enabled"`
	MinEdgeBps   int     `toml:"min_edge_bps"`
	MaxGroupSize int     `toml:"max_group_size"`
	SizePerLeg   float64 `toml:"size_per_leg"`
	TTLSeconds   int     `toml:"ttl_seconds"`
	MaxStaleSec  int     `toml:"max_stale_sec"`
}

// BondStrategyConfig holds config for bond strategy.
type BondStrategyConfig struct {
	Enabled         bool    `toml:"enabled"`
	MinYesPrice     float64 `toml:"min_yes_price"`
	MinAPR          float64 `toml:"min_apr"`
	MinVolume       float64 `toml:"min_volume"`
	MaxDaysToExp    int     `toml:"max_days_to_exp"`
	MinDaysToExp    int     `toml:"min_days_to_exp"`
	MaxPositions    int     `toml:"max_positions"`
	SizePerPosition float64 `toml:"size_per_position"`
}

// LiquidityProviderConfig holds config for liquidity_provider strategy.
type LiquidityProviderConfig struct {
	Enabled          bool    `toml:"enabled"`
	HalfSpreadBps    int     `toml:"half_spread_bps"`
	RequoteThreshold float64 `toml:"requote_threshold"`
	Size             float64 `toml:"size"`
	MaxMarkets       int     `toml:"max_markets"`
	MinVolume        float64 `toml:"min_volume"`
	RewardsOnly      bool    `toml:"rewards_only"`
}

// CombinatorialArbConfig holds config for combinatorial_arb strategy.
type CombinatorialArbConfig struct {
	Enabled      bool    `toml:"enabled"`
	MinEdgeBps   int     `toml:"min_edge_bps"`
	MaxRelations int     `toml:"max_relations"`
	SizePerLeg   float64 `toml:"size_per_leg"`
}

// YesNoSpreadConfig holds config for yes_no_spread strategy.
type YesNoSpreadConfig struct {
	Enabled     bool    `toml:"enabled"`
	MinEdgeBps  int     `toml:"min_edge_bps"`
	SizePerLeg  float64 `toml:"size_per_leg"`
	TTLSeconds  int     `toml:"ttl_seconds"`
	MaxStaleSec int     `toml:"max_stale_sec"`
	CooldownSec int     `toml:"cooldown_sec"`
}

// CrossPlatformArbConfig holds config for cross_platform_arb strategy.
type CrossPlatformArbConfig struct {
	Enabled     bool              `toml:"enabled"`
	MinEdgeBps  int               `toml:"min_edge_bps"`
	SizePerLeg  float64           `toml:"size_per_leg"`
	TTLSeconds  int               `toml:"ttl_seconds"`
	RefreshSec  int               `toml:"refresh_sec"`
	MaxStaleSec int               `toml:"max_stale_sec"`
	CooldownSec int               `toml:"cooldown_sec"`
	MarketMap   map[string]string `toml:"market_map"`
}

// TemporalOverlapConfig holds config for temporal_overlap strategy.
type TemporalOverlapConfig struct {
	Enabled        bool    `toml:"enabled"`
	MinEdgeBps     int     `toml:"min_edge_bps"`
	SizePerLeg     float64 `toml:"size_per_leg"`
	TTLSeconds     int     `toml:"ttl_seconds"`
	MaxStaleSec    int     `toml:"max_stale_sec"`
	CooldownSec    int     `toml:"cooldown_sec"`
	RefreshMinutes int     `toml:"refresh_minutes"`
	MaxPairs       int     `toml:"max_pairs"`
}

// ArbitrageConfig holds arbitrage parameters and selectable strategy.
type ArbitrageConfig struct {
	// Strategy selects which arbitrage strategy to run: "spread", "imbalance", "yes_no_spread".
	Strategy            string             `toml:"strategy"`
	Enabled             bool               `toml:"enabled"`
	MinNetEdgeBps       float64            `toml:"min_net_edge_bps"`
	MaxTradeAmount      float64            `toml:"max_trade_amount"`
	MaxTradesPerOpp     int                `toml:"max_trades_per_opp"`
	MinDurationMs       int64              `toml:"min_duration_ms"`
	MaxLegGapMs         int64              `toml:"max_leg_gap_ms"`
	MaxUnhedgedNotional float64            `toml:"max_unhedged_notional"`
	MaxSlippageBps      float64            `toml:"max_slippage_bps"`
	KillSwitchLossUSD   float64            `toml:"kill_switch_loss_usd"`
	PerVenueFeeBps      map[string]float64 `toml:"per_venue_fee_bps"`
	// MinSpreadBps is the minimum bid-ask spread in bps for spread strategy.
	MinSpreadBps float64 `toml:"min_spread_bps"`
	// ImbalanceRatioThreshold: bid_vol/ask_vol or ask_vol/bid_vol must exceed this for imbalance strategy.
	ImbalanceRatioThreshold float64 `toml:"imbalance_ratio_threshold"`
}

// PipelineConfig holds data-pipeline / scraping parameters.
type PipelineConfig struct {
	Enabled              bool     `toml:"enabled"`
	GoldskyURL           string   `toml:"goldsky_url"`
	GoldskyAPIKey        string   `toml:"goldsky_api_key"`
	ScrapeInterval       duration `toml:"scrape_interval"`
	ArchiveRetentionDays int      `toml:"archive_retention_days"`
	ArchiveCron          string   `toml:"archive_cron"`
}

// duration is a wrapper around time.Duration that supports TOML string decoding
// (e.g. "5m", "30s").
type duration struct {
	time.Duration
}

// UnmarshalText implements encoding.TextUnmarshaler so the TOML decoder can
// parse duration strings like "5m" or "30s".
func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

// MarshalText implements encoding.TextMarshaler for round-trip encoding.
func (d duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// ServerConfig holds HTTP server parameters.
type ServerConfig struct {
	Enabled     bool     `toml:"enabled"`
	Port        int      `toml:"port"`
	CORSOrigins []string `toml:"cors_origins"`
}

// NotifyConfig holds notification channel credentials.
type NotifyConfig struct {
	TelegramToken     string   `toml:"telegram_token"`
	TelegramChatID    string   `toml:"telegram_chat_id"`
	DiscordWebhookURL string   `toml:"discord_webhook_url"`
	Events            []string `toml:"events"`
}

// Defaults returns a Config populated with reasonable default values.
// These match the values in config.example.toml.
func Defaults() Config {
	return Config{
		Polymarket: PolymarketConfig{
			ClobHost:      "https://clob.polymarket.com",
			GammaHost:     "https://gamma-api.polymarket.com",
			WsHost:        "wss://ws-subscriptions-clob.polymarket.com",
			ChainID:       137,
			SignatureType: 2,
		},
		Kalshi: KalshiConfig{
			BaseURL: "https://api.elections.kalshi.com/trade-api/v2",
		},
		Supabase: SupabaseConfig{
			DSN:           "",
			Host:          "localhost",
			Port:          5432,
			Database:      "postgres",
			User:          "postgres",
			SSLMode:       "disable",
			PoolMaxConns:  10,
			PoolMinConns:  2,
			RunMigrations: true,
		},
		Redis: RedisConfig{
			Addr:       "localhost:6379",
			DB:         0,
			PoolSize:   20,
			MaxRetries: 3,
			TLSEnabled: false,
		},
		S3: S3Config{
			Endpoint:       "http://localhost:9000",
			Region:         "us-east-1",
			Bucket:         "polybot-data",
			UseSSL:         false,
			ForcePathStyle: true,
		},
		Strategy: StrategyConfig{
			Name:         "flash_crash",
			AutoExecute:  true,
			Coin:         "ETH",
			Size:         5.0,
			PriceScale:   1_000_000,
			SizeScale:    1_000_000,
			MaxPositions: 1,
			TakeProfit:   0.10,
			StopLoss:     0.05,
			Params:       map[string]any{},
			YesNoSpread: YesNoSpreadConfig{
				Enabled:     true,
				MinEdgeBps:  40,
				SizePerLeg:  5.0,
				TTLSeconds:  30,
				MaxStaleSec: 5,
				CooldownSec: 2,
			},
			CrossPlatformArb: CrossPlatformArbConfig{
				Enabled:     false,
				MinEdgeBps:  60,
				SizePerLeg:  5.0,
				TTLSeconds:  30,
				RefreshSec:  5,
				MaxStaleSec: 8,
				CooldownSec: 3,
				MarketMap:   map[string]string{},
			},
			TemporalOverlap: TemporalOverlapConfig{
				Enabled:        false,
				MinEdgeBps:     80,
				SizePerLeg:     5.0,
				TTLSeconds:     30,
				MaxStaleSec:    6,
				CooldownSec:    3,
				RefreshMinutes: 10,
				MaxPairs:       100,
			},
		},
		Arbitrage: ArbitrageConfig{
			Strategy:                "spread",
			Enabled:                 false,
			MinNetEdgeBps:           50.0,
			MaxTradeAmount:          10.0,
			MaxTradesPerOpp:         2,
			MinDurationMs:           500,
			MaxLegGapMs:             2000,
			MaxUnhedgedNotional:     50.0,
			MaxSlippageBps:          20.0,
			KillSwitchLossUSD:       100.0,
			MinSpreadBps:            30.0,
			ImbalanceRatioThreshold: 1.5,
			PerVenueFeeBps: map[string]float64{
				"polymarket": 0.0,
				"kalshi":     7.0,
			},
		},
		Pipeline: PipelineConfig{
			Enabled:              false,
			GoldskyURL:           "", // Set to your Goldsky subgraph URL when you have one; leave empty to skip order-fill scrape
			GoldskyAPIKey:        "",
			ScrapeInterval:       duration{5 * time.Minute},
			ArchiveRetentionDays: 90,
			ArchiveCron:          "0 3 1 * *",
		},
		Server: ServerConfig{
			Enabled:     true,
			Port:        8000,
			CORSOrigins: []string{"http://localhost:3000", "http://localhost:5173"},
		},
		Notify: NotifyConfig{
			Events: []string{"arb_detected", "order_filled", "position_closed", "error"},
		},
		Mode:     "full",
		LogLevel: "info",
	}
}

// validModes enumerates the accepted values for Config.Mode.
var validModes = map[string]bool{
	"trade":     true,
	"arbitrage": true,
	"monitor":   true,
	"scrape":    true,
	"backtest":  true,
	"server":    true,
	"full":      true,
}

// validLogLevels enumerates the accepted values for Config.LogLevel.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// Validate checks Config for obviously invalid or missing values and returns a
// combined error describing every problem found.
func (c *Config) Validate() error {
	var errs []string

	// Mode
	if !validModes[strings.ToLower(c.Mode)] {
		errs = append(errs, fmt.Sprintf("unknown mode %q (valid: trade, arbitrage, monitor, scrape, backtest, server, full)", c.Mode))
	}

	// LogLevel
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		errs = append(errs, fmt.Sprintf("unknown log_level %q (valid: debug, info, warn, error)", c.LogLevel))
	}

	// Wallet — at least one credential source must be specified for trading modes.
	needsWallet := c.Mode == "trade" || c.Mode == "arbitrage" || c.Mode == "full"
	if needsWallet {
		if c.Wallet.PrivateKey == "" && c.Wallet.EncryptedKeyPath == "" {
			errs = append(errs, "wallet: either private_key or encrypted_key_path must be set for mode "+c.Mode)
		}
		if c.Wallet.EncryptedKeyPath != "" && c.Wallet.KeyPassword == "" {
			errs = append(errs, "wallet: key_password is required when encrypted_key_path is set")
		}
	}

	// Polymarket endpoints
	if c.Polymarket.ClobHost == "" {
		errs = append(errs, "polymarket: clob_host must not be empty")
	}
	if c.Polymarket.ChainID <= 0 {
		errs = append(errs, "polymarket: chain_id must be positive")
	}
	if c.Polymarket.SignatureType != 1 && c.Polymarket.SignatureType != 2 {
		errs = append(errs, fmt.Sprintf("polymarket: signature_type must be 1 (EOA) or 2 (Safe), got %d", c.Polymarket.SignatureType))
	}

	// Builder — all three fields must be set together, or all empty.
	bk := c.Builder.ApiKey != ""
	bs := c.Builder.ApiSecret != ""
	bp := c.Builder.ApiPassphrase != ""
	if bk || bs || bp {
		if !(bk && bs && bp) {
			errs = append(errs, "builder: api_key, api_secret, and api_passphrase must all be set together")
		}
	}

	// Kalshi — needed for arbitrage mode.
	if c.Mode == "arbitrage" || (c.Mode == "full" && c.Arbitrage.Enabled) {
		if c.Kalshi.ApiKey == "" {
			errs = append(errs, "kalshi: api_key is required for arbitrage mode")
		}
		if c.Kalshi.BaseURL == "" {
			errs = append(errs, "kalshi: base_url must not be empty")
		}
	}

	// Supabase
	if strings.TrimSpace(c.Supabase.DSN) == "" {
		if c.Supabase.Host == "" {
			errs = append(errs, "supabase: host must not be empty (or set supabase.dsn)")
		}
		if c.Supabase.Port <= 0 || c.Supabase.Port > 65535 {
			errs = append(errs, fmt.Sprintf("supabase: port must be 1-65535, got %d", c.Supabase.Port))
		}
		if c.Supabase.Database == "" {
			errs = append(errs, "supabase: database must not be empty")
		}
	}
	if c.Supabase.PoolMaxConns < 1 {
		errs = append(errs, "supabase: pool_max_conns must be >= 1")
	}
	if c.Supabase.PoolMinConns < 0 {
		errs = append(errs, "supabase: pool_min_conns must be >= 0")
	}
	if c.Supabase.PoolMinConns > c.Supabase.PoolMaxConns {
		errs = append(errs, "supabase: pool_min_conns must not exceed pool_max_conns")
	}

	// Redis
	if c.Redis.Addr == "" {
		errs = append(errs, "redis: addr must not be empty")
	}
	if c.Redis.PoolSize < 1 {
		errs = append(errs, "redis: pool_size must be >= 1")
	}

	// S3
	if c.S3.Endpoint == "" {
		errs = append(errs, "s3: endpoint must not be empty")
	}
	if c.S3.Bucket == "" {
		errs = append(errs, "s3: bucket must not be empty")
	}

	// Strategy
	if c.Strategy.Size <= 0 {
		errs = append(errs, "strategy: size must be > 0")
	}
	if c.Strategy.PriceScale <= 0 {
		errs = append(errs, "strategy: price_scale must be > 0")
	}
	if c.Strategy.SizeScale <= 0 {
		errs = append(errs, "strategy: size_scale must be > 0")
	}
	if c.Strategy.MaxPositions < 1 {
		errs = append(errs, "strategy: max_positions must be >= 1")
	}

	// Arbitrage
	if c.Arbitrage.Enabled {
		if c.Arbitrage.MinNetEdgeBps <= 0 {
			errs = append(errs, "arbitrage: min_net_edge_bps must be > 0 when enabled")
		}
		if c.Arbitrage.MaxTradeAmount <= 0 {
			errs = append(errs, "arbitrage: max_trade_amount must be > 0 when enabled")
		}
		if c.Arbitrage.KillSwitchLossUSD <= 0 {
			errs = append(errs, "arbitrage: kill_switch_loss_usd must be > 0 when enabled")
		}
	}

	// Server
	if c.Server.Enabled {
		if c.Server.Port <= 0 || c.Server.Port > 65535 {
			errs = append(errs, fmt.Sprintf("server: port must be 1-65535, got %d", c.Server.Port))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
