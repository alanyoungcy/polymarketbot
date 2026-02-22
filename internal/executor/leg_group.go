package executor

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PendingLegGroup holds signals that share a leg_group_id until complete or timeout.
type PendingLegGroup struct {
	LegGroupID string
	Legs       []domain.TradeSignal
	Expected   int
	Policy     domain.LegPolicy
	FirstSeen  time.Time
	timer      *time.Timer
}

// LegGroupAccumulator buffers multi-leg signals and invokes a callback when
// the group is complete or times out.
type LegGroupAccumulator struct {
	mu        sync.Mutex
	groups    map[string]*PendingLegGroup
	maxGapMs  int64
	onComplete func(ctx context.Context, legs []domain.TradeSignal, policy domain.LegPolicy) error
	logger    *slog.Logger
}

// NewLegGroupAccumulator creates an accumulator. maxGapMs is the maximum time
// allowed between first and last leg; when exceeded the group is discarded.
func NewLegGroupAccumulator(
	maxGapMs int64,
	onComplete func(ctx context.Context, legs []domain.TradeSignal, policy domain.LegPolicy) error,
	logger *slog.Logger,
) *LegGroupAccumulator {
	return &LegGroupAccumulator{
		groups:    make(map[string]*PendingLegGroup),
		maxGapMs:  maxGapMs,
		onComplete: onComplete,
		logger:    logger.With(slog.String("component", "leg_accumulator")),
	}
}

// Add adds a signal to its leg group. If the group reaches expected count,
// onComplete is called and the group is removed. Returns true if the signal
// was part of a completed group (caller should not place single-leg).
func (a *LegGroupAccumulator) Add(ctx context.Context, sig domain.TradeSignal) (completed bool) {
	legGroupID, ok := sig.Metadata["leg_group_id"]
	if !ok || legGroupID == "" {
		return false
	}
	expectedStr := sig.Metadata["leg_count"]
	expected := 1
	if expectedStr != "" {
		if n, err := strconv.Atoi(expectedStr); err == nil && n > 0 {
			expected = n
		}
	}
	policy := domain.LegPolicyBestEffort
	if p := sig.Metadata["leg_policy"]; p != "" {
		policy = domain.LegPolicy(p)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	g, exists := a.groups[legGroupID]
	if !exists {
		g = &PendingLegGroup{
			LegGroupID: legGroupID,
			Expected:   expected,
			Policy:     policy,
			FirstSeen:  time.Now().UTC(),
		}
		g.timer = time.AfterFunc(time.Duration(a.maxGapMs)*time.Millisecond, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if _, ok := a.groups[legGroupID]; ok {
				delete(a.groups, legGroupID)
				a.logger.Warn("leg group timed out",
					slog.String("leg_group_id", legGroupID),
					slog.Int("received", len(g.Legs)),
					slog.Int("expected", expected),
				)
			}
		})
		a.groups[legGroupID] = g
	}

	g.Legs = append(g.Legs, sig)
	if len(g.Legs) < g.Expected {
		return true
	}

	g.timer.Stop()
	delete(a.groups, legGroupID)
	legs := make([]domain.TradeSignal, len(g.Legs))
	copy(legs, g.Legs)
	a.mu.Unlock()
	err := a.onComplete(ctx, legs, g.Policy)
	a.mu.Lock()
	if err != nil {
		a.logger.Error("leg group onComplete failed",
			slog.String("leg_group_id", legGroupID),
			slog.String("error", err.Error()),
		)
	}
	return true
}
