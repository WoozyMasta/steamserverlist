// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"strconv"
	"strings"
)

const (
	// filterInitialConditionsCapacity reserves space for a typical app-ID filter.
	filterInitialConditionsCapacity = 2
	// filterInitialExclusionsCapacity avoids growth for the common single exclusion.
	filterInitialExclusionsCapacity = 1
)

const (
	// KeyDedicated selects servers marked as dedicated;
	// "1" is the conventional flag value.
	KeyDedicated FilterKey = "dedicated"
	// KeySecure selects VAC-secure servers;
	// "1" is the conventional flag value.
	KeySecure FilterKey = "secure"
	// KeyGameDir selects a game directory, such as "dayz" or "cstrike".
	KeyGameDir FilterKey = "gamedir"
	// KeyMap selects servers running the specified map.
	KeyMap FilterKey = "map"
	// KeyLinux selects Linux servers;
	// "1" is the conventional flag value.
	KeyLinux FilterKey = "linux"
	// KeyPassword filters password protection;
	// GetServerList-specific value semantics are not validated here.
	KeyPassword FilterKey = "password"
	// KeyEmpty excludes empty servers with the documented value "1";
	// use KeyNoPlayers to select empty servers.
	KeyEmpty FilterKey = "empty"
	// KeyFull excludes full servers with the documented value "1";
	// use KeyNotFull to select servers below capacity.
	KeyFull FilterKey = "full"
	// KeyProxy filters proxy status;
	// GetServerList-specific value semantics are not validated here.
	KeyProxy FilterKey = "proxy"
	// KeyAppID selects servers running the specified Steam application ID.
	KeyAppID FilterKey = "appid"
	// KeyNotAppID excludes servers running the specified Steam application ID.
	KeyNotAppID FilterKey = "napp"
	// KeyNoPlayers selects servers reporting no players with the positive value "1".
	KeyNoPlayers FilterKey = "noplayers"
	// KeyNotFull selects servers below their player limit.
	KeyNotFull FilterKey = "notfull"
	// KeyWhite filters whitelist status;
	// GetServerList-specific value semantics are not validated here.
	KeyWhite FilterKey = "white"
	// KeyGameType selects servers with a tag from their game-type list.
	KeyGameType FilterKey = "gametype"
	// KeyGameData selects servers with the specified hidden game-data tag.
	KeyGameData FilterKey = "gamedata"
	// KeyName selects servers whose names match the supplied wildcard pattern.
	KeyName FilterKey = "name_match"
	// KeyVersion selects servers whose versions match the supplied wildcard pattern.
	KeyVersion FilterKey = "version_match"
	// KeySingleAddr collapses results to one server per IP with "1" or "0".
	KeySingleAddr FilterKey = "collapse_addr_hash"
	// KeyGameAddr selects servers at the specified IP address and optional port.
	KeyGameAddr FilterKey = "gameaddr"
)

/*
FilterKey identifies a field in Steam's backslash-delimited master-server filter syntax.
Valve's native matchmaking documentation describes this grammar,
but is not an endpoint-specific contract for GetServerList.
A recognized key is not a guarantee that every game implements it identically.

Filter composition follows these rules:

  - Positive conditions are conjunctive: every condition must match.
  - Repeated keys are preserved, not overwritten.
    In particular, one server may carry multiple gametype tags,
    so repeated gametype conditions can match the same server
    and do not form exclusive buckets.
  - A group combines its predicates as OR, NOR, or NAND.
    QueryFilter supports one flat group alongside the positive conjunction;
    it is not a general nested-expression builder.
    Filter is a lower-level pair builder, not a nested-expression builder;
    [ListQuery.Filter] accepts a caller-built string.
  - Different exact map predicates conflict in a positive conjunction.
    Use an OR group when either of several exact maps is acceptable.

Bounded live DayZ probes confirmed repeated-gametype conjunction
and selected NOR complements, including tested map/gametype combinations.
They do not establish universal behavior for every key, game, or filter size.
In particular, boolean-like zero values are not reliable generic negations,
gameaddr NOR is not a validated address complement,
and no global GetServerList predicate-count limit has been established.
QueryFilter checks encoding constraints and known contradictions;
it cannot prove how Steam will evaluate a request.
Filter intentionally leaves raw operands to callers.
*/
type FilterKey string

// Filter builds low-level Master Server Query Protocol filter expressions.
// Its zero value is ready to use. It preserves repeated conditions and their order,
// does not validate keys or operands, and cannot combine NOR and NAND groups.
// Use [NewQueryFilter] for structural validation and known contradictions.
type Filter struct {
	// conditions contains expressions that all must match.
	conditions []filterCondition
	// norConditions contains expressions excluded by the NOR group.
	norConditions []filterCondition
	// nandConditions contains expressions excluded by the NAND group.
	nandConditions []filterCondition
}

// filterCondition stores a filter pair without allocating its encoded form.
type filterCondition struct {
	// key is the wire-format filter key.
	key string
	// value is the caller-provided filter operand.
	value string
}

// NewFilter creates an empty filter. The zero value of Filter is also usable.
func NewFilter() *Filter {
	return &Filter{}
}

// Clone returns an independent copy of the filter.
func (f *Filter) Clone() *Filter {
	if f == nil {
		return nil
	}

	return &Filter{
		conditions:    append([]filterCondition(nil), f.conditions...),
		norConditions: append([]filterCondition(nil), f.norConditions...),
		nandConditions: append(
			[]filterCondition(nil), f.nandConditions...,
		),
	}
}

// Add appends a condition that must match.
func (f *Filter) Add(key FilterKey, value string) {
	f.conditions = addFilterCondition(
		f.conditions, key, value, filterInitialConditionsCapacity,
	)
}

// AddNor appends a condition to the NOR group.
func (f *Filter) AddNor(key FilterKey, value string) {
	f.norConditions = addFilterCondition(
		f.norConditions, key, value, filterInitialExclusionsCapacity,
	)
}

// AddNand appends a condition to the NAND group.
func (f *Filter) AddNand(key FilterKey, value string) {
	f.nandConditions = addFilterCondition(
		f.nandConditions, key, value, filterInitialExclusionsCapacity,
	)
}

// Remove removes the first matching regular condition, if present.
func (f *Filter) Remove(key FilterKey, value string) {
	f.conditions = removeFilterCondition(f.conditions, key, value)
}

// RemoveNor removes the first matching condition from the NOR group.
func (f *Filter) RemoveNor(key FilterKey, value string) {
	f.norConditions = removeFilterCondition(f.norConditions, key, value)
}

// RemoveNand removes the first matching condition from the NAND group.
func (f *Filter) RemoveNand(key FilterKey, value string) {
	f.nandConditions = removeFilterCondition(f.nandConditions, key, value)
}

// String encodes the filter using Steam's backslash-delimited syntax.
// It returns ErrFilterCannotCombineNORAndNAND if both exclusion groups are used.
func (f *Filter) String() (string, error) {
	if len(f.norConditions) > 0 && len(f.nandConditions) > 0 {
		return "", ErrFilterCannotCombineNORAndNAND
	}

	encodedSize := filterConditionsSize(f.conditions)
	if len(f.norConditions) > 0 {
		if len(f.conditions) > 0 {
			encodedSize++
		}
		encodedSize += len("nor\\") + filterDecimalDigits(len(f.norConditions)) + 1 +
			filterConditionsSize(f.norConditions)
	}
	if len(f.nandConditions) > 0 {
		if len(f.conditions) > 0 {
			encodedSize++
		}
		encodedSize += len("nand\\") + filterDecimalDigits(len(f.nandConditions)) + 1 +
			filterConditionsSize(f.nandConditions)
	}

	var encoded strings.Builder
	encoded.Grow(encodedSize)
	appendFilterConditions(&encoded, f.conditions)
	if len(f.norConditions) > 0 {
		if encoded.Len() > 0 {
			encoded.WriteByte('\\')
		}
		encoded.WriteString("nor\\")
		appendFilterCount(&encoded, len(f.norConditions))
		encoded.WriteByte('\\')
		appendFilterConditions(&encoded, f.norConditions)
	}
	if len(f.nandConditions) > 0 {
		if encoded.Len() > 0 {
			encoded.WriteByte('\\')
		}
		encoded.WriteString("nand\\")
		appendFilterCount(&encoded, len(f.nandConditions))
		encoded.WriteByte('\\')
		appendFilterConditions(&encoded, f.nandConditions)
	}

	return encoded.String(), nil
}

// appendFilterConditions writes encoded pairs separated by backslashes.
func appendFilterConditions(builder *strings.Builder, conditions []filterCondition) {
	for i, condition := range conditions {
		if i > 0 {
			builder.WriteByte('\\')
		}
		builder.WriteString(condition.key)
		builder.WriteByte('\\')
		builder.WriteString(condition.value)
	}
}

// appendFilterCount writes a non-negative condition count without string allocation.
func appendFilterCount(builder *strings.Builder, count int) {
	var digits [20]byte
	builder.Write(strconv.AppendInt(digits[:0], int64(count), 10))
}

// addFilterCondition appends a key/value pair, reserving common group capacity.
func addFilterCondition(
	conditions []filterCondition,
	key FilterKey,
	value string,
	initialCapacity int,
) []filterCondition {
	if len(conditions) == 0 && cap(conditions) == 0 {
		conditions = make([]filterCondition, 0, initialCapacity)
	}
	keyString := string(key)

	return append(conditions, filterCondition{key: keyString, value: value})
}

// filterConditionsSize returns a group's encoded size, including pair separators.
func filterConditionsSize(conditions []filterCondition) int {
	if len(conditions) == 0 {
		return 0
	}

	size := len(conditions) - 1
	for _, condition := range conditions {
		size += len(condition.key) + 1 + len(condition.value)
	}

	return size
}

// filterDecimalDigits returns the base-10 width of a positive count.
func filterDecimalDigits(count int) int {
	digits := 1
	for count >= 10 {
		count /= 10
		digits++
	}

	return digits
}

// removeFilterCondition removes only the first exact key/value match.
func removeFilterCondition(
	conditions []filterCondition,
	key FilterKey,
	value string,
) []filterCondition {
	keyString := string(key)
	for i, condition := range conditions {
		if condition.key == keyString && condition.value == value {
			last := len(conditions) - 1
			copy(conditions[i:], conditions[i+1:])
			conditions[last] = filterCondition{}
			return conditions[:last]
		}
	}

	return conditions
}
