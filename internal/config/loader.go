package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/joho/godotenv"
)

// Load reads a TOML configuration file at path, merges it on top of the
// built-in defaults, applies POLYBOT_* environment variable overrides, and
// returns the final Config. The returned Config has NOT been validated; the
// caller should invoke Config.Validate() after Load.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}

	// Load .env file if present (silently ignore if missing).
	_ = godotenv.Load()

	applyEnvOverrides(&cfg)

	return &cfg, nil
}

// applyEnvOverrides reads well-known POLYBOT_* environment variables and
// overwrites the corresponding Config fields when a variable is set (i.e. not
// empty). This lets operators inject secrets at deploy time without touching
// the TOML file.
func applyEnvOverrides(cfg *Config) {
	// ── Wallet ──
	setStr(&cfg.Wallet.PrivateKey, "POLYBOT_WALLET_PRIVATE_KEY")
	setStr(&cfg.Wallet.SafeAddress, "POLYBOT_WALLET_SAFE_ADDRESS")
	setStr(&cfg.Wallet.EncryptedKeyPath, "POLYBOT_WALLET_ENCRYPTED_KEY_PATH")
	setStr(&cfg.Wallet.KeyPassword, "POLYBOT_WALLET_KEY_PASSWORD")

	// ── Polymarket ──
	setStr(&cfg.Polymarket.ClobHost, "POLYBOT_POLYMARKET_CLOB_HOST")
	setStr(&cfg.Polymarket.GammaHost, "POLYBOT_POLYMARKET_GAMMA_HOST")
	setStr(&cfg.Polymarket.WsHost, "POLYBOT_POLYMARKET_WS_HOST")
	setInt(&cfg.Polymarket.ChainID, "POLYBOT_POLYMARKET_CHAIN_ID")
	setInt(&cfg.Polymarket.SignatureType, "POLYBOT_POLYMARKET_SIGNATURE_TYPE")

	// ── Builder ──
	setStr(&cfg.Builder.ApiKey, "POLYBOT_BUILDER_API_KEY")
	setStr(&cfg.Builder.ApiSecret, "POLYBOT_BUILDER_API_SECRET")
	setStr(&cfg.Builder.ApiPassphrase, "POLYBOT_BUILDER_API_PASSPHRASE")

	// ── Kalshi ──
	setStr(&cfg.Kalshi.ApiKey, "POLYBOT_KALSHI_API_KEY")
	setStr(&cfg.Kalshi.RsaPrivateKeyPath, "POLYBOT_KALSHI_RSA_PRIVATE_KEY_PATH")
	setStr(&cfg.Kalshi.BaseURL, "POLYBOT_KALSHI_BASE_URL")

	// ── Supabase ──
	setStr(&cfg.Supabase.DSN, "POLYBOT_SUPABASE_DSN")
	setStr(&cfg.Supabase.DSN, "POLYBOT_SUPABASE_URL") // compatibility alias
	setStr(&cfg.Supabase.Host, "POLYBOT_SUPABASE_HOST")
	setInt(&cfg.Supabase.Port, "POLYBOT_SUPABASE_PORT")
	setStr(&cfg.Supabase.Database, "POLYBOT_SUPABASE_DATABASE")
	setStr(&cfg.Supabase.User, "POLYBOT_SUPABASE_USER")
	setStr(&cfg.Supabase.Password, "POLYBOT_SUPABASE_PASSWORD")
	setStr(&cfg.Supabase.SSLMode, "POLYBOT_SUPABASE_SSLMODE")
	setStr(&cfg.Supabase.SSLMode, "POLYBOT_SUPABASE_SSL_MODE") // compatibility alias
	setInt(&cfg.Supabase.PoolMaxConns, "POLYBOT_SUPABASE_POOL_MAX_CONNS")
	setInt(&cfg.Supabase.PoolMinConns, "POLYBOT_SUPABASE_POOL_MIN_CONNS")
	setStr(&cfg.Supabase.ApiURL, "POLYBOT_SUPABASE_API_URL")
	setStr(&cfg.Supabase.ApiKey, "POLYBOT_SUPABASE_API_KEY")
	setBool(&cfg.Supabase.RunMigrations, "POLYBOT_SUPABASE_RUN_MIGRATIONS")

	// ── Redis ──
	setStr(&cfg.Redis.Addr, "POLYBOT_REDIS_ADDR")
	setStr(&cfg.Redis.Password, "POLYBOT_REDIS_PASSWORD")
	setInt(&cfg.Redis.DB, "POLYBOT_REDIS_DB")
	setInt(&cfg.Redis.PoolSize, "POLYBOT_REDIS_POOL_SIZE")
	setInt(&cfg.Redis.MaxRetries, "POLYBOT_REDIS_MAX_RETRIES")
	setBool(&cfg.Redis.TLSEnabled, "POLYBOT_REDIS_TLS_ENABLED")

	// ── S3 ──
	setStr(&cfg.S3.Endpoint, "POLYBOT_S3_ENDPOINT")
	setStr(&cfg.S3.Region, "POLYBOT_S3_REGION")
	setStr(&cfg.S3.Bucket, "POLYBOT_S3_BUCKET")
	setStr(&cfg.S3.AccessKey, "POLYBOT_S3_ACCESS_KEY")
	setStr(&cfg.S3.SecretKey, "POLYBOT_S3_SECRET_KEY")
	setBool(&cfg.S3.UseSSL, "POLYBOT_S3_USE_SSL")
	setBool(&cfg.S3.ForcePathStyle, "POLYBOT_S3_FORCE_PATH_STYLE")

	// ── Strategy ──
	setStr(&cfg.Strategy.Name, "POLYBOT_STRATEGY_NAME")
	setBool(&cfg.Strategy.AutoExecute, "POLYBOT_STRATEGY_AUTO_EXECUTE")
	setStr(&cfg.Strategy.Coin, "POLYBOT_STRATEGY_COIN")
	setFloat64(&cfg.Strategy.Size, "POLYBOT_STRATEGY_SIZE")
	setInt(&cfg.Strategy.PriceScale, "POLYBOT_STRATEGY_PRICE_SCALE")
	setInt(&cfg.Strategy.SizeScale, "POLYBOT_STRATEGY_SIZE_SCALE")
	setInt(&cfg.Strategy.MaxPositions, "POLYBOT_STRATEGY_MAX_POSITIONS")
	setFloat64(&cfg.Strategy.TakeProfit, "POLYBOT_STRATEGY_TAKE_PROFIT")
	setFloat64(&cfg.Strategy.StopLoss, "POLYBOT_STRATEGY_STOP_LOSS")
	setBool(&cfg.Strategy.YesNoSpread.Enabled, "POLYBOT_STRATEGY_YES_NO_SPREAD_ENABLED")
	setBool(&cfg.Strategy.CrossPlatformArb.Enabled, "POLYBOT_STRATEGY_CROSS_PLATFORM_ARB_ENABLED")
	setBool(&cfg.Strategy.TemporalOverlap.Enabled, "POLYBOT_STRATEGY_TEMPORAL_OVERLAP_ENABLED")

	// ── Arbitrage ──
	setStr(&cfg.Arbitrage.Strategy, "POLYBOT_ARBITRAGE_STRATEGY")
	setBool(&cfg.Arbitrage.Enabled, "POLYBOT_ARBITRAGE_ENABLED")
	setFloat64(&cfg.Arbitrage.MinNetEdgeBps, "POLYBOT_ARBITRAGE_MIN_NET_EDGE_BPS")
	setFloat64(&cfg.Arbitrage.MaxTradeAmount, "POLYBOT_ARBITRAGE_MAX_TRADE_AMOUNT")
	setFloat64(&cfg.Arbitrage.MinSpreadBps, "POLYBOT_ARBITRAGE_MIN_SPREAD_BPS")
	setFloat64(&cfg.Arbitrage.ImbalanceRatioThreshold, "POLYBOT_ARBITRAGE_IMBALANCE_RATIO_THRESHOLD")
	setInt(&cfg.Arbitrage.MaxTradesPerOpp, "POLYBOT_ARBITRAGE_MAX_TRADES_PER_OPP")
	setInt64(&cfg.Arbitrage.MinDurationMs, "POLYBOT_ARBITRAGE_MIN_DURATION_MS")
	setInt64(&cfg.Arbitrage.MaxLegGapMs, "POLYBOT_ARBITRAGE_MAX_LEG_GAP_MS")
	setFloat64(&cfg.Arbitrage.MaxUnhedgedNotional, "POLYBOT_ARBITRAGE_MAX_UNHEDGED_NOTIONAL")
	setFloat64(&cfg.Arbitrage.MaxSlippageBps, "POLYBOT_ARBITRAGE_MAX_SLIPPAGE_BPS")
	setFloat64(&cfg.Arbitrage.KillSwitchLossUSD, "POLYBOT_ARBITRAGE_KILL_SWITCH_LOSS_USD")

	// ── Pipeline ──
	setBool(&cfg.Pipeline.Enabled, "POLYBOT_PIPELINE_ENABLED")
	setStr(&cfg.Pipeline.GoldskyURL, "POLYBOT_PIPELINE_GOLDSKY_URL")
	setStr(&cfg.Pipeline.GoldskyAPIKey, "POLYBOT_PIPELINE_GOLDSKY_API_KEY")
	setDuration(&cfg.Pipeline.ScrapeInterval, "POLYBOT_PIPELINE_SCRAPE_INTERVAL")
	setInt(&cfg.Pipeline.ArchiveRetentionDays, "POLYBOT_PIPELINE_ARCHIVE_RETENTION_DAYS")
	setStr(&cfg.Pipeline.ArchiveCron, "POLYBOT_PIPELINE_ARCHIVE_CRON")

	// ── Server ──
	setBool(&cfg.Server.Enabled, "POLYBOT_SERVER_ENABLED")
	setInt(&cfg.Server.Port, "POLYBOT_SERVER_PORT")
	setStringSlice(&cfg.Server.CORSOrigins, "POLYBOT_SERVER_CORS_ORIGINS")

	// ── Notify ──
	setStr(&cfg.Notify.TelegramToken, "POLYBOT_NOTIFY_TELEGRAM_TOKEN")
	setStr(&cfg.Notify.TelegramChatID, "POLYBOT_NOTIFY_TELEGRAM_CHAT_ID")
	setStr(&cfg.Notify.DiscordWebhookURL, "POLYBOT_NOTIFY_DISCORD_WEBHOOK_URL")
	setStringSlice(&cfg.Notify.Events, "POLYBOT_NOTIFY_EVENTS")

	// ── Top-level ──
	setStr(&cfg.Mode, "POLYBOT_MODE")
	setStr(&cfg.LogLevel, "POLYBOT_LOG_LEVEL")
}

// ---------------------------------------------------------------------------
// Typed env-var helpers. Each only mutates the target when the environment
// variable is present and non-empty.
// ---------------------------------------------------------------------------

func setStr(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func setInt(dst *int, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setInt64(dst *int64, key string) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			*dst = n
		}
	}
}

func setFloat64(dst *float64, key string) {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*dst = f
		}
	}
}

func setBool(dst *bool, key string) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setDuration(dst *duration, key string) {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			dst.Duration = d
		}
	}
}

func setStringSlice(dst *[]string, key string) {
	if v := os.Getenv(key); v != "" {
		parts := strings.Split(v, ",")
		cleaned := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				cleaned = append(cleaned, p)
			}
		}
		if len(cleaned) > 0 {
			*dst = cleaned
		}
	}
}
