package strategy

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// StrategyInfo holds runtime info for a registered strategy (for status APIs).
type StrategyInfo struct {
	Name        string
	Status      string     // "pending", "running", "stopped", "error"
	SignalsSent int64
	LastSignal  *time.Time
	ErrorCount  int64
}

// Registry manages a named collection of strategies that can be looked up at
// runtime. It is safe for concurrent use.
type Registry struct {
	strategies map[string]Strategy
	mu         sync.RWMutex
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		strategies: make(map[string]Strategy),
	}
}

// Register adds a strategy to the registry under the given name.
// If a strategy with the same name already exists it will be replaced.
func (r *Registry) Register(name string, s Strategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strategies[name] = s
}

// Get retrieves a strategy by name. It returns an error when the name is not
// registered.
func (r *Registry) Get(name string) (Strategy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.strategies[name]
	if !ok {
		return nil, fmt.Errorf("strategy %q: not registered", name)
	}
	return s, nil
}

// List returns the names of all registered strategies in sorted order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.strategies))
	for n := range r.strategies {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ListInfo returns runtime info for all registered strategies. Status is "pending"
// until the strategy is run by the engine (engine may update via SetStrategyInfo).
func (r *Registry) ListInfo() []StrategyInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.strategies))
	for n := range r.strategies {
		names = append(names, n)
	}
	sort.Strings(names)
	infos := make([]StrategyInfo, 0, len(names))
	for _, n := range names {
		infos = append(infos, StrategyInfo{Name: n, Status: "pending"})
	}
	return infos
}
