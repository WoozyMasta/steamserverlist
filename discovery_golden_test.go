// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"
)

func TestDiscoverDayZSteamGoldenCorpus(t *testing.T) {
	// This fixed sample comes from a successful DayZ Steam capture.
	// Its identity fields were replaced while preserving repeated-host grouping.
	servers := readDayZGoldenServers(t)
	if len(servers) != 1000 {
		t.Fatalf("golden fixture has %d servers, want 1000", len(servers))
	}

	reserved := netip.MustParsePrefix("198.18.0.0/15")
	addresses := make(map[netip.AddrPort]struct{}, len(servers))
	hosts := make(map[netip.Addr]struct{}, len(servers))
	for _, server := range servers {
		if server.AppID != 221100 {
			t.Fatalf("golden fixture contains appid %d, want DayZ (221100)", server.AppID)
		}
		if !reserved.Contains(server.Addr.Addr()) {
			t.Fatalf("fixture address %s is outside the anonymized range", server.Addr)
		}
		if !strings.HasPrefix(server.Name, "Anonymized server ") {
			t.Fatalf("fixture contains a non-anonymized server name %q", server.Name)
		}
		if server.SteamID < 9000000000000000000 || server.SteamID >= 9000000000000001000 {
			t.Fatalf("fixture contains a non-synthetic Steam ID")
		}
		if _, exists := addresses[server.Addr]; exists {
			t.Fatalf("fixture contains duplicate endpoint %s", server.Addr)
		}

		addresses[server.Addr] = struct{}{}
		hosts[server.Addr.Addr()] = struct{}{}
	}

	if len(hosts) >= len(servers) {
		t.Fatal("fixture lost repeated-host distribution during anonymization")
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

	var events []DiscoveryEvent
	discovered, err := client.DiscoverLite(context.Background(), root, DiscoveryOptions{
		Limit: 100, MaxRequests: 48, MaxPlanQueries: 24,
		Diagnostics: func(event DiscoveryEvent) { events = append(events, event) },
	})
	if err != nil {
		t.Fatalf("DiscoverLite() error = %v", err)
	}

	if got := len(discovered.Items); got != len(servers) {
		for _, event := range events {
			t.Logf(
				"request %d filter=%q rows=%d new=%d reasons=%v",
				event.Request, event.Filter, event.ReturnedRows, event.NewEndpoints, event.Reason)
		}
		t.Fatalf(
			"DiscoverLite() returned %d endpoints, want %d; status=%+v",
			got, len(servers), discovered.Status)
	}

	assertGoldenEndpointSet(t, servers, discovered.Items)
	if !discovered.Status.Complete() {
		for _, event := range events {
			t.Logf(
				"request %d filter=%q rows=%d reasons=%v",
				event.Request, event.Filter, event.ReturnedRows, event.Reason)
		}
		t.Fatalf("golden discovery status = %+v, want complete", discovered.Status)
	}

	if discovered.Status.Requests <= 1 || discovered.Status.Requests > 48 {
		t.Fatalf("discovery used %d requests, want at most 48", discovered.Status.Requests)
	}
	if leaves := discovered.Plan.QueryCount(); leaves <= 1 || leaves > 24 {
		t.Fatalf("discovery produced %d plan leaves, want 2..24", leaves)
	}

	t.Logf("DayZ golden sample: %d endpoints, %d requests, %d active leaves, %d anonymized hosts",
		len(discovered.Items), discovered.Status.Requests, discovered.Plan.QueryCount(), len(hosts))
	uniqueFilters := make(map[string]struct{}, len(events))

	for _, event := range events {
		uniqueFilters[event.Filter] = struct{}{}
	}
	if len(uniqueFilters) <= 1 {
		t.Fatal("discovery did not issue distinct partition filters")
	}

	collected, err := client.CollectLite(context.Background(), discovered.Plan, CollectOptions{})
	if err != nil {
		t.Fatalf("CollectLite() error = %v", err)
	}
	if len(collected.Items) != len(servers) || !collected.Status.Complete() {
		t.Fatalf(
			"collection returned %d endpoints, status %+v; want all %d",
			len(collected.Items), collected.Status, len(servers))
	}

	assertGoldenEndpointSet(t, servers, collected.Items)
}

func assertGoldenEndpointSet(t *testing.T, expected Servers, actual []ServerLite) {
	t.Helper()

	want := make(map[netip.AddrPort]struct{}, len(expected))
	for _, server := range expected {
		want[server.Addr] = struct{}{}
	}

	for _, server := range actual {
		if _, exists := want[server.Addr]; !exists {
			t.Errorf("unexpected or duplicate endpoint %s", server.Addr)
			continue
		}
		delete(want, server.Addr)
	}

	if len(want) != 0 {
		t.Errorf("result omitted %d golden endpoints", len(want))
	}
}

func readDayZGoldenServers(t *testing.T) Servers {
	t.Helper()

	data, err := os.ReadFile("testdata/dayz_steam_response_1000.json")
	if err != nil {
		t.Fatalf("read anonymized Steam fixture: %v", err)
	}

	var envelope responseEnvelope[rawServerList]
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode anonymized Steam fixture: %v", err)
	}
	if envelope.Response == nil {
		t.Fatal("anonymized Steam fixture has no response envelope")
	}

	servers := make(Servers, 0, len(envelope.Response.Servers))
	for index, raw := range envelope.Response.Servers {
		server, err := raw.server()
		if err != nil {
			t.Fatalf("decode fixture server %d: %v", index, err)
		}
		servers = append(servers, server)
	}

	return servers
}
