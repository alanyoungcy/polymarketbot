package domain

import "time"

// RelationType describes how two condition groups are related.
type RelationType string

const (
	RelationImplies  RelationType = "implies"  // A winning implies B winning
	RelationExcludes RelationType = "excludes" // A winning excludes B winning
	RelationSubset   RelationType = "subset"   // A outcomes are a subset of B outcomes
)

// MarketRelation links two condition groups for combinatorial arbitrage.
type MarketRelation struct {
	ID            string
	SourceGroupID string
	TargetGroupID string
	RelationType  RelationType
	Confidence    float64 // 0.0–1.0
	Config        map[string]any
	CreatedAt     time.Time
}
