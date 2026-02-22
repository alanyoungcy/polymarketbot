package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Engine orchestrates the execution of one or more strategies. It receives
// market data events, delegates them to the active strategy (or fans out to
// all when using RunAll), and forwards any resulting trade signals to the
// signal channel consumed by the executor layer.
type Engine struct {
	registry    *Registry
	active      Strategy
	activeNames []string
	signalCh    chan<- domain.TradeSignal
	tracker     *PriceTracker
	logger      *slog.Logger

	// Multi-strategy: per-name channels for fan-out. Used when activeNames is set.
	mu       sync.Mutex
	bookChs  map[string]chan domain.OrderbookSnapshot
	priceChs map[string]chan domain.PriceChange
	tradeChs map[string]chan domain.Trade
	closed   bool

	recentSignals []domain.TradeSignal
	recentLimit   int
}

// NewEngine creates an Engine. The signalCh is the output channel where emitted
// TradeSignals are sent to the executor. The prices cache and logger are used
// to construct a shared PriceTracker with a default 5-minute window.
func NewEngine(registry *Registry, signalCh chan<- domain.TradeSignal, prices domain.PriceCache, logger *slog.Logger) *Engine {
	return &Engine{
		registry:    registry,
		signalCh:    signalCh,
		tracker:     NewPriceTracker(prices, 5*time.Minute),
		logger:      logger.With(slog.String("component", "strategy_engine")),
		recentLimit: 500,
	}
}

// ActiveName returns the current active strategy name (single-strategy mode)
// or a comma-separated list (multi-strategy mode). Empty if none set.
func (e *Engine) ActiveName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active != nil {
		return e.active.Name()
	}
	if len(e.activeNames) > 0 {
		return strings.Join(e.activeNames, ",")
	}
	return ""
}

// ListNames returns the names of all registered strategies in sorted order.
func (e *Engine) ListNames() []string {
	return e.registry.List()
}

// RecentSignals returns up to limit most recent emitted signals in reverse
// chronological order (newest first).
func (e *Engine) RecentSignals(limit int) []domain.TradeSignal {
	if limit <= 0 {
		limit = 20
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.recentSignals)
	if n == 0 {
		return []domain.TradeSignal{}
	}
	if limit > n {
		limit = n
	}
	out := make([]domain.TradeSignal, 0, limit)
	for i := n - 1; i >= 0 && len(out) < limit; i-- {
		sig := e.recentSignals[i]
		if sig.Metadata != nil {
			meta := make(map[string]string, len(sig.Metadata))
			for k, v := range sig.Metadata {
				meta[k] = v
			}
			sig.Metadata = meta
		}
		out = append(out, sig)
	}
	return out
}

// SetActive switches the active strategy to the one registered under name (single-strategy mode).
// It returns an error if the name is not found in the registry.
func (e *Engine) SetActive(name string) error {
	e.mu.Lock()
	e.activeNames = nil
	e.mu.Unlock()
	s, err := e.registry.Get(name)
	if err != nil {
		return fmt.Errorf("set active strategy: %w", err)
	}
	e.active = s
	e.logger.Info("active strategy changed", slog.String("strategy", name))
	return nil
}

// SetActiveNames enables multi-strategy mode: all listed strategies will receive
// events when RunAll is used. Names must be registered in the registry.
func (e *Engine) SetActiveNames(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("active names cannot be empty")
	}
	for _, name := range names {
		if _, err := e.registry.Get(name); err != nil {
			return fmt.Errorf("strategy %q: %w", name, err)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	// Close existing channels if any
	e.closeStrategyChannelsLocked()
	e.active = nil
	e.activeNames = names
	buf := 32
	e.bookChs = make(map[string]chan domain.OrderbookSnapshot, len(names))
	e.priceChs = make(map[string]chan domain.PriceChange, len(names))
	e.tradeChs = make(map[string]chan domain.Trade, len(names))
	for _, name := range names {
		e.bookChs[name] = make(chan domain.OrderbookSnapshot, buf)
		e.priceChs[name] = make(chan domain.PriceChange, buf)
		e.tradeChs[name] = make(chan domain.Trade, buf)
	}
	e.closed = false
	e.logger.Info("active strategies set", slog.Any("strategies", names))
	return nil
}

func (e *Engine) closeStrategyChannelsLocked() {
	for _, ch := range e.bookChs {
		close(ch)
	}
	for _, ch := range e.priceChs {
		close(ch)
	}
	for _, ch := range e.tradeChs {
		close(ch)
	}
	e.bookChs = nil
	e.priceChs = nil
	e.tradeChs = nil
}

// HandleBookUpdate feeds an orderbook snapshot to the active strategy (or all active when using RunAll) and emits any resulting signals.
func (e *Engine) HandleBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) error {
	e.mu.Lock()
	names := e.activeNames
	bookChs := e.bookChs
	active := e.active
	e.mu.Unlock()

	if len(names) > 0 && bookChs != nil {
		for _, name := range names {
			if ch, ok := bookChs[name]; ok {
				select {
				case ch <- snap:
				case <-ctx.Done():
					return ctx.Err()
				default:
					// Buffer full, skip this update for this strategy
				}
			}
		}
		return nil
	}
	if active == nil {
		return fmt.Errorf("no active strategy set")
	}
	signals, err := active.OnBookUpdate(ctx, snap)
	if err != nil {
		return fmt.Errorf("strategy %s OnBookUpdate: %w", active.Name(), err)
	}
	e.emit(ctx, signals)
	return nil
}

// HandlePriceChange feeds an incremental price change to the active strategy or all.
func (e *Engine) HandlePriceChange(ctx context.Context, change domain.PriceChange) error {
	e.mu.Lock()
	names := e.activeNames
	priceChs := e.priceChs
	active := e.active
	e.mu.Unlock()

	if len(names) > 0 && priceChs != nil {
		for _, name := range names {
			if ch, ok := priceChs[name]; ok {
				select {
				case ch <- change:
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
		}
		return nil
	}
	if active == nil {
		return fmt.Errorf("no active strategy set")
	}
	signals, err := active.OnPriceChange(ctx, change)
	if err != nil {
		return fmt.Errorf("strategy %s OnPriceChange: %w", active.Name(), err)
	}
	e.emit(ctx, signals)
	return nil
}

// HandleTrade feeds a trade event to the active strategy or all.
func (e *Engine) HandleTrade(ctx context.Context, trade domain.Trade) error {
	e.mu.Lock()
	names := e.activeNames
	tradeChs := e.tradeChs
	active := e.active
	e.mu.Unlock()

	if len(names) > 0 && tradeChs != nil {
		for _, name := range names {
			if ch, ok := tradeChs[name]; ok {
				select {
				case ch <- trade:
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
		}
		return nil
	}
	if active == nil {
		return fmt.Errorf("no active strategy set")
	}
	signals, err := active.OnTrade(ctx, trade)
	if err != nil {
		return fmt.Errorf("strategy %s OnTrade: %w", active.Name(), err)
	}
	e.emit(ctx, signals)
	return nil
}

// runStrategy runs a single strategy in a loop, reading from its channels and emitting signals.
func (e *Engine) runStrategy(ctx context.Context, name string) error {
	strat, err := e.registry.Get(name)
	if err != nil {
		return err
	}
	if err := strat.Init(ctx); err != nil {
		e.logger.Error("strategy init failed", slog.String("strategy", name), slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = strat.Close() }()

	e.mu.Lock()
	bookCh := e.bookChs[name]
	priceCh := e.priceChs[name]
	tradeCh := e.tradeChs[name]
	e.mu.Unlock()
	if bookCh == nil || priceCh == nil || tradeCh == nil {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snap, ok := <-bookCh:
			if !ok {
				return nil
			}
			signals, err := strat.OnBookUpdate(ctx, snap)
			if err != nil {
				e.logger.Warn("strategy OnBookUpdate error", slog.String("strategy", name), slog.String("error", err.Error()))
				continue
			}
			e.emit(ctx, signals)
		case change, ok := <-priceCh:
			if !ok {
				return nil
			}
			signals, err := strat.OnPriceChange(ctx, change)
			if err != nil {
				e.logger.Warn("strategy OnPriceChange error", slog.String("strategy", name), slog.String("error", err.Error()))
				continue
			}
			e.emit(ctx, signals)
		case trade, ok := <-tradeCh:
			if !ok {
				return nil
			}
			signals, err := strat.OnTrade(ctx, trade)
			if err != nil {
				e.logger.Warn("strategy OnTrade error", slog.String("strategy", name), slog.String("error", err.Error()))
				continue
			}
			e.emit(ctx, signals)
		}
	}
}

// Run starts the engine's main loop (single-strategy mode). It blocks until the context is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	e.logger.Info("strategy engine started")
	defer e.logger.Info("strategy engine stopped")
	<-ctx.Done()
	return ctx.Err()
}

// RunAll starts one goroutine per enabled strategy. Each strategy receives events via its channels and emits to the shared signalCh. Blocks until context is cancelled.
func (e *Engine) RunAll(ctx context.Context) error {
	e.mu.Lock()
	names := make([]string, len(e.activeNames))
	copy(names, e.activeNames)
	e.mu.Unlock()
	if len(names) == 0 {
		e.logger.Info("RunAll: no active strategies, blocking until context done")
		<-ctx.Done()
		return ctx.Err()
	}

	e.logger.Info("strategy engine RunAll started", slog.Any("strategies", names))
	defer func() {
		e.mu.Lock()
		e.closeStrategyChannelsLocked()
		e.closed = true
		e.mu.Unlock()
		e.logger.Info("strategy engine RunAll stopped")
	}()

	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		name := name
		g.Go(func() error {
			return e.runStrategy(gctx, name)
		})
	}
	return g.Wait()
}

// emit sends each signal to the signal channel. It respects context cancellation.
func (e *Engine) emit(ctx context.Context, signals []domain.TradeSignal) {
	for i := range signals {
		select {
		case <-ctx.Done():
			e.logger.Warn("context cancelled while emitting signals",
				slog.Int("remaining", len(signals)-i),
			)
			return
		case e.signalCh <- signals[i]:
			e.rememberSignal(signals[i])
			e.logger.Debug("signal emitted",
				slog.String("signal_id", signals[i].ID),
				slog.String("source", signals[i].Source),
				slog.String("side", string(signals[i].Side)),
			)
		}
	}
}

func (e *Engine) rememberSignal(sig domain.TradeSignal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recentSignals = append(e.recentSignals, sig)
	if overflow := len(e.recentSignals) - e.recentLimit; overflow > 0 {
		e.recentSignals = append([]domain.TradeSignal(nil), e.recentSignals[overflow:]...)
	}
}
