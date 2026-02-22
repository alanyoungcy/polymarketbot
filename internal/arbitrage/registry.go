package arbitrage

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds named arbitrage strategies for selection by config.
type Registry struct {
	strategies map[string]Strategy
	mu         sync.RWMutex
}

// NewRegistry returns an empty registry. Call Register to add strategies.
func NewRegistry() *Registry {
	return &Registry{strategies: make(map[string]Strategy)}
}

// Register adds a strategy under the given name.
func (r *Registry) Register(name string, s Strategy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.strategies[name] = s
}

// Get returns the strategy by name, or an error if not found.
func (r *Registry) Get(name string) (Strategy, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.strategies[name]
	if !ok {
		return nil, fmt.Errorf("arbitrage strategy %q not found", name)
	}
	return s, nil
}

// List returns all registered strategy names, sorted.
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
