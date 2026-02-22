// Command polybot is the backend entry point for the polymarket bot. It loads
// configuration, validates it, wires dependencies, sets up signal handling, and
// starts the application in the configured mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alanyoungcy/polymarketbot/internal/app"
	"github.com/alanyoungcy/polymarketbot/internal/config"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to configuration file")
	flag.Parse()

	// Setup structured JSON logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config",
			slog.String("path", *configPath),
			slog.String("error", err.Error()),
		)
		os.Exit(1)
	}

	// Set log level from config.
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)

	// Validate configuration.
	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("polymarket bot starting",
		slog.String("mode", cfg.Mode),
		slog.String("config", *configPath),
	)

	// Create the application.
	application := app.New(cfg, logger)
	defer application.Close()

	// Setup signal handling for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Run the application.
	if err := application.Run(ctx); err != nil {
		// context.Canceled is expected on clean shutdown.
		if err == context.Canceled {
			logger.Info("application shut down gracefully")
		} else {
			logger.Error("application exited with error",
				slog.String("error", err.Error()),
			)
			fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
			os.Exit(1)
		}
	}

	logger.Info("polymarket bot stopped")
}
