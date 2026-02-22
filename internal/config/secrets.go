package config

// RedactedConfig returns a shallow copy of cfg with sensitive fields replaced
// by the redaction placeholder "***". Use this when logging or printing the
// active configuration so secrets are never accidentally exposed.
func RedactedConfig(cfg *Config) Config {
	out := *cfg // shallow copy of the top-level struct

	// Wallet
	out.Wallet = cfg.Wallet
	redact(&out.Wallet.PrivateKey)
	redact(&out.Wallet.KeyPassword)

	// Builder
	out.Builder = cfg.Builder
	redact(&out.Builder.ApiKey)
	redact(&out.Builder.ApiSecret)
	redact(&out.Builder.ApiPassphrase)

	// Kalshi
	out.Kalshi = cfg.Kalshi
	redact(&out.Kalshi.ApiKey)

	// Supabase
	out.Supabase = cfg.Supabase
	redact(&out.Supabase.DSN)
	redact(&out.Supabase.Password)
	redact(&out.Supabase.ApiKey)

	// Redis
	out.Redis = cfg.Redis
	redact(&out.Redis.Password)

	// S3
	out.S3 = cfg.S3
	redact(&out.S3.AccessKey)
	redact(&out.S3.SecretKey)

	// Notify
	out.Notify = cfg.Notify
	redact(&out.Notify.TelegramToken)
	redact(&out.Notify.DiscordWebhookURL)

	// Copy slices so callers cannot mutate the original through the redacted
	// copy.
	if cfg.Notify.Events != nil {
		out.Notify.Events = make([]string, len(cfg.Notify.Events))
		copy(out.Notify.Events, cfg.Notify.Events)
	}
	if cfg.Server.CORSOrigins != nil {
		out.Server.CORSOrigins = make([]string, len(cfg.Server.CORSOrigins))
		copy(out.Server.CORSOrigins, cfg.Server.CORSOrigins)
	}

	// Copy maps so mutations to the redacted copy do not affect the original.
	if cfg.Strategy.Params != nil {
		out.Strategy.Params = make(map[string]any, len(cfg.Strategy.Params))
		for k, v := range cfg.Strategy.Params {
			out.Strategy.Params[k] = v
		}
	}
	if cfg.Arbitrage.PerVenueFeeBps != nil {
		out.Arbitrage.PerVenueFeeBps = make(map[string]float64, len(cfg.Arbitrage.PerVenueFeeBps))
		for k, v := range cfg.Arbitrage.PerVenueFeeBps {
			out.Arbitrage.PerVenueFeeBps[k] = v
		}
	}

	return out
}

const redacted = "***"

// redact replaces a non-empty string with the redacted placeholder.
func redact(s *string) {
	if *s != "" {
		*s = redacted
	}
}
