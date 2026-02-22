// Package app provides the top-level application lifecycle management for the
// polymarket bot. It wires together all dependencies (stores, caches, blob
// storage, services, strategies, pipelines, and notifications) and starts the
// appropriate goroutines based on the configured operating mode.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alanyoungcy/polymarketbot/internal/config"
)

// App is the root application object. It owns the configuration, logger, and a
// list of cleanup functions that are called in reverse order on shutdown.
type App struct {
	cfg     *config.Config
	logger  *slog.Logger
	closers []func()
}

// New creates a new App from the given configuration and logger.
func New(cfg *config.Config, logger *slog.Logger) *App {
	return &App{
		cfg:    cfg,
		logger: logger.With(slog.String("component", "app")),
	}
}

// Run is the main entry point. It wires all dependencies, selects the
// operating mode, starts the corresponding goroutines, and blocks until the
// context is cancelled. On return it runs all registered cleanup functions.
func (a *App) Run(ctx context.Context) error {
	a.logger.InfoContext(ctx, "starting application",
		slog.String("mode", a.cfg.Mode),
		slog.String("log_level", a.cfg.LogLevel),
	)

	deps, cleanup, err := Wire(ctx, a.cfg)
	if err != nil {
		return fmt.Errorf("app: wire dependencies: %w", err)
	}
	a.closers = append(a.closers, cleanup)

	mode := strings.ToLower(a.cfg.Mode)
	switch mode {
	case "trade":
		return a.TradeMode(ctx, deps)
	case "arbitrage":
		return a.ArbitrageMode(ctx, deps)
	case "monitor":
		return a.MonitorMode(ctx, deps)
	case "scrape":
		return a.ScrapeMode(ctx, deps)
	case "full":
		return a.FullMode(ctx, deps)
	default:
		return fmt.Errorf("app: unsupported mode %q", a.cfg.Mode)
	}
}

// Close tears down all resources in reverse registration order. It is safe to
// call multiple times; subsequent calls are no-ops.
func (a *App) Close() {
	a.logger.Info("shutting down application")
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}
