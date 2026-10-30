// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"fmt"
	"math"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	// defaultDiscoveryRequests caps all requests in a first-run discovery.
	defaultDiscoveryRequests = 24
	// defaultDiscoveryLeaves caps the reusable plan's routine polling cost.
	defaultDiscoveryLeaves = 24
	// defaultDiscoveryBuffer bounds retained records used to validate partitions.
	defaultDiscoveryBuffer = 250000
	// maxDiscoveryBuffer is the hard upper bound accepted for witness retention.
	maxDiscoveryBuffer = 250000
	// defaultDiscoveryDepth bounds nested partition splits.
	defaultDiscoveryDepth = 64
	// defaultDiscoveryMinValue excludes one-off sample values from candidates.
	defaultDiscoveryMinValue = 2
)

// chooseDiscoverySplit selects the most balanced supported sampled value.
func chooseDiscoverySplit[T any](
	records []discoveryRecord[T],
	filter QueryFilter,
	features []SplitFeature,
	minCount int,
) (Predicate, bool) {
	counts := make(map[Predicate]int)

	for _, record := range records {
		for _, feature := range features {
			// Scalar exact fields cannot be split again once required; gametype is a multi-tag list.
			if hasPositiveFilterKey(filter, feature.Key) && feature.Key != KeyGameType {
				continue
			}

			values, known := witnessValues(record.witness, feature.Key)
			if !known {
				continue
			}

			for valueIndex, value := range values {
				if value != "" {
					// One server may repeat a tag; count it once for this candidate.
					if slices.Contains(values[:valueIndex], value) {
						continue
					}

					counts[Predicate{Key: feature.Key, Value: value}]++
				}
			}
		}
	}

	predicates := make([]Predicate, 0, len(counts))
	for predicate := range counts {
		predicates = append(predicates, predicate)
	}

	sort.Slice(predicates, func(i, j int) bool {
		if predicates[i].Key == predicates[j].Key {
			return predicates[i].Value < predicates[j].Value
		}

		return predicates[i].Key < predicates[j].Key
	})

	var best Predicate
	bestScore, found := math.Inf(1), false
	for _, predicate := range predicates {
		count := counts[predicate]
		if _, err := NewQueryFilter([]Predicate{predicate}, nil); err != nil {
			continue
		}

		if count < minCount || count >= len(records) || hasPositivePredicate(filter, predicate) ||
			!canExtendDiscoveryNOR(filter, predicate) ||
			!splitCandidateCoversSample(records, predicate) {
			continue
		}

		weight := 1.0
		for _, feature := range features {
			if feature.Key == predicate.Key && feature.Weight > 0 {
				weight = feature.Weight
				break
			}
		}

		// Lower scores favor near-half splits; feature weight adjusts preference,
		// but the score is only a sample heuristic, not a population estimate.
		score := math.Abs(float64(2*count-len(records))) / float64(len(records)) / weight
		if !found || score < bestScore {
			best, bestScore, found = predicate, score, true
		}
	}

	return best, found
}

// validDiscoveryChildren requires disjoint child pages with verified membership.
func validDiscoveryChildren[T any](matching, nonMatching []discoveryRecord[T], predicate Predicate) bool {
	seen := make(map[netip.AddrPort]bool, len(matching))
	for _, record := range matching {
		if !witnessMatches(record.witness, predicate) {
			return false
		}

		seen[record.witness.addr] = true
	}

	for _, record := range nonMatching {
		if witnessMatches(record.witness, predicate) {
			return false
		}
		if seen[record.witness.addr] {
			return false
		}
	}

	return true
}

// splitMembershipKnown separates missing evidence from a contradictory response.
func splitMembershipKnown[T any](matching, nonMatching []discoveryRecord[T], predicate Predicate) bool {
	for _, record := range matching {
		if !witnessKnown(record.witness, predicate.Key) {
			return false
		}
	}

	for _, record := range nonMatching {
		if !witnessKnown(record.witness, predicate.Key) {
			return false
		}
	}

	return true
}

// hasUnknownFeatureEvidence detects absent fields when no safe split was found.
func hasUnknownFeatureEvidence[T any](records []discoveryRecord[T], filter QueryFilter, features []SplitFeature) bool {
	for _, feature := range features {
		if hasPositiveFilterKey(filter, feature.Key) && feature.Key != KeyGameType {
			continue
		}

		for _, record := range records {
			if !witnessKnown(record.witness, feature.Key) {
				return true
			}
		}
	}

	return false
}

// witnessValues returns known filterable values and whether the field was present.
func witnessValues(w discoveryWitness, key FilterKey) ([]string, bool) {
	switch key {
	case KeyMap:
		if w.mapName != "" {
			return []string{strings.ToLower(w.mapName)}, true
		}
		// An empty map is observed evidence, not a missing JSON field.
		return nil, true

	case KeyVersion:
		if w.version == "" {
			// Empty versions are known non-matches; wildcard-bearing values cannot be partitioned exactly.
			return nil, true
		}
		if !strings.ContainsAny(w.version, "*?[") {
			return []string{w.version}, true
		}

	case KeyGameType:
		if w.gameType != "" {
			var values []string
			for tag := range strings.SplitSeq(w.gameType, ",") {
				tag = strings.TrimSpace(tag)
				if tag != "" {
					values = append(values, tag)
				}
			}
			return values, true
		}
		// A present empty tag field is still known not to match any exact tag.
		return nil, true

	case KeyLinux:
		if strings.EqualFold(w.os, "l") || strings.EqualFold(w.os, "linux") {
			return []string{"1"}, true
		}
		if strings.EqualFold(w.os, "w") || strings.EqualFold(w.os, "windows") {
			return nil, true
		}

	case KeySecure:
		if w.secureKnown {
			if w.secure {
				return []string{"1"}, true
			}
			return nil, true
		}

	case KeyDedicated:
		if w.dedicatedKnown {
			if w.dedicated {
				return []string{"1"}, true
			}
			return nil, true
		}
	}

	return nil, false
}

// witnessKnown distinguishes absent boolean/OS evidence from a negative value.
func witnessKnown(w discoveryWitness, key FilterKey) bool {
	switch key {
	case KeyMap, KeyGameType:
		return true

	case KeyVersion:
		return !strings.ContainsAny(w.version, "*?[")

	case KeyLinux:
		return strings.EqualFold(w.os, "l") ||
			strings.EqualFold(w.os, "linux") ||
			strings.EqualFold(w.os, "w") ||
			strings.EqualFold(w.os, "windows")

	case KeySecure:
		return w.secureKnown

	case KeyDedicated:
		return w.dedicatedKnown
	}

	return false
}

// witnessMatches checks exact membership without allocating candidate slices.
func witnessMatches(w discoveryWitness, predicate Predicate) bool {
	switch predicate.Key {
	case KeyMap:
		return strings.EqualFold(w.mapName, predicate.Value)

	case KeyVersion:
		return w.version == predicate.Value

	case KeyGameType:
		for tag := range strings.SplitSeq(w.gameType, ",") {
			if strings.TrimSpace(tag) == predicate.Value {
				return true
			}
		}

	case KeyLinux:
		return predicate.Value == "1" &&
			(strings.EqualFold(w.os, "l") || strings.EqualFold(w.os, "linux"))

	case KeySecure:
		return predicate.Value == "1" && w.secureKnown && w.secure

	case KeyDedicated:
		return predicate.Value == "1" && w.dedicatedKnown && w.dedicated
	}

	return false
}

// hasPositiveFilterKey reports whether a branch already selects this key positively.
func hasPositiveFilterKey(filter QueryFilter, key FilterKey) bool {
	for _, condition := range filter.conditions {
		if condition.Key == key {
			return true
		}
	}

	return false
}

// hasPositivePredicate prevents repeating an exact positive condition
// while still allowing the verified intersection of distinct game-type tags.
func hasPositivePredicate(filter QueryFilter, candidate Predicate) bool {
	return slices.Contains(filter.conditions, candidate)
}

// normalizeDiscoveryOptions applies defaults and validates resource ceilings.
func normalizeDiscoveryOptions(options *DiscoveryOptions) error {
	if options.Limit == 0 {
		options.Limit = DefaultLimit
	}
	if options.MaxRequests == 0 {
		options.MaxRequests = defaultDiscoveryRequests
	}
	if options.MaxPlanQueries == 0 {
		options.MaxPlanQueries = defaultDiscoveryLeaves
	}
	if options.MaxBufferedRecords == 0 {
		options.MaxBufferedRecords = defaultDiscoveryBuffer
	}
	if options.MaxDepth == 0 {
		options.MaxDepth = defaultDiscoveryDepth
	}
	if options.MinValueCount == 0 {
		options.MinValueCount = defaultDiscoveryMinValue
	}
	if options.Features == nil {
		options.Features = DefaultSplitFeatures()
	}

	options.Features = append([]SplitFeature(nil), options.Features...)
	// Reject invalid or repeated feature weights before they affect split ranking.
	if options.Limit > DefaultLimit ||
		options.MaxRequests < 1 ||
		options.MaxRequests > 256 ||
		options.MaxPlanQueries < 1 ||
		options.MaxPlanQueries > maxDiscoveryQueries ||
		options.MaxBufferedRecords < int(options.Limit) ||
		options.MaxBufferedRecords > maxDiscoveryBuffer ||
		options.MaxDepth < 1 ||
		options.MaxDepth > maxDiscoveryDepth ||
		options.MinValueCount < 1 {
		return fmt.Errorf("%w: limits outside supported bounds", ErrInvalidDiscoveryOptions)
	}

	seen := make(map[FilterKey]struct{}, len(options.Features))
	for _, feature := range options.Features {
		if !isDiscoveryPartitionKey(feature.Key) ||
			feature.Weight < 0 ||
			feature.Weight > 100 ||
			feature.Weight != feature.Weight {
			return fmt.Errorf("%w: invalid split feature %q", ErrInvalidDiscoveryOptions, feature.Key)
		}

		if _, exists := seen[feature.Key]; exists {
			return fmt.Errorf("%w: duplicate split feature %q", ErrInvalidDiscoveryOptions, feature.Key)
		}

		seen[feature.Key] = struct{}{}
	}

	return nil
}

// validateDiscoveryRoot rejects unscoped or unsupported expressions before I/O.
func validateDiscoveryRoot(root QueryFilter) error {
	if len(root.conditions) == 0 && root.group == nil {
		return fmt.Errorf("%w: root filter must scope the game", ErrInvalidDiscoveryOptions)
	}

	if root.group != nil && root.group.Kind != GroupNOR {
		return fmt.Errorf("%w: discovery root supports only conjunction and NOR", ErrUnsupportedDiscoveryFilter)
	}

	for _, p := range root.conditions {
		if p.Key == KeySingleAddr {
			return fmt.Errorf("%w: %s collapses result rows", ErrUnsupportedDiscoveryFilter, p.Key)
		}
	}

	if root.group != nil {
		for _, p := range root.group.Predicates {
			if p.Key == KeySingleAddr {
				return fmt.Errorf("%w: %s collapses result rows", ErrUnsupportedDiscoveryFilter, p.Key)
			}
		}
	}

	return nil
}

// discoveryNearLimit applies the conservative 0.1% response-capacity margin.
func discoveryNearLimit(count, limit int) bool {
	margin := limit / 1000
	if limit%1000 != 0 {
		margin++
	}
	threshold := limit - margin
	if threshold == 0 {
		threshold = 1
	}

	return count >= threshold
}

// rootAppIDIssue compares response records with an explicit root app-ID condition.
func rootAppIDIssue[T any](root QueryFilter, batches ...[]discoveryRecord[T]) DiscoveryReasons {
	var expected uint64
	found := false

	for _, condition := range root.conditions {
		if condition.Key == KeyAppID {
			parsed, err := strconv.ParseUint(condition.Value, 10, 32)
			if err != nil {
				return DiscoveryReasonUnverifiedSemantics
			}

			expected = parsed
			found = true
			break
		}
	}

	if !found {
		return 0
	}

	var reason DiscoveryReasons
	for _, batch := range batches {
		for _, record := range batch {
			if record.witness.appID == 0 {
				reason |= DiscoveryReasonUnverifiedSemantics
			} else if uint64(record.witness.appID) != expected {
				reason |= DiscoveryReasonInconsistent
			}
		}
	}

	return reason
}

// splitCandidateCoversSample requires usable membership evidence for every row.
func splitCandidateCoversSample[T any](records []discoveryRecord[T], predicate Predicate) bool {
	for _, record := range records {
		if !witnessKnown(record.witness, predicate.Key) {
			return false
		}
	}

	return true
}
