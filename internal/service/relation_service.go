package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// RelationService discovers and manages relationships between condition groups
// and computes implied prices for combinatorial arbitrage.
type RelationService struct {
	groups   domain.ConditionGroupStore
	relations domain.MarketRelationStore
	logger   *slog.Logger
}

// NewRelationService creates a RelationService.
func NewRelationService(
	groups domain.ConditionGroupStore,
	relations domain.MarketRelationStore,
	logger *slog.Logger,
) *RelationService {
	return &RelationService{
		groups:    groups,
		relations: relations,
		logger:    logger.With(slog.String("component", "relation_service")),
	}
}

// ComputeImpliedPrices returns implied YES prices for each market in the target
// group, given the source group's market prices and the relation between the two.
// sourcePrices is keyed by source group market ID (YES price 0..1).
// Returns a map of target market ID -> implied price.
func (s *RelationService) ComputeImpliedPrices(
	ctx context.Context,
	sourceGroupID string,
	sourcePrices map[string]float64,
	targetGroupID string,
) (map[string]float64, error) {
	rels, err := s.relations.ListBySource(ctx, sourceGroupID)
	if err != nil {
		return nil, fmt.Errorf("list relations by source: %w", err)
	}
	var rel *domain.MarketRelation
	for i := range rels {
		if rels[i].TargetGroupID == targetGroupID {
			rel = &rels[i]
			break
		}
	}
	if rel == nil {
		return nil, fmt.Errorf("no relation from source group %s to target group %s", sourceGroupID, targetGroupID)
	}

	targetMarketIDs, err := s.groups.ListMarkets(ctx, targetGroupID)
	if err != nil {
		return nil, fmt.Errorf("list target group markets: %w", err)
	}
	out := make(map[string]float64, len(targetMarketIDs))

	switch rel.RelationType {
	case domain.RelationImplies:
		// Config may hold {"source_market_id": "target_market_id"} or outcome mapping.
		// Implied target price = source price * confidence when source implies target.
		if outcomeMap, ok := rel.Config["outcome_map"].(map[string]any); ok {
			for srcMid, targetVal := range outcomeMap {
				targetMid, _ := targetVal.(string)
				if targetMid == "" {
					continue
				}
				p := sourcePrices[srcMid] * rel.Confidence
				if p > 1 {
					p = 1
				}
				out[targetMid] = p
			}
		}
		// If no mapping, treat first source market as implying first target market.
		if len(out) == 0 && len(sourcePrices) > 0 && len(targetMarketIDs) > 0 {
			var firstSourcePrice float64
			for _, p := range sourcePrices {
				firstSourcePrice = p
				break
			}
			out[targetMarketIDs[0]] = firstSourcePrice * rel.Confidence
		}
	case domain.RelationExcludes:
		// Source outcome excludes target: implied target YES = (1 - source YES) * confidence.
		if outcomeMap, ok := rel.Config["outcome_map"].(map[string]any); ok {
			for srcMid, targetVal := range outcomeMap {
				targetMid, _ := targetVal.(string)
				if targetMid == "" {
					continue
				}
				p := (1 - sourcePrices[srcMid]) * rel.Confidence
				if p > 1 {
					p = 1
				}
				if p < 0 {
					p = 0
				}
				out[targetMid] = p
			}
		}
		if len(out) == 0 && len(sourcePrices) > 0 && len(targetMarketIDs) > 0 {
			var firstSourcePrice float64
			for _, p := range sourcePrices {
				firstSourcePrice = p
				break
			}
			p := (1 - firstSourcePrice) * rel.Confidence
			if p < 0 {
				p = 0
			}
			out[targetMarketIDs[0]] = p
		}
	case domain.RelationSubset:
		// Target outcomes are a subset of source; copy over matching prices.
		if outcomeMap, ok := rel.Config["outcome_map"].(map[string]any); ok {
			for srcMid, targetVal := range outcomeMap {
				targetMid, _ := targetVal.(string)
				if targetMid == "" {
					continue
				}
				p := sourcePrices[srcMid] * rel.Confidence
				if p > 1 {
					p = 1
				}
				out[targetMid] = p
			}
		}
		for _, mid := range targetMarketIDs {
			if _, ok := out[mid]; !ok {
				out[mid] = 0.5 // default when no mapping
			}
		}
	}

	// Ensure every target market has a value (default 0.5 for unknown).
	for _, mid := range targetMarketIDs {
		if _, ok := out[mid]; !ok {
			out[mid] = 0.5
		}
	}
	return out, nil
}

// DiscoverRelations examines all active condition groups and creates
// MarketRelation entries for group pairs that share keywords in their titles.
// Pairs that already have a relation are skipped.
func (s *RelationService) DiscoverRelations(ctx context.Context) error {
	groups, err := s.groups.List(ctx)
	if err != nil {
		return fmt.Errorf("relation_service: list groups: %w", err)
	}

	// Filter to active groups only and tokenize titles.
	type groupTokens struct {
		group  domain.ConditionGroup
		tokens map[string]bool
	}
	var active []groupTokens
	for _, g := range groups {
		if g.Status != "active" {
			continue
		}
		tokens := tokenize(g.Title)
		if len(tokens) == 0 {
			continue
		}
		active = append(active, groupTokens{group: g, tokens: tokens})
	}

	// Build set of existing relations for fast lookup.
	existingRels, err := s.relations.List(ctx)
	if err != nil {
		return fmt.Errorf("relation_service: list relations: %w", err)
	}
	existingSet := make(map[string]bool, len(existingRels))
	for _, r := range existingRels {
		existingSet[r.SourceGroupID+":"+r.TargetGroupID] = true
		existingSet[r.TargetGroupID+":"+r.SourceGroupID] = true
	}

	discovered := 0
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			a := active[i]
			b := active[j]

			// Skip if relation already exists.
			key := a.group.ID + ":" + b.group.ID
			if existingSet[key] {
				continue
			}

			// Count shared keywords.
			shared := 0
			for tok := range a.tokens {
				if b.tokens[tok] {
					shared++
				}
			}

			if shared >= 2 {
				rel := domain.MarketRelation{
					ID:            uuid.New().String(),
					SourceGroupID: a.group.ID,
					TargetGroupID: b.group.ID,
					RelationType:  domain.RelationImplies,
					Confidence:    0.5,
					Config:        map[string]any{},
					CreatedAt:     time.Now().UTC(),
				}
				if err := s.relations.Create(ctx, rel); err != nil {
					s.logger.Warn("relation_service: create relation failed",
						slog.String("source", a.group.ID),
						slog.String("target", b.group.ID),
						slog.String("error", err.Error()),
					)
					continue
				}
				existingSet[key] = true
				existingSet[b.group.ID+":"+a.group.ID] = true
				discovered++
				s.logger.Info("relation_service: discovered relation",
					slog.String("source", a.group.Title),
					slog.String("target", b.group.Title),
					slog.Int("shared_keywords", shared),
				)
			}
		}
	}

	s.logger.Info("relation_service: discovery complete",
		slog.Int("groups_checked", len(active)),
		slog.Int("relations_discovered", discovered),
	)
	return nil
}

// tokenize splits a title into lowercased keywords, filtering out short stop words.
func tokenize(title string) map[string]bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"of": true, "in": true, "to": true, "for": true, "is": true,
		"on": true, "at": true, "by": true, "be": true, "it": true,
		"will": true, "vs": true, "with": true, "this": true, "that": true,
	}
	tokens := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(title)) {
		// Remove common punctuation.
		word = strings.Trim(word, ".,!?;:\"'()-")
		if len(word) < 3 || stopWords[word] {
			continue
		}
		tokens[word] = true
	}
	return tokens
}
