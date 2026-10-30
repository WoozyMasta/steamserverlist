// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	// maxDiscoveryPlanBytes bounds JSON parsing work for persisted plans.
	maxDiscoveryPlanBytes = 1 << 20
	// maxDiscoveryDepth bounds recursive traversal of an untrusted plan tree.
	maxDiscoveryDepth = 64
	// maxDiscoveryQueries caps executable leaves in one plan.
	maxDiscoveryQueries = 256
)

// discoveryKnownReasons is the mask accepted in persisted plan leaves.
const discoveryKnownReasons = DiscoveryReasonRequestBudget |
	DiscoveryReasonPlanQueryBudget |
	DiscoveryReasonBufferBudget |
	DiscoveryReasonDepthLimit |
	DiscoveryReasonNoUsefulSplit |
	DiscoveryReasonSaturated |
	DiscoveryReasonInconsistent |
	DiscoveryReasonUnverifiedSemantics |
	DiscoveryReasonRequestFailure

// DiscoveryPlan stores the root and its binary partitions, without server data.
// The zero value is invalid. This unreleased format has no compatibility layer.
type DiscoveryPlan struct {
	// tree stores the reusable binary partition structure.
	tree *discoveryPlanNode
	// root is the original validated filter shared by every leaf.
	root QueryFilter
	// limit is the per-request response cap.
	limit uint32
}

// discoveryPlanNode represents either an active query leaf or a binary split.
type discoveryPlanNode struct {
	// Match applies the positive split predicate.
	Match *discoveryPlanNode `json:"match,omitempty"`
	// NonMatch applies the split's NOR complement.
	NonMatch *discoveryPlanNode `json:"non_match,omitempty"`
	// Split is the predicate encoded into both child filters.
	Split *Predicate `json:"split,omitempty"`
	// Reasons records why an active leaf remains unresolved.
	Reasons DiscoveryReasons `json:"reasons,omitempty"`
	// Active distinguishes a query leaf from an internal split node.
	Active bool `json:"active,omitempty"`
}

// discoveryPlanJSON is the validated JSON wire representation of DiscoveryPlan.
type discoveryPlanJSON struct {
	// Tree stores the partition hierarchy.
	Tree *discoveryPlanNode `json:"tree"`
	// Root stores the initial filter expression.
	Root queryFilterWire `json:"root"`
	// Limit applies to every derived query.
	Limit uint32 `json:"limit"`
}

// queryFilterWire preserves QueryFilter data without exposing its private fields.
type queryFilterWire struct {
	// Group is the optional flat boolean group.
	Group *PredicateGroup `json:"group,omitempty"`
	// Conditions are the required conjunction.
	Conditions []Predicate `json:"conditions,omitempty"`
}

// MarshalJSON serializes a valid plan using the sole supported format.
func (plan DiscoveryPlan) MarshalJSON() ([]byte, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}

	return json.Marshal(discoveryPlanJSON{
		Root: queryFilterWire{Conditions: plan.root.conditions, Group: plan.root.group},
		Tree: plan.tree, Limit: plan.limit,
	})
}

// UnmarshalJSON validates the document before replacing the receiver.
func (plan *DiscoveryPlan) UnmarshalJSON(data []byte) error {
	if plan == nil || len(data) > maxDiscoveryPlanBytes {
		return fmt.Errorf("%w: nil destination or document exceeds %d bytes", ErrInvalidDiscoveryPlan, maxDiscoveryPlanBytes)
	}

	if !json.Valid(data) {
		return fmt.Errorf("%w: invalid JSON", ErrInvalidDiscoveryPlan)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire discoveryPlanJSON
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDiscoveryPlan, err)
	}

	root, err := NewQueryFilter(wire.Root.Conditions, wire.Root.Group)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDiscoveryPlan, err)
	}

	// Run [DiscoveryPlan.Validate] on a temporary plan so malformed input cannot partially replace the receiver.
	candidate := DiscoveryPlan{root: root, tree: wire.Tree, limit: wire.Limit}
	if err := candidate.Validate(); err != nil {
		return err
	}

	*plan = candidate
	return nil
}

// Validate checks resource bounds and derives every executable partition.
func (plan DiscoveryPlan) Validate() error {
	_, err := plan.executableQueries()
	return err
}

// Queries returns independent queries in match-first order, or nil for an invalid plan.
func (plan DiscoveryPlan) Queries() []ListQuery {
	queries, _ := plan.executableQueries()
	return queries
}

// QueryCount returns the polling request count, or zero for an invalid plan.
func (plan DiscoveryPlan) QueryCount() int { return len(plan.Queries()) }

// newDiscoveryPlan initializes a plan with one active root query.
func newDiscoveryPlan(root QueryFilter, limit uint32) DiscoveryPlan {
	return DiscoveryPlan{root: root, limit: limit, tree: &discoveryPlanNode{Active: true}}
}

// executableQueries validates the tree and derives bounded leaf filters in match-first order.
func (plan DiscoveryPlan) executableQueries() ([]ListQuery, error) {
	if plan.limit == 0 || plan.limit > DefaultLimit {
		return nil, fmt.Errorf("%w: limit outside 1..%d", ErrInvalidDiscoveryPlan, DefaultLimit)
	}
	if err := validateDiscoveryRoot(plan.root); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDiscoveryPlan, err)
	}

	var queries []ListQuery
	nodes := 0

	var walk func(*discoveryPlanNode, QueryFilter, int) error
	walk = func(node *discoveryPlanNode, filter QueryFilter, depth int) error {
		nodes++
		// Bound depth and total nodes before descending into untrusted persisted data.
		if node == nil || depth > maxDiscoveryDepth || nodes > 2*maxDiscoveryQueries-1 {
			return fmt.Errorf("%w: missing node or tree exceeds bounds", ErrInvalidDiscoveryPlan)
		}

		if node.Active {
			if node.Split != nil || node.Match != nil || node.NonMatch != nil || node.Reasons & ^discoveryKnownReasons != 0 {
				return fmt.Errorf("%w: malformed leaf", ErrInvalidDiscoveryPlan)
			}

			queries = append(queries, ListQuery{Filter: filter.String(), Limit: plan.limit})
			return nil
		}

		if node.Split == nil || node.Reasons != 0 || !isDiscoveryPartitionKey(node.Split.Key) {
			return fmt.Errorf("%w: malformed split", ErrInvalidDiscoveryPlan)
		}
		if node.Split.Key == KeyVersion && strings.ContainsAny(node.Split.Value, "*?[") {
			return fmt.Errorf("%w: wildcard split", ErrInvalidDiscoveryPlan)
		}

		yes, err := filter.withPredicate(*node.Split)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidDiscoveryPlan, err)
		}
		no, err := filter.withNORPredicate(*node.Split)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidDiscoveryPlan, err)
		}

		if err := walk(node.Match, yes, depth+1); err != nil {
			return err
		}

		return walk(node.NonMatch, no, depth+1)
	}

	if err := walk(plan.tree, plan.root, 0); err != nil {
		return nil, err
	}

	return queries, nil
}

// planStatus counts active leaves; historical split nodes carry no status.
func planStatus(node *discoveryPlanNode) (leaves, unresolved int, reasons DiscoveryReasons) {
	if node == nil {
		return
	}

	if node.Active {
		if node.Reasons != 0 {
			unresolved = 1
		}
		return 1, unresolved, node.Reasons
	}

	left, pending, flags := planStatus(node.Match)
	right, more, other := planStatus(node.NonMatch)

	return left + right, pending + more, flags | other
}

// clonePredicateGroup copies group storage owned by a query filter.
func clonePredicateGroup(group *PredicateGroup) *PredicateGroup {
	if group == nil {
		return nil
	}

	return &PredicateGroup{
		Predicates: append([]Predicate(nil), group.Predicates...),
		Kind:       group.Kind,
	}
}

// withPredicate returns a copy with one positive condition appended.
func (filter QueryFilter) withPredicate(predicate Predicate) (QueryFilter, error) {
	conditions := append(append([]Predicate(nil), filter.conditions...), predicate)
	return NewQueryFilter(conditions, filter.group)
}

// withNORPredicate returns a copy with a predicate added to the flat NOR group.
func (filter QueryFilter) withNORPredicate(predicate Predicate) (QueryFilter, error) {
	if !canExtendDiscoveryNOR(filter, predicate) {
		return QueryFilter{}, fmt.Errorf(
			"%w: discovery cannot extend NOR with this predicate combination",
			ErrUnsupportedDiscoveryFilter)
	}
	group := clonePredicateGroup(filter.group)
	if group == nil {
		group = &PredicateGroup{Kind: GroupNOR}
	}
	if group.Kind != GroupNOR {
		return QueryFilter{}, fmt.Errorf(
			"%w: discovery complement cannot extend a %s group",
			ErrInvalidQueryFilter, group.Kind)
	}

	group.Predicates = append(group.Predicates, predicate)

	return NewQueryFilter(filter.conditions, group)
}

// canExtendDiscoveryNOR permits partition predicates in one flat NOR group.
// Live probes validated selected map/game-type combinations on bounded DayZ cohorts,
// but established no global predicate-count limit.
// Each split must still pass sampled membership checks before it becomes part of the plan.
func canExtendDiscoveryNOR(filter QueryFilter, predicate Predicate) bool {
	if !isDiscoveryPartitionKey(predicate.Key) {
		return false
	}
	if filter.group == nil {
		return true
	}
	if filter.group.Kind != GroupNOR {
		return false
	}
	if slices.Contains(filter.group.Predicates, predicate) {
		return false
	}

	return true
}

// isDiscoveryPartitionKey identifies exact predicates the planner can derive.
// KeyGameAddr is excluded because a live probe found that
// its NOR query did not return the observed complement;
// positive matching alone is insufficient.
func isDiscoveryPartitionKey(key FilterKey) bool {
	switch key {
	case KeyMap, KeyVersion, KeyGameType, KeyLinux, KeySecure, KeyDedicated:
		return true

	default:
		return false
	}
}
