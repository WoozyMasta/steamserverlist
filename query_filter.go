// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	// GroupOR matches a server when any group predicate matches.
	GroupOR GroupKind = "or"
	// GroupNOR excludes servers matching any group predicate.
	GroupNOR GroupKind = "nor"
	// GroupNAND excludes servers matching every group predicate.
	GroupNAND GroupKind = "nand"
)

// GroupKind selects how predicates in a validated filter group are combined.
type GroupKind string

// Predicate is one key/value condition in a validated server filter.
type Predicate struct {
	// Key selects the filter field.
	Key FilterKey `json:"key" yaml:"key"`
	// Value is the exact operand encoded for the selected field.
	Value string `json:"value" yaml:"value"`
}

// PredicateGroup combines its predicates using one protocol group operator.
type PredicateGroup struct {
	// Kind selects OR, NOR, or NAND behavior for the flat predicate list.
	Kind GroupKind `json:"kind" yaml:"kind"`
	// Predicates are the alternatives or exclusions combined by Kind.
	Predicates []Predicate `json:"predicates" yaml:"predicates"`
}

/*
QueryFilter is an immutable filter expression validated
for encoding shape and known contradictions.

Use [NewQueryFilter] to construct one;
use [Filter] when the caller needs unrestricted raw encoding.

Validation is local:
it does not contact Steam or guarantee that a key, value,
or Boolean composition has the expected meaning on a particular GetServerList backend.
The supported expression is a positive conjunction and at most one flat OR, NOR, or NAND group;
arbitrary nesting is unsupported.
*/
type QueryFilter struct {
	group      *PredicateGroup // group is the copied optional flat Boolean expression.
	conditions []Predicate     // conditions is the copied conjunction.
}

// NewQueryFilter validates and copies a conjunction and an optional flat Boolean group.
// It rejects malformed operands and only those contradictions established
// by exact local invariants or validated game-type complement behavior;
// it does not rewrite or normalize caller input.
func NewQueryFilter(all []Predicate, group *PredicateGroup) (QueryFilter, error) {
	// Copy caller-owned slices so the validated filter remains immutable after construction.
	conditions := append([]Predicate(nil), all...)
	if err := validatePredicates(conditions); err != nil {
		return QueryFilter{}, err
	}
	if err := validateExactConjunctions(conditions); err != nil {
		return QueryFilter{}, err
	}

	var copiedGroup *PredicateGroup
	if group != nil {
		if group.Kind != GroupOR && group.Kind != GroupNOR && group.Kind != GroupNAND {
			return QueryFilter{}, fmt.Errorf("%w: unsupported group kind %q", ErrInvalidQueryFilter, group.Kind)
		}
		if len(group.Predicates) == 0 {
			return QueryFilter{}, fmt.Errorf("%w: predicate group is empty", ErrInvalidQueryFilter)
		}

		// Copy the group separately because it is also retained by the filter.
		predicates := append([]Predicate(nil), group.Predicates...)
		if err := validatePredicates(predicates); err != nil {
			return QueryFilter{}, err
		}

		if err := validateGroupContradictions(conditions, PredicateGroup{
			Kind:       group.Kind,
			Predicates: predicates,
		}); err != nil {
			return QueryFilter{}, err
		}

		copiedGroup = &PredicateGroup{Kind: group.Kind, Predicates: predicates}
	}

	return QueryFilter{conditions: conditions, group: copiedGroup}, nil
}

// String encodes the validated expression using Steam's backslash-delimited filter syntax.
func (q QueryFilter) String() string {
	var encoded strings.Builder
	size := filterPredicatesSize(q.conditions)
	if q.group != nil {
		if size > 0 {
			size++
		}
		size += len(q.group.Kind) + 1 + filterDecimalDigits(len(q.group.Predicates)) + 1
		size += filterPredicatesSize(q.group.Predicates)
	}

	encoded.Grow(size)
	appendPredicates(&encoded, q.conditions)

	if q.group != nil {
		if encoded.Len() > 0 {
			encoded.WriteByte('\\')
		}
		encoded.WriteString(string(q.group.Kind))
		encoded.WriteByte('\\')
		encoded.WriteString(strconv.Itoa(len(q.group.Predicates)))
		encoded.WriteByte('\\')
		appendPredicates(&encoded, q.group.Predicates)
	}

	return encoded.String()
}

// validateGroupContradictions rejects flat groups made impossible
// by exact app-ID or single-valued map predicates in the outer conjunction.
func validateGroupContradictions(conditions []Predicate, group PredicateGroup) error {
	for _, condition := range conditions {
		switch condition.Key {
		case KeyAppID:

			switch group.Kind {
			case GroupNOR:
				for _, predicate := range group.Predicates {
					if predicate.Key == KeyAppID && equalNumericAppIDs(condition.Value, predicate.Value) {
						return fmt.Errorf(
							"%w: appid=%q is both required and excluded",
							ErrConflictingQueryFilter, condition.Value,
						)
					}
				}

			case GroupOR:
				// An OR group is impossible only when every alternative is a different exact app ID.
				allAppIDsConflict := len(group.Predicates) > 0
				conditionAppID, _ := strconv.ParseUint(condition.Value, 10, 32)

				for _, predicate := range group.Predicates {
					parsedAppID, err := strconv.ParseUint(predicate.Value, 10, 32)
					if predicate.Key != KeyAppID || err != nil ||
						parsedAppID == conditionAppID {
						allAppIDsConflict = false
						break
					}
				}

				if allAppIDsConflict {
					return fmt.Errorf(
						"%w: appid=%q cannot match any OR appid predicate",
						ErrConflictingQueryFilter, condition.Value,
					)
				}
			}

		case KeyMap:
			if strings.ContainsAny(condition.Value, "*?[") {
				continue
			}

			switch group.Kind {
			case GroupNOR:
				for _, predicate := range group.Predicates {
					if predicate.Key == KeyMap && !strings.ContainsAny(predicate.Value, "*?[") &&
						predicate.Value == condition.Value {
						return fmt.Errorf(
							"%w: map=%q is both required and excluded",
							ErrConflictingQueryFilter, condition.Value,
						)
					}
				}

			case GroupOR:
				// Patterns or predicates on other keys may still match, so only exact map-only ORs conflict.
				allMapsConflict := len(group.Predicates) > 0
				for _, predicate := range group.Predicates {
					if predicate.Key != KeyMap || strings.ContainsAny(predicate.Value, "*?[") ||
						predicate.Value == condition.Value {
						allMapsConflict = false
						break
					}
				}

				if allMapsConflict {
					return fmt.Errorf(
						"%w: map=%q cannot match any OR map predicate",
						ErrConflictingQueryFilter, condition.Value,
					)
				}
			}
		}
	}

	if group.Kind == GroupNOR {
		return validateGameTypeContradiction(conditions, group.Predicates)
	}
	if group.Kind == GroupNAND {
		return validateGameTypeNANDContradiction(conditions, group.Predicates)
	}

	return nil
}

// equalNumericAppIDs reports whether two valid unsigned 32-bit app IDs match.
func equalNumericAppIDs(left, right string) bool {
	leftID, leftErr := strconv.ParseUint(left, 10, 32)
	rightID, rightErr := strconv.ParseUint(right, 10, 32)

	return leftErr == nil && rightErr == nil && leftID == rightID
}

// validatePredicates rejects keys and values that cannot be represented
// as one unambiguous key/value pair in the protocol's delimiter-based syntax.
func validatePredicates(predicates []Predicate) error {
	for index, predicate := range predicates {
		if !isSupportedFilterKey(predicate.Key) {
			return fmt.Errorf(
				"%w: unsupported key %q at index %d",
				ErrInvalidQueryFilter, predicate.Key, index,
			)
		}

		if predicate.Value == "" || strings.ContainsAny(predicate.Value, "\\\x00") {
			return fmt.Errorf(
				"%w: empty or delimited value for key %q at index %d",
				ErrInvalidQueryFilter, predicate.Key, index,
			)
		}

		switch predicate.Key {
		case KeyAppID:
			if _, err := strconv.ParseUint(predicate.Value, 10, 32); err != nil {
				return fmt.Errorf(
					"%w: appid %q is not an unsigned 32-bit integer",
					ErrInvalidQueryFilter, predicate.Value,
				)
			}

		case KeyNotAppID:
			if _, err := strconv.ParseUint(predicate.Value, 10, 32); err != nil {
				return fmt.Errorf(
					"%w: napp %q is not an unsigned 32-bit integer",
					ErrInvalidQueryFilter, predicate.Value,
				)
			}
		}
	}

	return nil
}

// validateExactConjunctions rejects contradictory exact app-ID exclusions
// and map predicates while leaving pattern-capable and multi-value keys untouched.
func validateExactConjunctions(predicates []Predicate) error {
	var appID string
	var mapName string
	var excludedAppIDs []string
	hasAppID := false
	hasMap := false

	// Process in caller order while retaining exclusions, since napp may precede appid.
	for _, predicate := range predicates {
		switch predicate.Key {
		case KeyAppID:
			parsedAppID, err := strconv.ParseUint(predicate.Value, 10, 32)
			if err != nil {
				return fmt.Errorf(
					"%w: appid %q is not an unsigned 32-bit integer",
					ErrInvalidQueryFilter, predicate.Value,
				)
			}

			// Canonical decimal form makes values such as "0221100" compare numerically.
			value := strconv.FormatUint(parsedAppID, 10)
			if hasAppID && appID != value {
				return fmt.Errorf(
					"%w: appid=%q conflicts with appid=%q",
					ErrConflictingQueryFilter, appID, value,
				)
			}

			appID = value
			hasAppID = true
			if slices.Contains(excludedAppIDs, value) {
				return fmt.Errorf(
					"%w: appid=%q is excluded by napp",
					ErrConflictingQueryFilter, value,
				)
			}

		case KeyNotAppID:
			parsedAppID, err := strconv.ParseUint(predicate.Value, 10, 32)
			if err != nil {
				return fmt.Errorf(
					"%w: napp %q is not an unsigned 32-bit integer",
					ErrInvalidQueryFilter, predicate.Value,
				)
			}

			value := strconv.FormatUint(parsedAppID, 10)
			if hasAppID && appID == value {
				return fmt.Errorf(
					"%w: appid=%q is excluded by napp",
					ErrConflictingQueryFilter, value,
				)
			}

			excludedAppIDs = append(excludedAppIDs, value)

		case KeyMap:
			if strings.ContainsAny(predicate.Value, "*?[") {
				continue
			}

			if hasMap && mapName != predicate.Value {
				return fmt.Errorf(
					"%w: map=%q conflicts with map=%q",
					ErrConflictingQueryFilter, mapName, predicate.Value,
				)
			}

			mapName = predicate.Value
			hasMap = true
		}
	}

	return nil
}

// validateGameTypeContradiction catches a positive tag explicitly excluded by a NOR group,
// for which the live Sakhal probe confirmed complement behavior.
func validateGameTypeContradiction(conditions, excluded []Predicate) error {
	for _, predicate := range excluded {
		if predicate.Key != KeyGameType {
			continue
		}

		for _, condition := range conditions {
			if condition.Key == KeyGameType && condition.Value == predicate.Value {
				return fmt.Errorf(
					"%w: gametype=%q is both required and excluded",
					ErrConflictingQueryFilter, predicate.Value,
				)
			}
		}
	}

	return nil
}

// validateGameTypeNANDContradiction rejects a NAND group
// when every exact game-type tag it tests is already required by the outer conjunction.
func validateGameTypeNANDContradiction(conditions, excluded []Predicate) error {
	for _, predicate := range excluded {
		if predicate.Key != KeyGameType {
			return nil
		}
		if !hasRequiredGameType(conditions, predicate.Value) {
			return nil
		}
	}

	return fmt.Errorf(
		"%w: all NAND game-type predicates are required",
		ErrConflictingQueryFilter,
	)
}

// hasRequiredGameType reports whether the outer conjunction requires value.
func hasRequiredGameType(conditions []Predicate, value string) bool {
	for _, condition := range conditions {
		if condition.Key == KeyGameType && condition.Value == value {
			return true
		}
	}

	return false
}

// isSupportedFilterKey reports whether the validated builder recognizes key.
func isSupportedFilterKey(key FilterKey) bool {
	switch key {
	case KeyDedicated,
		KeySecure,
		KeyGameDir,
		KeyMap,
		KeyLinux,
		KeyPassword,
		KeyEmpty,
		KeyFull,
		KeyProxy,
		KeyAppID,
		KeyNotAppID,
		KeyNoPlayers,
		KeyNotFull,
		KeyWhite,
		KeyGameType,
		KeyGameData,
		KeyName,
		KeyVersion,
		KeySingleAddr,
		KeyGameAddr:
		return true

	default:
		return false
	}
}

// appendPredicates writes key/value pairs separated by backslashes.
func appendPredicates(builder *strings.Builder, predicates []Predicate) {
	for index, predicate := range predicates {
		if index > 0 {
			builder.WriteByte('\\')
		}
		builder.WriteString(string(predicate.Key))
		builder.WriteByte('\\')
		builder.WriteString(predicate.Value)
	}
}

// filterPredicatesSize returns the encoded size of a predicate sequence.
func filterPredicatesSize(predicates []Predicate) int {
	if len(predicates) == 0 {
		return 0
	}

	size := len(predicates) - 1
	for _, predicate := range predicates {
		size += len(predicate.Key) + 1 + len(predicate.Value)
	}

	return size
}
