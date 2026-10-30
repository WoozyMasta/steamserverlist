// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDiscoveryChildValidationAndFallback(t *testing.T) {
	for _, mode := range []string{"duplicates", "overlap", "missing"} {
		t.Run(mode, func(t *testing.T) {
			servers := discoveryTestServers(6)
			for i := range servers {
				servers[i].Map = "a"
				if i >= 3 {
					servers[i].Map = "b"
				}
			}

			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				filter := r.URL.Query().Get("filter")
				rows, err := filterDiscoveryTestServers(servers, filter)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				if strings.Contains(filter, "nor") {
					switch mode {
					case "overlap":
						rows = append(rows, servers[0])
					case "missing":
						rows = nil
					}
				} else if strings.Contains(filter, "map") && mode == "duplicates" {
					rows = append(rows, rows[0])
				}
				writeDiscoveryTestResponse(w, rows[:min(len(rows), 5)])
			}))
			defer server.Close()

			client, err := New("", WithBaseURL(server.URL))
			if err != nil {
				t.Fatal(err)
			}

			root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
			if err != nil {
				t.Fatal(err)
			}

			result, err := client.DiscoverAddresses(context.Background(), root, DiscoveryOptions{
				Limit:       5,
				MaxRequests: 3,
				Features:    []SplitFeature{{Key: KeyMap}},
			})
			if err != nil || calls != 3 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}

			wantInconsistent := mode != "duplicates"
			if result.Status.Reasons.Has(DiscoveryReasonInconsistent) != wantInconsistent {
				t.Fatalf("status=%+v, want inconsistency=%t", result.Status, wantInconsistent)
			}

			wantLeaves := 1
			if mode == "duplicates" {
				wantLeaves = 2
			}
			if result.Plan.QueryCount() != wantLeaves {
				t.Fatalf("plan=%v", result.Plan.Queries())
			}

			wantItems := 6
			if mode == "missing" {
				wantItems = 5
			}
			if len(result.Items) != wantItems {
				t.Fatalf("items=%d, want %d", len(result.Items), wantItems)
			}
		})
	}
}

func TestCollectionCancellationKeepsLastSuccessfulObservation(t *testing.T) {
	plan := newTestDiscoveryPlan(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rows := discoveryTestServers(1)
		rows[0].Name = "first"
		rows = append(rows, rows[0])
		rows[1].Name = "last"
		writeDiscoveryTestResponse(w, rows)
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Collect(ctx, plan, CollectOptions{Diagnostics: func(event DiscoveryEvent) {
		if event.Request == 1 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancellation", err)
	}
	if len(result.Items) != 1 || result.Items[0].Name != "last" {
		t.Fatalf("partial records=%+v", result.Items)
	}
	if result.Status.Complete() || result.Status.Unresolved != 1 || !result.Status.Reasons.Has(DiscoveryReasonRequestFailure) {
		t.Fatalf("status=%+v", result.Status)
	}
}

func TestDecodeDiscoveryRowsBoundsAndJSONShape(t *testing.T) {
	t.Parallel()
	type row struct {
		Addr string `json:"addr"`
	}
	convert := func(value row, _ int) (string, error) { return value.Addr, nil }

	decoded, err := decodeDiscoveryRows([]byte("null"), convert)
	if err != nil || decoded != nil {
		t.Fatalf("decode null = %v, %v; want nil, nil", decoded, err)
	}
	for _, input := range []string{"", `{}`, `[] {}`, `[] null`} {
		if _, err := decodeDiscoveryRows([]byte(input), convert); !errors.Is(err, ErrDecodeServerList) {
			t.Errorf("decode %q error = %v, want ErrDecodeServerList", input, err)
		}
	}

	rowJSON := `{"addr":"127.0.0.1:27015"}`
	tooMany := "[" + strings.Repeat(rowJSON+",", int(DefaultLimit)) + rowJSON + "]"
	if _, err := decodeDiscoveryRows([]byte(tooMany), convert); !errors.Is(err, ErrDecodeServerList) {
		t.Fatalf("decode %d rows error = %v, want ErrDecodeServerList", DefaultLimit+1, err)
	}
}

func TestDiscoverAndCollectProjectedResults(t *testing.T) {
	servers := discoveryTestServers(12)
	for i := range servers {
		servers[i].Map = "chernarusplus"
		if i >= 7 {
			servers[i].Map = "enoch"
		}
	}
	service := &discoveryTestService{servers: servers}
	httpServer := httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	options := DiscoveryOptions{Limit: 10, MaxRequests: 8, Features: []SplitFeature{{Key: KeyMap, Weight: 1}}}
	result, err := client.Discover(context.Background(), root, options)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := len(result.Items), len(servers); got != want {
		t.Fatalf("Discover() returned %d items, want %d", got, want)
	}
	if !result.Status.Complete() {
		t.Fatalf("Discover() status = %+v, want complete", result.Status)
	}
	if got := result.Plan.QueryCount(); got != 2 {
		t.Fatalf("plan has %d leaves, want 2", got)
	}

	planData, err := json.Marshal(result.Plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}

	var savedPlan DiscoveryPlan
	if err := json.Unmarshal(planData, &savedPlan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}

	collected, err := client.Collect(context.Background(), savedPlan, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got, want := len(collected.Items), len(servers); got != want {
		t.Fatalf("Collect() returned %d items, want %d", got, want)
	}
	if !collected.Status.Complete() {
		t.Fatalf("Collect() status = %+v, want complete", collected.Status)
	}

	lite, err := client.DiscoverLite(context.Background(), root, options)
	if err != nil {
		t.Fatalf("DiscoverLite() error = %v", err)
	}
	if got, want := len(lite.Items), len(servers); got != want {
		t.Fatalf("DiscoverLite() returned %d items, want %d", got, want)
	}

	liteCollection, err := client.CollectLite(context.Background(), lite.Plan, CollectOptions{})
	if err != nil {
		t.Fatalf("CollectLite() error = %v", err)
	}
	if len(liteCollection.Items) != len(servers) || !liteCollection.Status.Complete() {
		t.Fatalf("CollectLite() result = %d, status %+v", len(liteCollection.Items), liteCollection.Status)
	}

	addresses, err := client.DiscoverAddresses(context.Background(), root, options)
	if err != nil {
		t.Fatalf("DiscoverAddresses() error = %v", err)
	}
	if got, want := len(addresses.Items), len(servers); got != want {
		t.Fatalf("DiscoverAddresses() returned %d items, want %d", got, want)
	}
	for index, server := range servers {
		if addresses.Items[index] != server.Addr {
			t.Fatalf("address[%d] = %v, want %v", index, addresses.Items[index], server.Addr)
		}
	}

	addressCollection, err := client.CollectAddresses(context.Background(), addresses.Plan, CollectOptions{})
	if err != nil {
		t.Fatalf("CollectAddresses() error = %v", err)
	}
	if len(addressCollection.Items) != len(servers) || !addressCollection.Status.Complete() {
		t.Fatalf("CollectAddresses() result = %d, status %+v", len(addressCollection.Items), addressCollection.Status)
	}
}

func TestDiscoverVerifiesParentCoverageAcrossRecursiveCappedPages(t *testing.T) {
	var corpus struct {
		Limit            uint32     `json:"limit"`
		RecordCount      int        `json:"record_count"`
		GameTypePatterns [][]string `json:"game_type_patterns"`
	}
	data, err := os.ReadFile("testdata/discovery_cap100.json")
	if err != nil {
		t.Fatalf("read small discovery corpus: %v", err)
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode small discovery corpus: %v", err)
	}

	servers := make(Servers, 0, corpus.RecordCount)
	for index := range corpus.RecordCount {
		gameTypes := corpus.GameTypePatterns[index%len(corpus.GameTypePatterns)]
		servers = append(servers, Server{
			Addr:     netip.AddrPortFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(index + 1)}), 27015),
			AppID:    221100,
			GameType: gameTypes,
		})
	}

	calls := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		filter := r.URL.Query().Get("filter")
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil {
			t.Errorf("parse request limit: %v", err)
			return
		}

		matched := make(Servers, 0, len(servers))
		for _, server := range servers {
			if matchesRecursiveCoverageTestFilter(server, filter) {
				matched = append(matched, server)
			}
		}

		// Rotating each filter's order models capped responses with no shared prefix.
		if len(matched) > 0 {
			offset := 0
			for index := range filter {
				offset = (offset*31 + int(filter[index])) % len(matched)
			}
			ordered := append(Servers(nil), matched[offset:]...)
			ordered = append(ordered, matched[:offset]...)
			matched = ordered
		}

		if len(matched) > limit {
			matched = matched[:limit]
		}

		writeDiscoveryTestResponse(w, matched)
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	initialRecords := make([]discoveryRecord[netip.AddrPort], 10)
	for index := range initialRecords {
		gameType := "tag-a"
		if index%2 == 0 {
			gameType += ", tag-b"
		}
		initialRecords[index].witness = discoveryWitness{
			addr:     servers[index].Addr,
			gameType: gameType,
		}
	}
	if _, ok := chooseDiscoverySplit(initialRecords, root, []SplitFeature{{Key: KeyGameType, Weight: 1}}, 2); !ok {
		t.Fatal("chooseDiscoverySplit() did not select the supported game-type split")
	}

	var events []DiscoveryEvent
	result, err := client.DiscoverAddresses(context.Background(), root, DiscoveryOptions{
		Limit:          corpus.Limit,
		MaxRequests:    7,
		MaxPlanQueries: 4,
		Features:       []SplitFeature{{Key: KeyGameType, Weight: 1}},
		Diagnostics:    func(event DiscoveryEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("DiscoverAddresses() error = %v", err)
	}

	if calls != 7 || len(result.Items) != corpus.RecordCount || !result.Status.Complete() {
		for _, event := range events {
			t.Logf("discovery event: %+v", event)
		}
		t.Fatalf("recursive capped discovery = calls:%d items:%d status:%+v", calls, len(result.Items), result.Status)
	}
	if got := result.Plan.QueryCount(); got != 4 {
		t.Fatalf("saved plan has %d leaves, want 4", got)
	}
}

func TestDiscoverBudgetDoesNotReportUnexploredCoverageAsInconsistent(t *testing.T) {
	servers := make(Servers, 40)
	for index := range servers {
		gameType := "other"
		if index < 20 {
			gameType = "shard"
		}
		servers[index] = Server{
			Addr:     netip.AddrPortFrom(netip.AddrFrom4([4]byte{198, 51, 100, byte(index + 1)}), 27015),
			AppID:    221100,
			GameType: []string{gameType},
		}
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		filter := r.URL.Query().Get("filter")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		var matched Servers
		if filter == root.String() {
			// Return a tail sample absent from each child's capped prefix.
			matched = append(matched, servers[10:15]...)
			matched = append(matched, servers[30:35]...)
		} else {
			var filterErr error
			matched, filterErr = filterDiscoveryTestServers(servers, filter)
			if filterErr != nil {
				http.Error(w, filterErr.Error(), http.StatusBadRequest)
				return
			}
		}

		if len(matched) > limit {
			matched = matched[:limit]
		}

		writeDiscoveryTestResponse(w, matched)
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}

	var events []DiscoveryEvent
	result, err := client.DiscoverAddresses(context.Background(), root, DiscoveryOptions{
		Limit:          10,
		MaxRequests:    3,
		MaxPlanQueries: 4,
		Features:       []SplitFeature{{Key: KeyGameType, Weight: 1}},
		Diagnostics:    func(event DiscoveryEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("DiscoverAddresses() error = %v", err)
	}
	if calls != 3 || result.Status.Complete() {
		t.Fatalf("bounded discovery = calls:%d status:%+v, want 3 calls and incomplete", calls, result.Status)
	}
	if result.Status.Reasons.Has(DiscoveryReasonInconsistent) {
		t.Fatalf("budget-limited child pages were reported inconsistent: %+v", result.Status)
	}
	if !result.Status.Reasons.Has(DiscoveryReasonRequestBudget) || !result.Status.Reasons.Has(DiscoveryReasonSaturated) {
		t.Fatalf("budget-limited status = %+v, want request-budget and saturation reasons", result.Status)
	}
	if got := result.Plan.QueryCount(); got != 2 {
		t.Fatalf("saved plan has %d leaves, want the two validated child branches", got)
	}
	for _, event := range events {
		if len(event.Mismatches) != 0 {
			t.Fatalf("unexplored branch emitted coverage mismatches: %+v", event)
		}
	}
}

func matchesRecursiveCoverageTestFilter(server Server, raw string) bool {
	parts := strings.Split(raw, "\\")
	for index := 0; index < len(parts); {
		if parts[index] == "nor" {
			count, err := strconv.Atoi(parts[index+1])
			if err != nil {
				return false
			}

			for offset := range count {
				key, value := parts[index+2+offset*2], parts[index+3+offset*2]
				if matchesRecursiveCoverageTestCondition(server, key, value) {
					return false
				}
			}

			return true
		}

		if index+1 >= len(parts) || !matchesRecursiveCoverageTestCondition(server, parts[index], parts[index+1]) {
			return false
		}

		index += 2
	}

	return true
}

func matchesRecursiveCoverageTestCondition(server Server, key, value string) bool {
	switch key {
	case "appid":
		return strconv.FormatUint(uint64(server.AppID), 10) == value
	case "map":
		return server.Map == value
	case "gametype":
		return slices.Contains(server.GameType, value)
	case "version_match":
		return server.Version == value
	default:
		return false
	}
}

func TestDiscoverRetainsPartialRecordsOnRequestFailure(t *testing.T) {
	calls := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writeDiscoveryTestResponse(w, discoveryTestServers(12)[:10])
			return
		}
		http.Error(w, "temporary failure", http.StatusServiceUnavailable)
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Discover(context.Background(), root, DiscoveryOptions{
		Limit:       10,
		MaxRequests: 4,
		Features:    []SplitFeature{{Key: KeyMap, Weight: 1}},
	})
	if err == nil {
		t.Fatal("Discover() error = nil, want child request failure")
	}
	if len(result.Items) != 10 || result.Status.Complete() || !result.Status.Reasons.Has(DiscoveryReasonRequestFailure) {
		t.Fatalf("partial discovery = %d records, status %+v", len(result.Items), result.Status)
	}
	if queries := result.Plan.Queries(); len(queries) != 1 {
		t.Fatalf("failed split left %d active queries, want original root query", len(queries))
	}
}

func TestDiscoverRetainsSuccessfulChildPageBeforeSiblingFailure(t *testing.T) {
	servers := discoveryTestServers(12)
	extra := servers[10]
	extra.Map = servers[0].Map
	calls := 0

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		switch calls {
		case 1:
			writeDiscoveryTestResponse(w, servers[:10])
		case 2:
			writeDiscoveryTestResponse(w, Servers{extra})
		default:
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		}
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Discover(context.Background(), root, DiscoveryOptions{Limit: 10, MaxRequests: 4})
	if err == nil {
		t.Fatal("Discover() error = nil, want sibling request failure")
	}
	if len(result.Items) != 11 || result.Plan.QueryCount() != 1 || result.Status.Complete() {
		t.Fatalf("partial child result = %d records, %d plan queries, status %+v", len(result.Items), result.Plan.QueryCount(), result.Status)
	}
}

func TestDiscoverRejectsUnscopedRootBeforeRequest(t *testing.T) {
	client, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Discover(context.Background(), QueryFilter{}, DiscoveryOptions{}); err == nil {
		t.Fatal("Discover() error = nil for an unscoped root")
	}
}

func TestDiscoverQualifiesRootAppIDMismatch(t *testing.T) {
	servers := discoveryTestServers(2)
	for index := range servers {
		servers[index].AppID = 730
	}

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeDiscoveryTestResponse(w, servers)
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Discover(context.Background(), root, DiscoveryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(result.Items) != len(servers) ||
		result.Status.Complete() ||
		result.Status.Unresolved != 1 ||
		!result.Status.Reasons.Has(DiscoveryReasonInconsistent) {
		t.Fatalf("root AppID mismatch result = %+v", result.Status)
	}
	if result.Plan.QueryCount() != 1 {
		t.Fatalf("mismatched root response created %d plan leaves", result.Plan.QueryCount())
	}
}

func TestDiscoveryAddressProjectionUsesEndpointOnlyContract(t *testing.T) {
	addr := netip.MustParseAddrPort("192.0.2.10:27015")
	records, err := decodeDiscoveryAddresses([]byte(`{
	"response":{
		"servers":[{
			"addr":"192.0.2.10:27015",
			"map":"test",
			"name":"discarded"
		}]
	}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].item != addr || records[0].witness.mapName != "test" {
		t.Fatalf("address projection = %+v", records)
	}
}

func TestEmptyTextFeaturesAreKnownNonMatches(t *testing.T) {
	fields := []struct {
		name string
		key  FilterKey
	}{
		{name: "map", key: KeyMap},
		{name: "version", key: KeyVersion},
		{name: "game type", key: KeyGameType},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			values, known := witnessValues(discoveryWitness{}, field.key)
			if !known || len(values) != 0 {
				t.Fatalf("empty value = %v, known %t; want known non-match", values, known)
			}
		})
	}

	records := []discoveryRecord[netip.AddrPort]{
		{witness: discoveryWitness{mapName: "de_dust2"}},
		{witness: discoveryWitness{mapName: "de_dust2"}},
		{witness: discoveryWitness{}},
		{witness: discoveryWitness{}},
	}
	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "10"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := chooseDiscoverySplit(records, root, []SplitFeature{{Key: KeyMap, Weight: 1}}, 2); !ok {
		t.Fatal("empty map rows prevented a fully evidenced exact-map partition")
	}
}

func TestDiscoveryKeepsAbsentBooleanFieldsUnknown(t *testing.T) {
	records, err := decodeDiscoveryLite([]byte(`{
  "response": {
    "servers": [{
      "addr": "192.0.2.10:27015",
      "appid": 221100,
      "map": "test",
      "version": "1",
      "os": "l",
      "gametype": "tag"
    }, {
      "addr": "192.0.2.11:27015",
      "appid": 221100,
      "map": "test",
      "version": "1",
      "os": "l",
      "gametype": "tag",
      "secure": false,
      "dedicated": false
    }]
  }
}`))
	if err != nil {
		t.Fatal(err)
	}
	if records[0].witness.secureKnown || records[0].witness.dedicatedKnown {
		t.Fatalf("absent booleans were treated as known: %+v", records[0].witness)
	}
	if !records[1].witness.secureKnown || !records[1].witness.dedicatedKnown {
		t.Fatalf("explicit false booleans were treated as absent: %+v", records[1].witness)
	}

	linuxValues, linuxKnown := witnessValues(discoveryWitness{os: "w"}, KeyLinux)
	if !linuxKnown || len(linuxValues) != 0 {
		t.Fatalf("Windows OS values = %v, known %t; want known non-match, never linux=0", linuxValues, linuxKnown)
	}

	values, known := witnessValues(records[1].witness, KeySecure)
	if !known || len(values) != 0 {
		t.Fatalf("false secure value = %v, known %t; want a known non-match", values, known)
	}
	_, err = decodeDiscoveryLite([]byte(`{"response":{"servers":[{"addr":"192.0.2.12:27015","secure":"false"}]}}`))
	if !errors.Is(err, ErrInvalidJSONBoolean) {
		t.Fatalf("decode invalid boolean error = %v, want ErrInvalidJSONBoolean", err)
	}
}

func TestDiscoveryRejectsResponseOverRequestedLimit(t *testing.T) {
	servers := discoveryTestServers(2)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeDiscoveryTestResponse(w, servers)
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Discover(context.Background(), root, DiscoveryOptions{Limit: 1})
	if !errors.Is(err, ErrDiscoveryResponseOverLimit) {
		t.Fatalf("Discover() error = %v, want ErrDiscoveryResponseOverLimit", err)
	}
	if len(result.Items) != 0 || !result.Status.Reasons.Has(DiscoveryReasonRequestFailure) {
		t.Fatalf("over-limit response was retained: %d records, status %+v", len(result.Items), result.Status)
	}
}

func TestDiscoveryBufferBudgetStopsBeforeChildRequests(t *testing.T) {
	service := &discoveryTestService{servers: discoveryTestServers(12)}
	httpServer := httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := client.Discover(context.Background(), root, DiscoveryOptions{
		Limit: 10, MaxBufferedRecords: 29, Features: []SplitFeature{{Key: KeyMap, Weight: 1}},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.Status.Requests != 1 || result.Plan.QueryCount() != 1 || !result.Status.Reasons.Has(DiscoveryReasonBufferBudget) {
		t.Fatalf("buffer-limited discovery = %+v, queries=%d", result.Status, result.Plan.QueryCount())
	}
}

func TestDiscoveryNearLimitThreshold(t *testing.T) {
	tests := []struct {
		count int
		limit int
		want  bool
	}{
		{count: 9990, limit: 10000, want: true},
		{count: 9989, limit: 10000},
		{count: 99, limit: 100, want: true},
		{count: 98, limit: 100},
		{count: 1, limit: 1, want: true},
		{count: 0, limit: 1},
		{count: 0, limit: 100},
	}
	for _, test := range tests {
		if got := discoveryNearLimit(test.count, test.limit); got != test.want {
			t.Errorf("discoveryNearLimit(%d, %d) = %t, want %t", test.count, test.limit, got, test.want)
		}
	}
}

func TestCollectKeepsSemanticQualificationFromSavedPlan(t *testing.T) {
	service := &discoveryTestService{servers: discoveryTestServers(2)}
	httpServer := httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	plan := newDiscoveryPlan(root, 10)
	plan.tree.Reasons = DiscoveryReasonInconsistent | DiscoveryReasonNoUsefulSplit
	result, err := client.Collect(context.Background(), plan, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.Status.Unresolved != 0 || !result.Status.Reasons.Has(DiscoveryReasonInconsistent) || result.Status.Complete() {
		t.Fatalf("Collect() status = %+v, want semantic qualification without active unresolved leaf", result.Status)
	}
}

func TestCollectCountsSaturatedAppIDMismatchLeafOnce(t *testing.T) {
	servers := discoveryTestServers(10)
	for index := range servers {
		servers[index].AppID = 730
	}

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeDiscoveryTestResponse(w, servers)
	}))
	defer httpServer.Close()

	client, err := New("", WithBaseURL(httpServer.URL))
	if err != nil {
		t.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	plan := newDiscoveryPlan(root, 10)
	result, err := client.Collect(context.Background(), plan, CollectOptions{})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.Status.Unresolved != 1 ||
		!result.Status.Reasons.Has(DiscoveryReasonSaturated) ||
		!result.Status.Reasons.Has(DiscoveryReasonInconsistent) {
		t.Fatalf("Collect() status = %+v, want one unresolved saturated inconsistent leaf", result.Status)
	}
}

func TestDiscoveryStatusCompleteRequiresSuccessfulDrainedRun(t *testing.T) {
	tests := []struct {
		name   string
		status DiscoveryStatus
		want   bool
	}{
		{
			name:   "drained",
			status: DiscoveryStatus{Requests: 1},
			want:   true,
		},
		{
			name: "empty status",
		},
		{
			name:   "unresolved leaves",
			status: DiscoveryStatus{Requests: 1, Unresolved: 1},
		},
		{
			name:   "reason remains",
			status: DiscoveryStatus{Requests: 1, Reasons: DiscoveryReasonSaturated},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.status.Complete(); got != test.want {
				t.Fatalf("Complete() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDefaultSplitFeaturesReturnsFreshValues(t *testing.T) {
	first := DefaultSplitFeatures()
	wantKeys := []FilterKey{KeyMap, KeyVersion, KeyGameType, KeyLinux, KeySecure, KeyDedicated}
	if len(first) != len(wantKeys) {
		t.Fatalf("DefaultSplitFeatures() returned %d features, want %d", len(first), len(wantKeys))
	}

	for index, key := range wantKeys {
		if first[index].Key != key {
			t.Fatalf("default feature %d = %q, want %q", index, first[index].Key, key)
		}
	}

	first[0].Key = KeyGameAddr
	if second := DefaultSplitFeatures(); second[0].Key != KeyMap {
		t.Fatalf("mutating one result changed the next result's first key to %q", second[0].Key)
	}
}

type discoveryTestService struct {
	servers Servers
	dropNOR bool
}

func (s *discoveryTestService) serveHTTP(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	matched, err := filterDiscoveryTestServers(s.servers, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.dropNOR && strings.Contains(filter, `nor\`) {
		matched = nil
	}
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}

	writeDiscoveryTestResponse(w, matched)
}

func writeDiscoveryTestResponse(w http.ResponseWriter, matched Servers) {
	response := struct {
		Response struct {
			Servers []discoveryTestRecord `json:"servers"`
		} `json:"response"`
	}{}
	for _, server := range matched {
		response.Response.Servers = append(response.Response.Servers, discoveryTestRecord{
			Addr:       server.Addr.String(),
			SteamID:    strconv.FormatUint(server.SteamID, 10),
			AppID:      server.AppID,
			Name:       server.Name,
			Map:        server.Map,
			Version:    server.Version,
			OS:         server.OS,
			GameType:   strings.Join(server.GameType, ", "),
			Players:    server.Players,
			MaxPlayers: server.MaxPlayers,
			Secure:     server.Secure,
			Dedicated:  server.Dedicated,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

type discoveryTestRecord struct {
	Addr       string `json:"addr"`
	SteamID    string `json:"steamid"`
	AppID      uint32 `json:"appid"`
	Name       string `json:"name"`
	Map        string `json:"map"`
	Version    string `json:"version"`
	OS         string `json:"os"`
	GameType   string `json:"gametype"`
	Players    int32  `json:"players"`
	MaxPlayers int32  `json:"max_players"`
	Secure     bool   `json:"secure"`
	Dedicated  bool   `json:"dedicated"`
}

type discoveryTestCondition struct {
	key   string
	value string
}

func filterDiscoveryTestServers(servers Servers, rawFilter string) (Servers, error) {
	conditions, excluded, err := parseDiscoveryTestFilter(rawFilter)
	if err != nil {
		return nil, err
	}

	filtered := make(Servers, 0, len(servers))
	for _, server := range servers {
		matches := true
		for _, condition := range conditions {
			if !matchesDiscoveryTestCondition(server, condition) {
				matches = false
				break
			}
		}

		if !matches {
			continue
		}

		for _, condition := range excluded {
			if matchesDiscoveryTestCondition(server, condition) {
				matches = false
				break
			}
		}

		if matches {
			filtered = append(filtered, server)
		}
	}

	return filtered, nil
}

func parseDiscoveryTestFilter(raw string) ([]discoveryTestCondition, []discoveryTestCondition, error) {
	if raw == "" {
		return nil, nil, nil
	}

	parts := strings.Split(raw, "\\")
	conditions := make([]discoveryTestCondition, 0, len(parts)/2)
	excluded := make([]discoveryTestCondition, 0, 2)
	for i := 0; i < len(parts); {
		if parts[i] == "" {
			return nil, nil, fmt.Errorf("empty filter key at index %d", i)
		}

		if parts[i] == "nor" {
			if i+1 >= len(parts) {
				return nil, nil, errors.New("missing NOR count")
			}

			count, err := strconv.Atoi(parts[i+1])
			if err != nil || count < 0 || i+2+count*2 > len(parts) {
				return nil, nil, errors.New("invalid NOR group")
			}

			for j := range count {
				excluded = append(excluded, discoveryTestCondition{
					key: parts[i+2+j*2], value: parts[i+3+j*2],
				})
			}

			i += 2 + count*2
			continue
		}

		if i+1 >= len(parts) {
			return nil, nil, fmt.Errorf("missing value for %q", parts[i])
		}

		conditions = append(conditions, discoveryTestCondition{key: parts[i], value: parts[i+1]})
		i += 2
	}

	return conditions, excluded, nil
}

func matchesDiscoveryTestCondition(server Server, condition discoveryTestCondition) bool {
	switch condition.key {
	case "appid":
		return strconv.FormatUint(uint64(server.AppID), 10) == condition.value

	case "map":
		return strings.EqualFold(server.Map, condition.value)

	case "version_match":
		return server.Version == condition.value

	case "gametype":
		return slices.Contains(server.GameType, condition.value)

	case "linux":
		return condition.value == "1" && (strings.EqualFold(server.OS, "l") || strings.EqualFold(server.OS, "linux"))

	case "secure":
		return condition.value == "1" && server.Secure

	case "dedicated":
		return condition.value == "1" && server.Dedicated

	case "noplayers":
		return condition.value == "1" && server.Players == 0

	case "notfull":
		return condition.value == "1" && server.MaxPlayers > 0 && server.Players < server.MaxPlayers

	case "gameaddr":
		if !server.Addr.IsValid() {
			return false
		}
		if address, err := netip.ParseAddrPort(condition.value); err == nil {
			return server.Addr == address
		}
		address, err := netip.ParseAddr(condition.value)
		return err == nil && server.Addr.Addr() == address

	default:
		return false
	}
}

func discoveryTestServers(count int) Servers {
	servers := make(Servers, 0, count)
	for i := range count {
		mapName := "chernarusplus"
		if i >= 7 {
			mapName = "enoch"
		}

		version := "1.0.0"
		if i%2 != 0 {
			version = "1.1.0"
		}

		address := netip.AddrFrom4([4]byte{198, 51, 100, byte(i/100 + 1)})
		if i%2 == 0 {
			mapName = strings.ToUpper(mapName)
		}

		servers = append(servers, Server{
			Addr:    netip.AddrPortFrom(address, uint16(2300+i%100)),
			Name:    fmt.Sprintf("Test server %d", i),
			Map:     mapName,
			Version: version,
			OS:      "l",
			SteamID: uint64(i + 1),
			AppID:   221100,
		})
	}

	return servers
}
