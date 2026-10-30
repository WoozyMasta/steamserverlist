// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"errors"
	"testing"
)

func TestNewQueryFilterEncodesFlatGroups(t *testing.T) {
	tests := []struct {
		name string
		kind GroupKind
		want string
	}{
		{
			name: "OR",
			kind: GroupOR,
			want: `appid\221100\or\2\gametype\shardABC123\gametype\etm4.000000`,
		},
		{
			name: "NOR",
			kind: GroupNOR,
			want: `appid\221100\nor\2\gametype\shardABC123\gametype\etm4.000000`,
		},
		{
			name: "NAND",
			kind: GroupNAND,
			want: `appid\221100\nand\2\gametype\shardABC123\gametype\etm4.000000`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filter, err := NewQueryFilter(
				[]Predicate{{Key: KeyAppID, Value: "221100"}},
				&PredicateGroup{
					Kind: test.kind,
					Predicates: []Predicate{
						{Key: KeyGameType, Value: "shardABC123"},
						{Key: KeyGameType, Value: "etm4.000000"},
					},
				},
			)
			if err != nil {
				t.Fatalf("NewQueryFilter() error = %v", err)
			}
			if got := filter.String(); got != test.want {
				t.Fatalf("String() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewQueryFilterAllowsRepeatedGameTypeConjunction(t *testing.T) {
	filter, err := NewQueryFilter([]Predicate{
		{Key: KeyAppID, Value: "221100"},
		{Key: KeyGameType, Value: "shardABC123"},
		{Key: KeyGameType, Value: "etm4.000000"},
	}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() error = %v", err)
	}
	if want := `appid\221100\gametype\shardABC123\gametype\etm4.000000`; filter.String() != want {
		t.Fatalf("String() = %q, want %q", filter.String(), want)
	}
}

func TestNewQueryFilterRejectsMalformedPredicates(t *testing.T) {
	tests := []struct {
		name      string
		predicate Predicate
	}{
		{name: "unknown key", predicate: Predicate{Key: "unknown", Value: "x"}},
		{name: "empty key", predicate: Predicate{Value: "x"}},
		{name: "empty value", predicate: Predicate{Key: KeyMap}},
		{name: "invalid app ID", predicate: Predicate{Key: KeyAppID, Value: "steam"}},
		{name: "invalid excluded app ID", predicate: Predicate{Key: KeyNotAppID, Value: "steam"}},
		{name: "delimiter in key", predicate: Predicate{Key: `map\or`, Value: "x"}},
		{name: "delimiter in value", predicate: Predicate{Key: KeyMap, Value: `sakhal\appid`}},
		{name: "NUL in value", predicate: Predicate{Key: KeyMap, Value: "sakhal\x00x"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewQueryFilter([]Predicate{test.predicate}, nil); !errors.Is(err, ErrInvalidQueryFilter) {
				t.Fatalf("NewQueryFilter() error = %v, want ErrInvalidQueryFilter", err)
			}
		})
	}
}

func TestNewQueryFilterRejectsMalformedAppIDsInGroups(t *testing.T) {
	tests := []Predicate{
		{Key: KeyAppID, Value: "steam"},
		{Key: KeyNotAppID, Value: "4294967296"},
	}
	for _, predicate := range tests {
		t.Run(string(predicate.Key), func(t *testing.T) {
			group := &PredicateGroup{Kind: GroupOR, Predicates: []Predicate{predicate}}
			if _, err := NewQueryFilter(nil, group); !errors.Is(err, ErrInvalidQueryFilter) {
				t.Fatalf("NewQueryFilter() error = %v, want ErrInvalidQueryFilter", err)
			}
		})
	}
}

func TestNewQueryFilterRejectsMalformedGroups(t *testing.T) {
	tests := []struct {
		name  string
		group *PredicateGroup
	}{
		{name: "unknown kind", group: &PredicateGroup{Kind: "xor", Predicates: []Predicate{{Key: KeyMap, Value: "sakhal"}}}},
		{name: "empty group", group: &PredicateGroup{Kind: GroupOR}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewQueryFilter(nil, test.group); !errors.Is(err, ErrInvalidQueryFilter) {
				t.Fatalf("NewQueryFilter() error = %v, want ErrInvalidQueryFilter", err)
			}
		})
	}
}

func TestNewQueryFilterRejectsExactConflicts(t *testing.T) {
	tests := []struct {
		name       string
		conditions []Predicate
		group      *PredicateGroup
	}{
		{
			name: "distinct app IDs",
			conditions: []Predicate{
				{Key: KeyAppID, Value: "221100"},
				{Key: KeyAppID, Value: "730"},
			},
		},
		{
			name: "app ID excluded by napp",
			conditions: []Predicate{
				{Key: KeyAppID, Value: "221100"},
				{Key: KeyNotAppID, Value: "221100"},
			},
		},
		{
			name: "napp excluded by equivalent app ID",
			conditions: []Predicate{
				{Key: KeyNotAppID, Value: "0221100"},
				{Key: KeyAppID, Value: "221100"},
			},
		},
		{
			name: "distinct exact maps",
			conditions: []Predicate{
				{Key: KeyMap, Value: "sakhal"},
				{Key: KeyMap, Value: "chernarusplus"},
			},
		},
		{
			name:       "required game type excluded by NOR",
			conditions: []Predicate{{Key: KeyGameType, Value: "external"}},
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyGameType, Value: "external"}},
			},
		},
		{
			name: "required game types excluded by NAND",
			conditions: []Predicate{
				{Key: KeyGameType, Value: "shardABC123"},
				{Key: KeyGameType, Value: "etm4.000000"},
			},
			group: &PredicateGroup{
				Kind: GroupNAND,
				Predicates: []Predicate{
					{Key: KeyGameType, Value: "shardABC123"},
					{Key: KeyGameType, Value: "etm4.000000"},
				},
			},
		},
		{
			name:       "required exact map excluded by NOR",
			conditions: []Predicate{{Key: KeyMap, Value: "sakhal"}},
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyMap, Value: "sakhal"}},
			},
		},
		{
			name:       "required app ID excluded by NOR",
			conditions: []Predicate{{Key: KeyAppID, Value: "221100"}},
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyAppID, Value: "0221100"}},
			},
		},
		{
			name:       "required app ID absent from every OR alternative",
			conditions: []Predicate{{Key: KeyAppID, Value: "221100"}},
			group: &PredicateGroup{
				Kind: GroupOR,
				Predicates: []Predicate{
					{Key: KeyAppID, Value: "730"},
					{Key: KeyAppID, Value: "440"},
				},
			},
		},
		{
			name:       "required exact map excluded by every OR alternative",
			conditions: []Predicate{{Key: KeyMap, Value: "sakhal"}},
			group: &PredicateGroup{
				Kind: GroupOR,
				Predicates: []Predicate{
					{Key: KeyMap, Value: "chernarusplus"},
					{Key: KeyMap, Value: "enoch"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewQueryFilter(test.conditions, test.group); !errors.Is(err, ErrConflictingQueryFilter) {
				t.Fatalf("NewQueryFilter() error = %v, want ErrConflictingQueryFilter", err)
			}
		})
	}
}

func TestNewQueryFilterAllowsPossibleMapGroups(t *testing.T) {
	tests := []struct {
		name  string
		group *PredicateGroup
	}{
		{
			name: "NOR excludes another map",
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyMap, Value: "chernarusplus"}},
			},
		},
		{
			name: "OR contains required map",
			group: &PredicateGroup{
				Kind: GroupOR,
				Predicates: []Predicate{
					{Key: KeyMap, Value: "sakhal"},
					{Key: KeyMap, Value: "enoch"},
				},
			},
		},
		{
			name: "OR has another key that may match",
			group: &PredicateGroup{
				Kind: GroupOR,
				Predicates: []Predicate{
					{Key: KeyMap, Value: "chernarusplus"},
					{Key: KeyGameType, Value: "external"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewQueryFilter([]Predicate{{Key: KeyMap, Value: "sakhal"}}, test.group)
			if err != nil {
				t.Fatalf("NewQueryFilter() error = %v, want a potentially matching filter", err)
			}
		})
	}
}

func TestNewQueryFilterAllowsNANDWithNotAllGameTypesRequired(t *testing.T) {
	group := &PredicateGroup{
		Kind: GroupNAND,
		Predicates: []Predicate{
			{Key: KeyGameType, Value: "shardABC123"},
			{Key: KeyGameType, Value: "etm4.000000"},
		},
	}
	conditions := []Predicate{{Key: KeyGameType, Value: "shardABC123"}}

	if _, err := NewQueryFilter(conditions, group); err != nil {
		t.Fatalf("NewQueryFilter() error = %v, want a potentially matching filter", err)
	}
}

func TestNewQueryFilterAllowsORContainingRequiredAppID(t *testing.T) {
	group := &PredicateGroup{
		Kind: GroupOR,
		Predicates: []Predicate{
			{Key: KeyAppID, Value: "730"},
			{Key: KeyAppID, Value: "221100"},
		},
	}

	if _, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, group); err != nil {
		t.Fatalf("NewQueryFilter() error = %v, want a potentially matching filter", err)
	}
}

func TestNewQueryFilterValidatesAppIDAndDoesNotRewriteEquivalentValues(t *testing.T) {
	if _, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "-1"}}, nil); !errors.Is(err, ErrInvalidQueryFilter) {
		t.Fatalf("NewQueryFilter() error = %v, want ErrInvalidQueryFilter", err)
	}

	filter, err := NewQueryFilter([]Predicate{
		{Key: KeyAppID, Value: "0221100"},
		{Key: KeyAppID, Value: "221100"},
	}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() equivalent app IDs error = %v", err)
	}
	if want := `appid\0221100\appid\221100`; filter.String() != want {
		t.Fatalf("String() = %q, want original values %q", filter.String(), want)
	}
}

func TestNewQueryFilterDoesNotInferMapPatternConflicts(t *testing.T) {
	_, err := NewQueryFilter([]Predicate{
		{Key: KeyMap, Value: "chernarus*"},
		{Key: KeyMap, Value: "enoch"},
	}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() pattern conjunction error = %v", err)
	}
}

func TestNewQueryFilterCopiesInputs(t *testing.T) {
	conditions := []Predicate{{Key: KeyAppID, Value: "221100"}}
	groupConditions := []Predicate{{Key: KeyGameType, Value: "shardABC123"}}
	group := &PredicateGroup{Kind: GroupOR, Predicates: groupConditions}
	filter, err := NewQueryFilter(conditions, group)
	if err != nil {
		t.Fatalf("NewQueryFilter() error = %v", err)
	}

	conditions[0].Value = "730"
	groupConditions[0].Value = "changed"
	group.Kind = GroupNOR
	if want := `appid\221100\or\1\gametype\shardABC123`; filter.String() != want {
		t.Fatalf("String() after input mutation = %q, want %q", filter.String(), want)
	}
}

func TestNewQueryFilterKeepsRawBuilderUnrestricted(t *testing.T) {
	filter := NewFilter()
	filter.Add("future_key", "future_value")
	if got, err := filter.String(); err != nil || got != `future_key\future_value` {
		t.Fatalf("raw Filter.String() = %q, %v", got, err)
	}
}
