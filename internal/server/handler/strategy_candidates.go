package handler

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// StrategySignalProvider provides recent emitted strategy signals.
type StrategySignalProvider interface {
	RecentSignals(limit int) []domain.TradeSignal
}

// StrategyActiveProvider exposes the currently active strategy name.
type StrategyActiveProvider interface {
	ActiveName() string
}

// StrategyCandidateMarketResolver resolves market metadata for candidate output.
type StrategyCandidateMarketResolver interface {
	GetMarket(ctx context.Context, id string) (domain.Market, error)
}

// StrategyCandidate is a UI-facing, ranked signal candidate.
type StrategyCandidate struct {
	SignalID       string               `json:"signal_id"`
	Strategy       string               `json:"strategy"`
	MarketID       string               `json:"market_id"`
	MarketQuestion string               `json:"market_question,omitempty"`
	TokenID        string               `json:"token_id"`
	Side           domain.OrderSide     `json:"side"`
	Price          float64              `json:"price"`
	Size           float64              `json:"size"`
	Urgency        domain.SignalUrgency `json:"urgency"`
	Reason         string               `json:"reason"`
	CreatedAt      time.Time            `json:"created_at"`
	ExpiresAt      time.Time            `json:"expires_at"`
	Score          float64              `json:"score"`
}

type strategyCandidatesResponse struct {
	ActiveStrategy string              `json:"active_strategy"`
	AutoExecute    bool                `json:"auto_execute"`
	Candidates     []StrategyCandidate `json:"candidates"`
	Best           *StrategyCandidate  `json:"best,omitempty"`
}

// StrategyCandidatesHandler serves candidate signal discovery for manual bets.
type StrategyCandidatesHandler struct {
	signals     StrategySignalProvider
	active      StrategyActiveProvider
	markets     StrategyCandidateMarketResolver
	autoExecute bool
	logger      *slog.Logger
}

// NewStrategyCandidatesHandler creates a new candidate handler.
func NewStrategyCandidatesHandler(
	signals StrategySignalProvider,
	active StrategyActiveProvider,
	markets StrategyCandidateMarketResolver,
	autoExecute bool,
	logger *slog.Logger,
) *StrategyCandidatesHandler {
	return &StrategyCandidatesHandler{
		signals:     signals,
		active:      active,
		markets:     markets,
		autoExecute: autoExecute,
		logger:      logger,
	}
}

// ListCandidates returns ranked strategy candidates.
// GET /api/strategy/candidates?limit=20&source=rebalancing_arb
func (h *StrategyCandidatesHandler) ListCandidates(w http.ResponseWriter, r *http.Request) {
	if h.signals == nil {
		writeError(w, http.StatusNotImplemented, "strategy candidates not available in this mode")
		return
	}

	limit := 20
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	active := ""
	if h.active != nil {
		active = strings.TrimSpace(h.active.ActiveName())
	}

	filteredSources := parseSources(strings.TrimSpace(r.URL.Query().Get("source")))
	if len(filteredSources) == 0 {
		filteredSources = parseSources(active)
	}

	signalLimit := limit * 10
	if signalLimit < 50 {
		signalLimit = 50
	}
	signals := h.signals.RecentSignals(signalLimit)

	now := time.Now().UTC()
	candidates := make([]StrategyCandidate, 0, len(signals))
	questions := map[string]string{}
	for _, sig := range signals {
		if len(filteredSources) > 0 {
			if _, ok := filteredSources[sig.Source]; !ok {
				continue
			}
		}
		if !sig.ExpiresAt.IsZero() && now.After(sig.ExpiresAt) {
			continue
		}
		c := StrategyCandidate{
			SignalID:  sig.ID,
			Strategy:  sig.Source,
			MarketID:  sig.MarketID,
			TokenID:   sig.TokenID,
			Side:      sig.Side,
			Price:     sig.Price(),
			Size:      sig.Size(),
			Urgency:   sig.Urgency,
			Reason:    sig.Reason,
			CreatedAt: sig.CreatedAt,
			ExpiresAt: sig.ExpiresAt,
			Score:     scoreCandidate(sig, now),
		}
		if c.MarketID != "" && h.markets != nil {
			if q, ok := questions[c.MarketID]; ok {
				c.MarketQuestion = q
			} else {
				mkt, err := h.markets.GetMarket(r.Context(), c.MarketID)
				if err == nil {
					c.MarketQuestion = mkt.Question
					questions[c.MarketID] = mkt.Question
				} else {
					h.logger.DebugContext(r.Context(), "strategy candidates: market lookup failed",
						slog.String("market_id", c.MarketID),
						slog.String("error", err.Error()),
					)
				}
			}
		}
		candidates = append(candidates, c)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].CreatedAt.After(candidates[j].CreatedAt)
		}
		return candidates[i].Score > candidates[j].Score
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	var best *StrategyCandidate
	if len(candidates) > 0 {
		v := candidates[0]
		best = &v
	}

	writeJSON(w, http.StatusOK, strategyCandidatesResponse{
		ActiveStrategy: active,
		AutoExecute:    h.autoExecute,
		Candidates:     candidates,
		Best:           best,
	})
}

func parseSources(v string) map[string]struct{} {
	out := map[string]struct{}{}
	if strings.TrimSpace(v) == "" {
		return out
	}
	parts := strings.Split(v, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func scoreCandidate(sig domain.TradeSignal, now time.Time) float64 {
	urgencyWeight := float64(sig.Urgency) * 100.0
	ageSec := now.Sub(sig.CreatedAt).Seconds()
	if ageSec < 0 {
		ageSec = 0
	}
	freshness := 60.0 - ageSec
	if freshness < 0 {
		freshness = 0
	}
	ttlBoost := 0.0
	if !sig.ExpiresAt.IsZero() {
		remaining := sig.ExpiresAt.Sub(now).Seconds()
		if remaining > 0 {
			ttlBoost = remaining / 10.0
		}
	}
	return urgencyWeight + freshness + ttlBoost
}
