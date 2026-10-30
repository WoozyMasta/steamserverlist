// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDiscoveryPlanRoundTripAndDerivesQueries(t *testing.T) {
	plan := newTestDiscoveryPlan(t)
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded DiscoveryPlan
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	queries := decoded.Queries()
	if len(queries) != 2 {
		t.Fatalf("queries() returned %d queries, want 2", len(queries))
	}
	if got := decoded.QueryCount(); got != len(queries) {
		t.Fatalf("QueryCount() = %d, want %d", got, len(queries))
	}

	want := []string{
		`appid\221100\map\sakhal\gametype\shardABC123`,
		`appid\221100\map\sakhal\nor\1\gametype\shardABC123`,
	}
	for index, query := range queries {
		if query.Filter != want[index] {
			t.Errorf("query[%d].Filter = %q, want %q", index, query.Filter, want[index])
		}
		if query.Limit != 1000 {
			t.Errorf("query[%d].Limit = %d, want 1000", index, query.Limit)
		}
	}

	queries[0].Filter = "mutated"
	if got := decoded.Queries()[0].Filter; got != want[0] {
		t.Fatalf("mutating returned query changed plan filter to %q", got)
	}
}

func TestNewDiscoveryPlanStartsWithOneExecutableRoot(t *testing.T) {
	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() error = %v", err)
	}

	plan := newDiscoveryPlan(root, 100)
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := plan.QueryCount(); got != 1 {
		t.Fatalf("QueryCount() = %d, want 1", got)
	}
	if got := plan.Queries()[0].Filter; got != `appid\221100` {
		t.Fatalf("root query = %q, want appid filter", got)
	}
}

func TestDiscoveryNORContinuationUsesOnlyExplicitRestrictions(t *testing.T) {
	tests := []struct {
		name       string
		conditions []Predicate
		group      *PredicateGroup
		predicate  Predicate
		wantError  bool
	}{
		{
			name:      "first complement predicate",
			predicate: Predicate{Key: KeyMap, Value: "sakhal"},
		},
		{
			name: "more than six arbitrary flat predicates",
			group: &PredicateGroup{
				Kind: GroupNOR,
				Predicates: []Predicate{
					{Key: KeyMap, Value: "chernarusplus"},
					{Key: KeyMap, Value: "sakhal"},
					{Key: KeyGameType, Value: "tag-1"},
					{Key: KeyGameType, Value: "tag-2"},
					{Key: KeyGameType, Value: "tag-3"},
					{Key: KeyGameType, Value: "tag-4"},
					{Key: KeyGameType, Value: "tag-5"},
				},
			},
			predicate: Predicate{Key: KeyVersion, Value: "1.29"},
		},
		{
			name: "duplicate NOR predicate",
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyGameType, Value: "tag-1"}},
			},
			predicate: Predicate{Key: KeyGameType, Value: "tag-1"},
			wantError: true,
		},
		{
			name: "non-partition key",
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyGameType, Value: "tag-1"}},
			},
			predicate: Predicate{Key: KeyName, Value: "server"},
			wantError: true,
		},
		{
			name:       "positive map excluded by NOR",
			conditions: []Predicate{{Key: KeyMap, Value: "sakhal"}},
			group: &PredicateGroup{
				Kind:       GroupNOR,
				Predicates: []Predicate{{Key: KeyGameType, Value: "tag-1"}},
			},
			predicate: Predicate{Key: KeyMap, Value: "sakhal"},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filter, err := NewQueryFilter(test.conditions, test.group)
			if err != nil {
				t.Fatalf("NewQueryFilter() error = %v", err)
			}
			_, err = filter.withNORPredicate(test.predicate)
			if (err != nil) != test.wantError {
				t.Fatalf("withNORPredicate() error = %v, want error %t", err, test.wantError)
			}
		})
	}
}

func TestDiscoveryMixedNORFixtureMatchesObservedComplement(t *testing.T) {
	type fixtureServer struct {
		Addr     string   `json:"addr"`
		Map      string   `json:"map"`
		GameType []string `json:"gametype"`
	}
	var fixture struct {
		MapValue       string          `json:"map_value"`
		GameTypeValues []string        `json:"gametype_values"`
		Baseline       []fixtureServer `json:"baseline"`
		MixedNORAddrs  []string        `json:"mixed_nor_addresses"`
	}

	data, err := os.ReadFile("testdata/discovery_mixed_nor.json")
	if err != nil {
		t.Fatalf("read minimized live fixture: %v", err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode minimized live fixture: %v", err)
	}

	expected := make(map[netip.AddrPort]struct{}, len(fixture.Baseline))
	for _, server := range fixture.Baseline {
		matchesTag := false
		for _, value := range fixture.GameTypeValues {
			matchesTag = matchesTag || slices.Contains(server.GameType, value)
		}
		if strings.EqualFold(server.Map, fixture.MapValue) || matchesTag {
			continue
		}

		address, err := netip.ParseAddrPort(server.Addr)
		if err != nil {
			t.Fatalf("parse baseline address %q: %v", server.Addr, err)
		}
		expected[address] = struct{}{}
	}

	actual := make(map[netip.AddrPort]struct{}, len(fixture.MixedNORAddrs))
	for _, rawAddress := range fixture.MixedNORAddrs {
		address, err := netip.ParseAddrPort(rawAddress)
		if err != nil {
			t.Fatalf("parse NOR address %q: %v", rawAddress, err)
		}
		actual[address] = struct{}{}
	}

	if len(actual) != len(fixture.MixedNORAddrs) || len(actual) != len(expected) {
		t.Fatalf("mixed NOR set sizes: actual=%d expected=%d", len(actual), len(expected))
	}
	for address := range expected {
		if _, ok := actual[address]; !ok {
			t.Errorf("mixed NOR response omitted expected address %s", address)
		}
	}
	for address := range actual {
		if _, ok := expected[address]; !ok {
			t.Errorf("mixed NOR response returned unexpected address %s", address)
		}
	}
}

func TestDiscoveryPlanUnmarshalIsTransactional(t *testing.T) {
	plan := newTestDiscoveryPlan(t)
	before, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if err := json.Unmarshal([]byte(`{"version":1}`), &plan); !errors.Is(err, ErrInvalidDiscoveryPlan) {
		t.Fatalf("Unmarshal() error = %v, want ErrInvalidDiscoveryPlan", err)
	}
	after, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal() after rejected input error = %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("Unmarshal() changed the receiver after rejecting the document")
	}
}

func TestDiscoveryPlanRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "unknown legacy field",
			data: `{
  "version": 1,
  "near_cap_rule": 1,
  "limit": 100,
  "root": {},
  "tree": {
    "active": true
  }
}`,
		},
		{
			name: "unknown field",
			data: `{
  "limit": 100,
  "root": {
    "conditions": [{
      "key": "appid",
      "value": "221100"
    }]
  },
  "tree": {
    "active": true
  },
  "complete": true
}`,
		},
		{
			name: "missing split child",
			data: `{
  "limit": 100,
  "root": {
    "conditions": [{
      "key": "appid",
      "value": "221100"
    }]
  },
  "tree": {
    "split": {
      "key": "map",
      "value": "sakhal"
    },
    "match": {
      "active": true
    }
  }
}`,
		},
		{
			name: "unsupported discovery root group",
			data: `{
  "limit": 100,
  "root": {
    "conditions": [{
      "key": "appid",
      "value": "221100"
    }],
    "group": {
      "kind": "or",
      "predicates": [{
        "key": "map",
        "value": "sakhal"
      }]
    }
  },
  "tree": {
    "active": true
  }
}`,
		},
		{
			name: "collapsing root filter",
			data: `{
  "limit": 100,
  "root": {
    "conditions": [{
      "key": "collapse_addr_hash",
      "value": "1"
    }]
  },
  "tree": {
    "active": true
  }
}`,
		},
		{
			name: "unsupported split key",
			data: `{
  "limit": 100,
  "root": {
    "conditions": [{
      "key": "appid",
      "value": "221100"
    }]
  },
  "tree": {
    "split": {
      "key": "gameaddr",
      "value": "127.0.0.1"
    },
    "match": {
      "active": true
    },
    "non_match": {
      "active": true
    }
  }
}`,
		},
		{
			name: "unscoped root",
			data: `{
  "limit": 100,
  "root": {},
  "tree": {
    "active": true
  }
}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var plan DiscoveryPlan
			if err := json.Unmarshal([]byte(test.data), &plan); !errors.Is(err, ErrInvalidDiscoveryPlan) {
				t.Fatalf("Unmarshal() error = %v, want ErrInvalidDiscoveryPlan", err)
			}
		})
	}
}

func TestDiscoveryPlanRejectsSizeAndNestingLimits(t *testing.T) {
	var plan DiscoveryPlan
	tooLarge := `{"padding":"` + strings.Repeat("x", maxDiscoveryPlanBytes) + `"}`
	if err := json.Unmarshal([]byte(tooLarge), &plan); !errors.Is(err, ErrInvalidDiscoveryPlan) {
		t.Fatalf("oversized Unmarshal() error = %v, want ErrInvalidDiscoveryPlan", err)
	}

	deepJSON := strings.Repeat("[", maxDiscoveryDepth+1) + "0" +
		strings.Repeat("]", maxDiscoveryDepth+1)
	if err := json.Unmarshal([]byte(deepJSON), &plan); !errors.Is(err, ErrInvalidDiscoveryPlan) {
		t.Fatalf("deep Unmarshal() error = %v, want ErrInvalidDiscoveryPlan", err)
	}
}

func TestDiscoveryPlanRejectsExcessiveTreeDepth(t *testing.T) {
	rootFilter, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() error = %v", err)
	}

	node := &discoveryPlanNode{Active: true}
	for depth := 0; depth <= maxDiscoveryDepth; depth++ {
		node = &discoveryPlanNode{
			Split:    &Predicate{Key: KeyGameType, Value: fmt.Sprintf("tag-%d", depth)},
			Match:    node,
			NonMatch: &discoveryPlanNode{Active: true},
		}
	}
	plan := DiscoveryPlan{
		root:  rootFilter,
		tree:  node,
		limit: 100,
	}
	if err := plan.Validate(); !errors.Is(err, ErrInvalidDiscoveryPlan) {
		t.Fatalf("validate() error = %v, want ErrInvalidDiscoveryPlan", err)
	}
}

func newTestDiscoveryPlan(t *testing.T) DiscoveryPlan {
	t.Helper()
	root, err := NewQueryFilter([]Predicate{
		{Key: KeyAppID, Value: "221100"},
		{Key: KeyMap, Value: "sakhal"},
	}, nil)
	if err != nil {
		t.Fatalf("NewQueryFilter() error = %v", err)
	}

	return DiscoveryPlan{
		root: root,
		tree: &discoveryPlanNode{
			Split:    &Predicate{Key: KeyGameType, Value: "shardABC123"},
			Match:    &discoveryPlanNode{Active: true},
			NonMatch: &discoveryPlanNode{Active: true, Reasons: DiscoveryReasonNoUsefulSplit},
		},
		limit: 1000,
	}
}
