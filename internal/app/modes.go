package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alanyoungcy/polymarketbot/internal/arbitrage"
	"github.com/alanyoungcy/polymarketbot/internal/config"
	"github.com/alanyoungcy/polymarketbot/internal/crypto"
	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/executor"
	"github.com/alanyoungcy/polymarketbot/internal/feed"
	"github.com/alanyoungcy/polymarketbot/internal/pipeline"
	"github.com/alanyoungcy/polymarketbot/internal/platform/goldsky"
	"github.com/alanyoungcy/polymarketbot/internal/platform/kalshi"
	"github.com/alanyoungcy/polymarketbot/internal/platform/polymarket"
	"github.com/alanyoungcy/polymarketbot/internal/server/handler"
	"github.com/alanyoungcy/polymarketbot/internal/server/middleware"
	"github.com/alanyoungcy/polymarketbot/internal/server/ws"
	"github.com/alanyoungcy/polymarketbot/internal/service"
	"github.com/alanyoungcy/polymarketbot/internal/strategy"
)

// strategyDeps holds optional services for new strategies (built in Trade/Full mode when stores exist).
type strategyDeps struct {
	relationSvc    *service.RelationService
	rewardsTracker *service.RewardsTracker
	gammaClient    *polymarket.GammaClient
	kalshiClient   *kalshi.Client
}

// TradeMode starts the strategy engine, price service, order execution, and
// position monitoring goroutines.
func (a *App) TradeMode(ctx context.Context, deps *Dependencies) error {
	a.logger.InfoContext(ctx, "starting trade mode")

	g, ctx := errgroup.WithContext(ctx)

	// Build services.
	priceSvc := service.NewPriceService(deps.PriceCache, deps.BookCache, deps.SignalBus, a.logger)
	positionSvc := service.NewPositionService(
		deps.PositionStore, deps.PriceCache, deps.SignalBus, deps.AuditStore, a.logger,
	)
	_ = positionSvc
	marketSvc := service.NewMarketService(deps.MarketStore, deps.MarketCache, deps.SignalBus, a.logger)
	_ = marketSvc

	// Strategy engine.
	signalCh := make(chan domain.TradeSignal, 32)
	sd := a.buildStrategyDeps(deps)
	reg := a.newStrategyRegistry(deps, sd)
	engine := strategy.NewEngine(reg, signalCh, deps.PriceCache, a.logger)
	if len(a.cfg.Strategy.Active) > 0 {
		if err := engine.SetActiveNames(a.cfg.Strategy.Active); err != nil {
			a.logger.WarnContext(ctx, "failed to set active strategies, engine will idle",
				slog.Any("active", a.cfg.Strategy.Active),
				slog.String("error", err.Error()),
			)
		} else {
			g.Go(func() error {
				return engine.RunAll(ctx)
			})
		}
	} else {
		if err := engine.SetActive(a.cfg.Strategy.Name); err != nil {
			a.logger.WarnContext(ctx, "failed to set active strategy, engine will idle",
				slog.String("strategy", a.cfg.Strategy.Name),
				slog.String("error", err.Error()),
			)
		} else {
			g.Go(func() error {
				return engine.Run(ctx)
			})
		}
	}

	// Engine feeder: subscribe to "prices" and feed engine (so strategies get events from Redis).
	engineFeeder := feed.NewEngineFeeder(deps.SignalBus, deps.BookCache, engine, a.logger)
	g.Go(func() error {
		return engineFeeder.Run(ctx)
	})

	// Polymarket WS feed: push book/price into PriceService and engine (produces "prices" events).
	if deps.MarketStore != nil && a.cfg.Polymarket.WsHost != "" {
		assetIDs := a.watchAssetIDs(ctx, deps.MarketStore, 100)
		if len(assetIDs) > 0 {
			wsFeed := feed.NewPolymarketWSFeed(
				a.cfg.Polymarket.WsHost,
				assetIDs,
				func(ctx context.Context, snap domain.OrderbookSnapshot) {
					_ = priceSvc.HandleBookUpdate(ctx, snap)
					_ = engine.HandleBookUpdate(ctx, snap)
				},
				func(ctx context.Context, change domain.PriceChange) {
					_ = priceSvc.HandlePriceChange(ctx, change)
					_ = engine.HandlePriceChange(ctx, change)
				},
				a.logger,
			)
			g.Go(func() error {
				defer wsFeed.Close()
				return wsFeed.Run(ctx)
			})
		}
	}

	// BondTracker: poll open bond positions and update on resolution.
	if deps.BondPositionStore != nil && sd != nil && sd.gammaClient != nil {
		bondTracker := service.NewBondTracker(deps.BondPositionStore, sd.gammaClient, deps.SignalBus, 2*time.Minute, a.logger)
		g.Go(func() error {
			return bondTracker.Run(ctx)
		})
	}

	if !a.cfg.Strategy.AutoExecute {
		a.logger.InfoContext(ctx, "strategy.auto_execute is false; bot will scan and publish candidates only")
		g.Go(func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case _, ok := <-signalCh:
					if !ok {
						return nil
					}
				}
			}
		})
	} else {
		// Executor: reads signals and places orders through the full execution pipeline.
		exec, execErr := a.buildExecutor(ctx, deps, signalCh, sd)
		if execErr != nil {
			a.logger.WarnContext(ctx, "trade mode: executor build failed, falling back to log-only",
				slog.String("error", execErr.Error()),
			)
			g.Go(func() error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case sig, ok := <-signalCh:
						if !ok {
							return nil
						}
						a.logger.InfoContext(ctx, "trade signal received (no executor)",
							slog.String("signal_id", sig.ID),
							slog.String("source", sig.Source),
							slog.String("side", string(sig.Side)),
						)
					}
				}
			})
		} else {
			g.Go(func() error {
				return exec.Run(ctx)
			})
		}
	}

	// Relation discovery (one-shot).
	if sd != nil && sd.relationSvc != nil {
		go func() {
			if err := sd.relationSvc.DiscoverRelations(ctx); err != nil {
				a.logger.WarnContext(ctx, "trade mode: relation discovery failed",
					slog.String("error", err.Error()),
				)
			}
		}()
	}

	// HTTP server if enabled.
	if a.cfg.Server.Enabled {
		a.startHTTPServer(ctx, g, deps, nil, engine, engine)
	}

	return g.Wait()
}

// ArbitrageMode starts the selected arbitrage strategy detector and the HTTP server.
func (a *App) ArbitrageMode(ctx context.Context, deps *Dependencies) error {
	a.logger.InfoContext(ctx, "starting arbitrage mode",
		slog.String("strategy", a.cfg.Arbitrage.Strategy),
	)

	g, ctx := errgroup.WithContext(ctx)

	arbCfg := service.ArbConfig{
		MinNetEdgeBps:       a.cfg.Arbitrage.MinNetEdgeBps,
		MaxTradeAmount:      a.cfg.Arbitrage.MaxTradeAmount,
		MinDurationMs:       a.cfg.Arbitrage.MinDurationMs,
		MaxLegGapMs:         a.cfg.Arbitrage.MaxLegGapMs,
		MaxUnhedgedNotional: a.cfg.Arbitrage.MaxUnhedgedNotional,
		KillSwitchLossUSD:   a.cfg.Arbitrage.KillSwitchLossUSD,
		PerVenueFeeBps:      a.cfg.Arbitrage.PerVenueFeeBps,
	}
	arbSvc := service.NewArbService(deps.ArbStore, deps.SignalBus, deps.AuditStore, arbCfg, a.logger)

	arbStrategy, err := a.newArbStrategy(a.cfg.Arbitrage, a.logger)
	if err != nil {
		return fmt.Errorf("arbitrage mode: %w", err)
	}
	det := arbitrage.NewDetector(arbitrage.DetectorConfig{
		Strategy:  arbStrategy,
		ArbSvc:    arbSvc,
		BookCache: deps.BookCache,
		Logger:    a.logger,
	})
	g.Go(func() error {
		return det.Run(ctx, deps.SignalBus)
	})

	if a.cfg.Server.Enabled {
		a.startHTTPServer(ctx, g, deps, nil, nil, nil)
	}

	return g.Wait()
}

// MonitorMode starts read-only monitoring: price feeds, position tracking, and
// the HTTP server for API consumption. No orders are placed.
func (a *App) MonitorMode(ctx context.Context, deps *Dependencies) error {
	a.logger.InfoContext(ctx, "starting monitor mode")

	g, ctx := errgroup.WithContext(ctx)

	// Price feed consumer.
	g.Go(func() error {
		ch, err := deps.SignalBus.Subscribe(ctx, "price_updates")
		if err != nil {
			return fmt.Errorf("monitor mode: subscribe price_updates: %w", err)
		}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case _, ok := <-ch:
				if !ok {
					return nil
				}
			}
		}
	})

	// HTTP server is always started in monitor mode.
	a.startHTTPServer(ctx, g, deps, nil, nil, nil)

	return g.Wait()
}

// ScrapeMode starts the market scraper and trade processor pipelines.
func (a *App) ScrapeMode(ctx context.Context, deps *Dependencies) error {
	a.logger.InfoContext(ctx, "starting scrape mode")

	g, ctx := errgroup.WithContext(ctx)

	if !a.cfg.Pipeline.Enabled {
		a.logger.WarnContext(ctx, "pipeline.enabled is false, but scrape mode always runs the pipeline")
	}

	if err := a.startDataPipeline(ctx, g, deps, nil); err != nil {
		return fmt.Errorf("scrape mode: %w", err)
	}

	return g.Wait()
}

// FullMode starts all subsystems: trading, arbitrage, scraping, monitoring,
// and the HTTP server.
func (a *App) FullMode(ctx context.Context, deps *Dependencies) error {
	a.logger.InfoContext(ctx, "starting full mode")

	g, ctx := errgroup.WithContext(ctx)

	priceSvc := service.NewPriceService(deps.PriceCache, deps.BookCache, deps.SignalBus, a.logger)

	// Trade engine.
	signalCh := make(chan domain.TradeSignal, 32)
	sd := a.buildStrategyDeps(deps)
	reg := a.newStrategyRegistry(deps, sd)
	engine := strategy.NewEngine(reg, signalCh, deps.PriceCache, a.logger)
	if len(a.cfg.Strategy.Active) > 0 {
		if err := engine.SetActiveNames(a.cfg.Strategy.Active); err != nil {
			a.logger.WarnContext(ctx, "failed to set active strategies, engine will idle",
				slog.Any("active", a.cfg.Strategy.Active),
				slog.String("error", err.Error()),
			)
		} else {
			g.Go(func() error {
				return engine.RunAll(ctx)
			})
		}
	} else {
		if err := engine.SetActive(a.cfg.Strategy.Name); err != nil {
			a.logger.WarnContext(ctx, "failed to set active strategy, engine will idle",
				slog.String("strategy", a.cfg.Strategy.Name),
				slog.String("error", err.Error()),
			)
		} else {
			g.Go(func() error {
				return engine.Run(ctx)
			})
		}
	}

	// Engine feeder: subscribe to "prices" and feed engine.
	engineFeeder := feed.NewEngineFeeder(deps.SignalBus, deps.BookCache, engine, a.logger)
	g.Go(func() error {
		return engineFeeder.Run(ctx)
	})

	// Polymarket WS feed: push book/price into PriceService and engine.
	if deps.MarketStore != nil && a.cfg.Polymarket.WsHost != "" {
		assetIDs := a.watchAssetIDs(ctx, deps.MarketStore, 100)
		if len(assetIDs) > 0 {
			wsFeed := feed.NewPolymarketWSFeed(
				a.cfg.Polymarket.WsHost,
				assetIDs,
				func(ctx context.Context, snap domain.OrderbookSnapshot) {
					_ = priceSvc.HandleBookUpdate(ctx, snap)
					_ = engine.HandleBookUpdate(ctx, snap)
				},
				func(ctx context.Context, change domain.PriceChange) {
					_ = priceSvc.HandlePriceChange(ctx, change)
					_ = engine.HandlePriceChange(ctx, change)
				},
				a.logger,
			)
			g.Go(func() error {
				defer wsFeed.Close()
				return wsFeed.Run(ctx)
			})
		}
	}

	// BondTracker: poll open bond positions and update on resolution.
	if deps.BondPositionStore != nil && sd != nil && sd.gammaClient != nil {
		bondTracker := service.NewBondTracker(deps.BondPositionStore, sd.gammaClient, deps.SignalBus, 2*time.Minute, a.logger)
		g.Go(func() error {
			return bondTracker.Run(ctx)
		})
	}

	if !a.cfg.Strategy.AutoExecute {
		a.logger.InfoContext(ctx, "strategy.auto_execute is false; bot will scan and publish candidates only")
		g.Go(func() error {
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case _, ok := <-signalCh:
					if !ok {
						return nil
					}
				}
			}
		})
	} else {
		// Executor: reads signals and places orders through the full execution pipeline.
		exec, execErr := a.buildExecutor(ctx, deps, signalCh, sd)
		if execErr != nil {
			a.logger.WarnContext(ctx, "full mode: executor build failed, falling back to log-only",
				slog.String("error", execErr.Error()),
			)
			g.Go(func() error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case sig, ok := <-signalCh:
						if !ok {
							return nil
						}
						a.logger.InfoContext(ctx, "trade signal received (no executor)",
							slog.String("signal_id", sig.ID),
							slog.String("source", sig.Source),
						)
					}
				}
			})
		} else {
			g.Go(func() error {
				return exec.Run(ctx)
			})
		}
	}

	// Relation discovery (one-shot).
	if sd != nil && sd.relationSvc != nil {
		go func() {
			if err := sd.relationSvc.DiscoverRelations(ctx); err != nil {
				a.logger.WarnContext(ctx, "full mode: relation discovery failed",
					slog.String("error", err.Error()),
				)
			}
		}()
	}

	// Arb detection if enabled: run selected arbitrage strategy detector.
	if a.cfg.Arbitrage.Enabled && deps.ArbStore != nil {
		arbCfg := service.ArbConfig{
			MinNetEdgeBps:       a.cfg.Arbitrage.MinNetEdgeBps,
			MaxTradeAmount:      a.cfg.Arbitrage.MaxTradeAmount,
			MinDurationMs:       a.cfg.Arbitrage.MinDurationMs,
			MaxLegGapMs:         a.cfg.Arbitrage.MaxLegGapMs,
			MaxUnhedgedNotional: a.cfg.Arbitrage.MaxUnhedgedNotional,
			KillSwitchLossUSD:   a.cfg.Arbitrage.KillSwitchLossUSD,
			PerVenueFeeBps:      a.cfg.Arbitrage.PerVenueFeeBps,
		}
		arbSvc := service.NewArbService(deps.ArbStore, deps.SignalBus, deps.AuditStore, arbCfg, a.logger)
		arbStrategy, err := a.newArbStrategy(a.cfg.Arbitrage, a.logger)
		if err != nil {
			a.logger.WarnContext(ctx, "full mode: arb strategy disabled",
				slog.String("error", err.Error()),
			)
		} else {
			det := arbitrage.NewDetector(arbitrage.DetectorConfig{
				Strategy:  arbStrategy,
				ArbSvc:    arbSvc,
				BookCache: deps.BookCache,
				Logger:    a.logger,
			})
			g.Go(func() error {
				return det.Run(ctx, deps.SignalBus)
			})
		}
	}

	// Full mode always includes pipeline workers; trigger channel allows POST /api/pipeline/trigger to run one cycle.
	if !a.cfg.Pipeline.Enabled {
		a.logger.WarnContext(ctx, "pipeline.enabled is false, but full mode runs the pipeline by design")
	}
	pipelineTriggerCh := make(chan struct{}, 1)
	if err := a.startDataPipeline(ctx, g, deps, pipelineTriggerCh); err != nil {
		return fmt.Errorf("full mode: %w", err)
	}

	// HTTP server.
	if a.cfg.Server.Enabled {
		a.startHTTPServer(ctx, g, deps, pipelineTriggerCh, engine, engine)
	}

	return g.Wait()
}

// startHTTPServer adds an HTTP server goroutine to the given errgroup. It
// registers the WebSocket hub plus available REST handlers. The server is
// shut down gracefully when the context is cancelled.
// pipelineTriggerCh is optional; when non-nil, POST /api/pipeline/trigger will send on it to request one pipeline run.
// strategyCtrl is optional; when non-nil (trade/full mode), GET/POST /api/strategy/active and GET /api/strategy/list are registered.
// strategySignals is optional; when non-nil, GET /api/strategy/candidates returns ranked manual-bet candidates.
func (a *App) startHTTPServer(
	ctx context.Context,
	g *errgroup.Group,
	deps *Dependencies,
	pipelineTriggerCh chan<- struct{},
	strategyCtrl handler.StrategyRuntimeController,
	strategySignals handler.StrategySignalProvider,
) {
	addr := fmt.Sprintf(":%d", a.cfg.Server.Port)

	mux := http.NewServeMux()

	// Health — always available.
	health := handler.NewHealthHandler(a.logger)
	mux.HandleFunc("GET /api/health", health.HealthCheck)

	// Status — mode and strategy for dashboard (REST fallback when WS status not yet received).
	statusH := handler.NewStatusHandler(a.cfg.Mode, a.cfg.Strategy.Name)
	mux.HandleFunc("GET /api/status", statusH.GetStatus)

	// WebSocket hub — requires only Redis SignalBus.
	hub := ws.NewHub(deps.SignalBus, a.logger, ws.Config{
		Mode:         a.cfg.Mode,
		StrategyName: a.cfg.Strategy.Name,
		StartedAt:    time.Now().UTC(),
	})
	mux.HandleFunc("GET /ws", hub.HandleWS)

	g.Go(func() error {
		return hub.Run(ctx)
	})

	if strategyCtrl != nil {
		srh := handler.NewStrategyRuntimeHandler(strategyCtrl, hub, a.logger)
		mux.HandleFunc("GET /api/strategy/active", srh.GetActive)
		mux.HandleFunc("GET /api/strategy/list", srh.List)
		mux.HandleFunc("POST /api/strategy/active", srh.SetActive)
	}

	// Register store-backed handlers only when Postgres is wired.
	var marketResolver handler.StrategyCandidateMarketResolver
	if deps.MarketStore != nil {
		marketSvc := service.NewMarketService(deps.MarketStore, deps.MarketCache, deps.SignalBus, a.logger)
		marketResolver = marketSvc
		mh := handler.NewMarketHandler(marketSvc, a.logger)
		mux.HandleFunc("GET /api/markets", mh.ListMarkets)
		mux.HandleFunc("GET /api/markets/{id}", mh.GetMarket)
	}

	if strategySignals != nil {
		sc := handler.NewStrategyCandidatesHandler(
			strategySignals,
			strategyCtrl,
			marketResolver,
			a.cfg.Strategy.AutoExecute,
			a.logger,
		)
		mux.HandleFunc("GET /api/strategy/candidates", sc.ListCandidates)
	}

	if deps.OrderStore != nil && deps.PositionStore != nil {
		signer, err := crypto.NewSigner(a.cfg.Wallet.PrivateKey, a.cfg.Polymarket.ChainID)
		if err != nil {
			a.logger.WarnContext(ctx, "HTTP server: order endpoints disabled (signer unavailable)",
				slog.String("error", err.Error()),
			)
		} else {
			clobClient := polymarket.NewClobClient(a.cfg.Polymarket.ClobHost, signer, nil)
			if err := clobClient.DeriveAPIKey(ctx); err != nil {
				a.logger.WarnContext(ctx, "HTTP server: derive API key failed; order submission may fail",
					slog.String("error", err.Error()),
				)
				clobClient = nil
			}
			orderSvc := service.NewOrderService(
				deps.OrderStore, deps.PositionStore, deps.BookCache,
				deps.PriceCache, deps.RateLimiter, deps.SignalBus,
				deps.AuditStore, signer, a.logger,
			)
			if clobClient != nil {
				orderSvc.WithClobClient(clobClient)
			}
			oh := handler.NewOrderHandler(orderSvc, a.logger)
			mux.HandleFunc("GET /api/orders", oh.ListOrders)
			mux.HandleFunc("POST /api/orders", oh.PlaceOrder)
			mux.HandleFunc("DELETE /api/orders/{id}", oh.CancelOrder)
		}
	}

	if deps.ArbStore != nil {
		arbSvc := service.NewArbService(deps.ArbStore, deps.SignalBus, deps.AuditStore,
			service.ArbConfig{
				MinNetEdgeBps:     a.cfg.Arbitrage.MinNetEdgeBps,
				MaxTradeAmount:    a.cfg.Arbitrage.MaxTradeAmount,
				KillSwitchLossUSD: a.cfg.Arbitrage.KillSwitchLossUSD,
				PerVenueFeeBps:    a.cfg.Arbitrage.PerVenueFeeBps,
			}, a.logger)
		ah := handler.NewArbHandler(arbSvc, a.logger)
		if deps.ArbExecutionStore != nil {
			ah = ah.WithArbExecutionStore(deps.ArbExecutionStore)
		}
		mux.HandleFunc("GET /api/arbitrage/recent", ah.ListRecent)
		mux.HandleFunc("GET /api/arbitrage/profit", ah.Profit)
		mux.HandleFunc("GET /api/arbitrage/executions", ah.ListExecutions)
		mux.HandleFunc("GET /api/arbitrage/executions/{id}", ah.GetExecution)
	}

	// Pipeline trigger — when pipelineTriggerCh is set, trigger requests one run.
	ph := handler.NewPipelineHandler(a.logger)
	if pipelineTriggerCh != nil {
		ph = ph.WithTriggerChannel(pipelineTriggerCh)
	}
	mux.HandleFunc("POST /api/pipeline/trigger", ph.TriggerPipeline)

	// Bond handler — when BondPositionStore is wired.
	if deps.BondPositionStore != nil {
		bh := handler.NewBondHandler(deps.BondPositionStore, a.logger)
		mux.HandleFunc("GET /api/bonds", bh.ListBonds)
		mux.HandleFunc("GET /api/bonds/summary", bh.Summary)
		mux.HandleFunc("GET /api/bonds/{id}", bh.GetBond)
	}

	// Middleware chain: CORS then logging.
	var h http.Handler = mux
	if len(a.cfg.Server.CORSOrigins) > 0 {
		h = middleware.CORS(a.cfg.Server.CORSOrigins)(h)
	}
	h = middleware.Logging(a.logger)(h)

	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	g.Go(func() error {
		port := a.cfg.Server.Port
		a.logger.InfoContext(ctx, "HTTP server listening",
			slog.String("addr", addr),
			slog.Int("port", port),
			slog.String("url", fmt.Sprintf("http://localhost:%d", port)))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.logger.InfoContext(ctx, "HTTP server shutting down")
		return srv.Shutdown(shutCtx)
	})
}

func (a *App) newStrategyRegistry(deps *Dependencies, sd *strategyDeps) *strategy.Registry {
	baseParams := make(map[string]any)
	if a.cfg.Strategy.Params != nil {
		for k, v := range a.cfg.Strategy.Params {
			baseParams[k] = v
		}
	}
	baseCfg := strategy.Config{
		Name:         a.cfg.Strategy.Name,
		Coin:         a.cfg.Strategy.Coin,
		Size:         a.cfg.Strategy.Size,
		PriceScale:   a.cfg.Strategy.PriceScale,
		SizeScale:    a.cfg.Strategy.SizeScale,
		MaxPositions: a.cfg.Strategy.MaxPositions,
		TakeProfit:   a.cfg.Strategy.TakeProfit,
		StopLoss:     a.cfg.Strategy.StopLoss,
		Params:       baseParams,
	}
	prices := deps.PriceCache
	tracker := strategy.NewPriceTracker(prices, 5*time.Minute)
	reg := strategy.NewRegistry()

	reg.Register("flash_crash", strategy.NewFlashCrash(baseCfg, tracker, a.logger))
	reg.Register("mean_reversion", strategy.NewMeanReversion(baseCfg, strategy.NewPriceTracker(prices, 5*time.Minute), a.logger))
	reg.Register("arb", strategy.NewArbStrategy(baseCfg, a.logger))

	if deps.MarketStore != nil && deps.BookCache != nil && a.cfg.Strategy.YesNoSpread.Enabled {
		ynParams := mergeParams(baseParams, map[string]any{
			"min_edge_bps":  a.cfg.Strategy.YesNoSpread.MinEdgeBps,
			"size_per_leg":  a.cfg.Strategy.YesNoSpread.SizePerLeg,
			"ttl_seconds":   a.cfg.Strategy.YesNoSpread.TTLSeconds,
			"max_stale_sec": a.cfg.Strategy.YesNoSpread.MaxStaleSec,
			"cooldown_sec":  a.cfg.Strategy.YesNoSpread.CooldownSec,
		})
		reg.Register("yes_no_spread", strategy.NewYesNoSpread(
			strategy.Config{Name: baseCfg.Name, Params: ynParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.MarketStore,
			deps.BookCache,
			a.logger,
		))
	}

	if deps.ConditionGroupStore != nil && deps.MarketStore != nil {
		raParams := mergeParams(baseParams, map[string]any{
			"min_edge_bps":   a.cfg.Strategy.RebalancingArb.MinEdgeBps,
			"max_group_size": a.cfg.Strategy.RebalancingArb.MaxGroupSize,
			"size_per_leg":   a.cfg.Strategy.RebalancingArb.SizePerLeg,
			"ttl_seconds":    a.cfg.Strategy.RebalancingArb.TTLSeconds,
			"max_stale_sec":  a.cfg.Strategy.RebalancingArb.MaxStaleSec,
		})
		reg.Register("rebalancing_arb", strategy.NewRebalancingArb(
			strategy.Config{Name: baseCfg.Name, Params: raParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.ConditionGroupStore, deps.MarketStore, prices, a.logger))
	}
	if deps.BondPositionStore != nil && deps.MarketStore != nil {
		bParams := mergeParams(baseParams, map[string]any{
			"min_yes_price":     a.cfg.Strategy.Bond.MinYesPrice,
			"min_apr":           a.cfg.Strategy.Bond.MinAPR,
			"min_volume":        a.cfg.Strategy.Bond.MinVolume,
			"max_days_to_exp":   a.cfg.Strategy.Bond.MaxDaysToExp,
			"min_days_to_exp":   a.cfg.Strategy.Bond.MinDaysToExp,
			"max_positions":     a.cfg.Strategy.Bond.MaxPositions,
			"size_per_position": a.cfg.Strategy.Bond.SizePerPosition,
		})
		reg.Register("bond", strategy.NewBondStrategy(
			strategy.Config{Name: baseCfg.Name, Params: bParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.BondPositionStore, deps.MarketStore, a.logger))
	}
	var rewards strategy.RewardsTracker
	if sd != nil && sd.rewardsTracker != nil {
		rewards = sd.rewardsTracker
	}
	if deps.MarketStore != nil {
		lpParams := mergeParams(baseParams, map[string]any{
			"half_spread_bps":   a.cfg.Strategy.LiquidityProvider.HalfSpreadBps,
			"requote_threshold": a.cfg.Strategy.LiquidityProvider.RequoteThreshold,
			"size":              a.cfg.Strategy.LiquidityProvider.Size,
			"max_markets":       a.cfg.Strategy.LiquidityProvider.MaxMarkets,
		})
		reg.Register("liquidity_provider", strategy.NewLiquidityProvider(
			strategy.Config{Name: baseCfg.Name, Params: lpParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			rewards, deps.MarketStore, a.logger))
	}
	var relSvc strategy.RelationComputer
	if sd != nil && sd.relationSvc != nil {
		relSvc = sd.relationSvc
	}
	if deps.ConditionGroupStore != nil && deps.MarketRelationStore != nil && deps.MarketStore != nil {
		caParams := mergeParams(baseParams, map[string]any{
			"min_edge_bps":  a.cfg.Strategy.CombinatorialArb.MinEdgeBps,
			"max_relations": a.cfg.Strategy.CombinatorialArb.MaxRelations,
			"size_per_leg":  a.cfg.Strategy.CombinatorialArb.SizePerLeg,
		})
		reg.Register("combinatorial_arb", strategy.NewCombinatorialArb(
			strategy.Config{Name: baseCfg.Name, Params: caParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.ConditionGroupStore, deps.MarketRelationStore, relSvc,
			deps.MarketStore, prices, a.logger))
	}

	if deps.MarketStore != nil && deps.BookCache != nil && a.cfg.Strategy.CrossPlatformArb.Enabled && sd != nil && sd.kalshiClient != nil {
		cpParams := mergeParams(baseParams, map[string]any{
			"min_edge_bps":  a.cfg.Strategy.CrossPlatformArb.MinEdgeBps,
			"size_per_leg":  a.cfg.Strategy.CrossPlatformArb.SizePerLeg,
			"ttl_seconds":   a.cfg.Strategy.CrossPlatformArb.TTLSeconds,
			"refresh_sec":   a.cfg.Strategy.CrossPlatformArb.RefreshSec,
			"max_stale_sec": a.cfg.Strategy.CrossPlatformArb.MaxStaleSec,
			"cooldown_sec":  a.cfg.Strategy.CrossPlatformArb.CooldownSec,
		})
		reg.Register("cross_platform_arb", strategy.NewCrossPlatformArb(
			strategy.Config{Name: baseCfg.Name, Params: cpParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.MarketStore,
			deps.BookCache,
			sd.kalshiClient,
			a.cfg.Strategy.CrossPlatformArb.MarketMap,
			a.logger,
		))
	}

	if deps.MarketStore != nil && deps.BookCache != nil && a.cfg.Strategy.TemporalOverlap.Enabled {
		toParams := mergeParams(baseParams, map[string]any{
			"min_edge_bps":    a.cfg.Strategy.TemporalOverlap.MinEdgeBps,
			"size_per_leg":    a.cfg.Strategy.TemporalOverlap.SizePerLeg,
			"ttl_seconds":     a.cfg.Strategy.TemporalOverlap.TTLSeconds,
			"max_stale_sec":   a.cfg.Strategy.TemporalOverlap.MaxStaleSec,
			"cooldown_sec":    a.cfg.Strategy.TemporalOverlap.CooldownSec,
			"refresh_minutes": a.cfg.Strategy.TemporalOverlap.RefreshMinutes,
			"max_pairs":       a.cfg.Strategy.TemporalOverlap.MaxPairs,
		})
		reg.Register("temporal_overlap", strategy.NewTemporalOverlap(
			strategy.Config{Name: baseCfg.Name, Params: toParams},
			strategy.NewPriceTracker(prices, 5*time.Minute),
			deps.MarketStore,
			deps.BookCache,
			a.logger,
		))
	}
	return reg
}

func mergeParams(base map[string]any, overrides map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// newArbStrategy builds the arbitrage strategy registry and returns the
// strategy selected by config (e.g. "spread", "imbalance", "yes_no_spread").
func (a *App) newArbStrategy(cfg config.ArbitrageConfig, logger *slog.Logger) (arbitrage.Strategy, error) {
	reg := arbitrage.NewRegistry()
	polymarketFeeBps := 0.0
	if v, ok := cfg.PerVenueFeeBps["polymarket"]; ok {
		polymarketFeeBps = v
	}
	reg.Register("spread", arbitrage.NewSpread(arbitrage.SpreadConfig{
		MinSpreadBps:   cfg.MinSpreadBps,
		MinSize:        1.0,
		EstFeeBps:      polymarketFeeBps,
		EstSlippageBps: cfg.MaxSlippageBps,
		EstLatencyBps:  5.0,
		MaxAmount:      cfg.MaxTradeAmount,
	}, logger))
	reg.Register("imbalance", arbitrage.NewImbalance(arbitrage.ImbalanceConfig{
		RatioThreshold:  cfg.ImbalanceRatioThreshold,
		MinTotalVolume:  100.0,
		EstFeeBps:       polymarketFeeBps,
		EstSlippageBps:  cfg.MaxSlippageBps,
		EstLatencyBps:   5.0,
		MaxAmount:       cfg.MaxTradeAmount,
		EdgeBpsPerRatio: 15.0,
	}, logger))
	reg.Register("yes_no_spread", arbitrage.NewYesNoSpread(arbitrage.YesNoSpreadConfig{
		MinEdgeBps:     cfg.MinNetEdgeBps,
		EstFeeBps:      polymarketFeeBps,
		EstSlippageBps: cfg.MaxSlippageBps,
		EstLatencyBps:  5.0,
		MaxAmount:      cfg.MaxTradeAmount,
	}, logger))

	name := strings.TrimSpace(cfg.Strategy)
	if name == "" {
		name = "spread"
	}
	return reg.Get(name)
}

// watchAssetIDs returns token IDs from active markets for WS subscription (up to maxAssets).
func (a *App) watchAssetIDs(ctx context.Context, store domain.MarketStore, maxAssets int) []string {
	markets, err := store.ListActive(ctx, domain.ListOpts{Limit: 200})
	if err != nil {
		a.logger.WarnContext(ctx, "watch assets: list active failed", slog.String("error", err.Error()))
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
	for _, m := range markets {
		for _, tid := range m.TokenIDs {
			if tid == "" || seen[tid] {
				continue
			}
			seen[tid] = true
			ids = append(ids, tid)
			if len(ids) >= maxAssets {
				return ids
			}
		}
	}
	return ids
}

// buildStrategyDeps creates optional dependencies used by advanced strategies.
func (a *App) buildStrategyDeps(deps *Dependencies) *strategyDeps {
	sd := &strategyDeps{}
	if a.cfg.Polymarket.GammaHost != "" {
		sd.gammaClient = polymarket.NewGammaClient(a.cfg.Polymarket.GammaHost)
	}

	// Relation/rewards services for combinatorial_arb and liquidity_provider.
	if deps.ConditionGroupStore != nil && deps.MarketRelationStore != nil {
		sd.relationSvc = service.NewRelationService(deps.ConditionGroupStore, deps.MarketRelationStore, a.logger)
		if sd.gammaClient != nil {
			sd.rewardsTracker = service.NewRewardsTracker(sd.gammaClient, 50_000, a.logger)
		}
	}

	// Kalshi client for cross-platform strategy.
	if a.cfg.Kalshi.BaseURL != "" && a.cfg.Kalshi.ApiKey != "" && a.cfg.Kalshi.RsaPrivateKeyPath != "" {
		kc := kalshi.NewClient(a.cfg.Kalshi.BaseURL, a.cfg.Kalshi.ApiKey)
		keyBytes, err := os.ReadFile(a.cfg.Kalshi.RsaPrivateKeyPath)
		if err != nil {
			a.logger.Warn("build strategy deps: failed reading Kalshi RSA key",
				slog.String("path", a.cfg.Kalshi.RsaPrivateKeyPath),
				slog.String("error", err.Error()),
			)
		} else if err := kc.SetRSAPrivateKey(keyBytes); err != nil {
			a.logger.Warn("build strategy deps: failed parsing Kalshi RSA key",
				slog.String("path", a.cfg.Kalshi.RsaPrivateKeyPath),
				slog.String("error", err.Error()),
			)
		} else {
			sd.kalshiClient = kc
		}
	}

	return sd
}

// buildExecutor creates the full execution pipeline: signer -> clobClient ->
// orderService -> riskService -> executor. Returns the executor and any error.
func (a *App) buildExecutor(ctx context.Context, deps *Dependencies, signalCh <-chan domain.TradeSignal, sd *strategyDeps) (*executor.Executor, error) {
	signer, err := crypto.NewSigner(a.cfg.Wallet.PrivateKey, a.cfg.Polymarket.ChainID)
	if err != nil {
		return nil, fmt.Errorf("build executor: create signer: %w", err)
	}

	clobClient := polymarket.NewClobClient(a.cfg.Polymarket.ClobHost, signer, nil)
	if err := clobClient.DeriveAPIKey(ctx); err != nil {
		a.logger.WarnContext(ctx, "build executor: derive API key failed, CLOB submission disabled",
			slog.String("error", err.Error()),
		)
		clobClient = nil
	}

	orderSvc := service.NewOrderService(
		deps.OrderStore, deps.PositionStore, deps.BookCache,
		deps.PriceCache, deps.RateLimiter, deps.SignalBus,
		deps.AuditStore, signer, a.logger,
	)
	if clobClient != nil {
		orderSvc.WithClobClient(clobClient)
	}

	riskSvc := service.NewRiskService(deps.PositionStore, deps.PriceCache, service.RiskConfig{
		MaxPositions:   a.cfg.Strategy.MaxPositions,
		MaxTradeAmount: a.cfg.Arbitrage.MaxTradeAmount,
		MaxSlippageBps: a.cfg.Arbitrage.MaxSlippageBps,
	}, a.logger)

	exec := executor.NewExecutor(signalCh, orderSvc, riskSvc, signer.Address().Hex(), a.logger)

	// Enable arb execution recording if stores are available.
	if sd != nil && deps.ArbStore != nil && deps.ArbExecutionStore != nil {
		arbCfg := service.ArbConfig{
			MinNetEdgeBps:     a.cfg.Arbitrage.MinNetEdgeBps,
			MaxTradeAmount:    a.cfg.Arbitrage.MaxTradeAmount,
			KillSwitchLossUSD: a.cfg.Arbitrage.KillSwitchLossUSD,
			PerVenueFeeBps:    a.cfg.Arbitrage.PerVenueFeeBps,
		}
		arbSvc := service.NewArbService(deps.ArbStore, deps.SignalBus, deps.AuditStore, arbCfg, a.logger)
		exec.SetArbRecording(arbSvc, deps.ArbExecutionStore, a.cfg.Arbitrage.MaxLegGapMs)
	}

	return exec, nil
}

// pipelineTriggerCh is optional; when non-nil the pipeline loop also runs one cycle on receive.
func (a *App) startDataPipeline(ctx context.Context, g *errgroup.Group, deps *Dependencies, pipelineTriggerCh <-chan struct{}) error {
	if deps.MarketStore == nil || deps.TradeStore == nil || deps.AuditStore == nil {
		return fmt.Errorf("pipeline requires postgres stores (markets, trades, audit)")
	}
	if deps.BlobWriter == nil {
		return fmt.Errorf("pipeline requires blob storage writer")
	}

	interval := a.cfg.Pipeline.ScrapeInterval.Duration
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	marketSvc := service.NewMarketService(deps.MarketStore, deps.MarketCache, deps.SignalBus, a.logger)
	marketScraper := pipeline.NewMarketScraper(
		marketSvc,
		polymarket.NewGammaClient(a.cfg.Polymarket.GammaHost),
		a.logger,
	)

	g.Go(func() error {
		err := marketScraper.RunLoop(ctx, interval)
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("market scraper loop: %w", err)
	})

	// Event scraper: populate condition_groups and condition_group_markets.
	if deps.ConditionGroupStore != nil {
		gammaClient := polymarket.NewGammaClient(a.cfg.Polymarket.GammaHost)
		eventScraper := pipeline.NewEventScraper(deps.ConditionGroupStore, gammaClient, a.logger)
		g.Go(func() error {
			err := eventScraper.RunLoop(ctx, interval)
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("event scraper loop: %w", err)
		})
	}

	// Goldsky scrape + trade processing: only run when pipeline.goldsky_url is set.
	// If you don't have a Goldsky subgraph, leave it empty and the rest of the bot still runs.
	if a.cfg.Pipeline.GoldskyURL != "" {
		tradeSvc := service.NewTradeService(deps.TradeStore, deps.SignalBus, deps.AuditStore, a.logger)
		tradeProcessor := pipeline.NewTradeProcessor(tradeSvc, marketSvc, a.logger)
		goldskyScraper := pipeline.NewGoldskyScraper(
			goldsky.NewClient(a.cfg.Pipeline.GoldskyURL, a.cfg.Pipeline.GoldskyAPIKey),
			deps.BlobWriter,
			a.logger,
		)

		g.Go(func() error {
			lastTimestamp, err := tradeSvc.GetLastTimestamp(ctx)
			if err != nil {
				a.logger.WarnContext(ctx, "pipeline: failed to read last trade timestamp, defaulting to 24h lookback",
					slog.String("error", err.Error()),
				)
			}
			if lastTimestamp.IsZero() {
				lastTimestamp = time.Now().UTC().Add(-24 * time.Hour)
			}

			runOnce := func() {
				fills, scrapeErr := goldskyScraper.Run(ctx, lastTimestamp)
				if scrapeErr != nil {
					a.logger.ErrorContext(ctx, "pipeline: goldsky scrape failed", slog.String("error", scrapeErr.Error()))
					return
				}
				if len(fills) == 0 {
					return
				}

				ingested, processErr := tradeProcessor.ProcessFills(ctx, fills)
				if processErr != nil {
					a.logger.ErrorContext(ctx, "pipeline: trade processing failed", slog.String("error", processErr.Error()))
					return
				}

				lastTimestamp = latestRawFillTimestamp(fills, lastTimestamp)
				a.logger.InfoContext(ctx, "pipeline: processed goldsky fills",
					slog.Int("fills", len(fills)),
					slog.Int("trades_ingested", ingested),
					slog.Time("last_timestamp", lastTimestamp),
				)
			}

			runOnce()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				if pipelineTriggerCh != nil {
					select {
					case <-ctx.Done():
						return nil
					case <-ticker.C:
						runOnce()
					case <-pipelineTriggerCh:
						runOnce()
					}
				} else {
					select {
					case <-ctx.Done():
						return nil
					case <-ticker.C:
						runOnce()
					}
				}
			}
		})
	} else {
		a.logger.InfoContext(ctx, "pipeline: goldsky_url not set, skipping Goldsky order-fill scrape (rest of bot runs normally)")
	}

	a.logger.InfoContext(ctx, "pipeline workers started",
		slog.Duration("interval", interval),
		slog.String("gamma_host", a.cfg.Polymarket.GammaHost),
	)

	return nil
}

func latestRawFillTimestamp(fills []domain.RawFill, fallback time.Time) time.Time {
	latest := fallback
	for _, f := range fills {
		ts := time.Unix(f.Timestamp, 0)
		if ts.After(latest) {
			latest = ts
		}
	}
	return latest
}
