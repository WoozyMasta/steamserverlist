//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// integrationScenario describes one live filter example.
type integrationScenario struct {
	// name identifies the scenario in verbose test output.
	name string
	// appID is the expected game identifier in every returned record.
	appID uint32
	// configureFilter builds the query using the public filter API.
	configureFilter func(*Filter)
	// limit caps the result size for this scenario.
	limit uint32
}

func TestGetServerListIntegration(t *testing.T) {
	apiKey, keySource := integrationAPIKey(t)
	client, err := New(apiKey)
	if err != nil {
		t.Fatalf("create integration client (error type %T)", err)
	}

	scenarios := []integrationScenario{
		{
			name:  "Counter-Strike 2",
			appID: 730,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "730")
				filter.Add(KeyMap, "de_dust2")
				filter.AddNor(KeyGameType, "insecure")
				filter.AddNor(KeyGameType, "empty")
			},
			limit: 10,
		},
		{
			name:  "Counter-Strike Source",
			appID: 240,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "240")
				filter.Add(KeyMap, "de_dust2")
				filter.Add(KeyGameType, "bots")
			},
			limit: 10,
		},
		{
			name:  "DayZ",
			appID: 221100,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "221100")
				filter.Add(KeyGameType, "battleye")
				filter.AddNor(KeyGameType, "external")
			},
			limit: 10,
		},
		{
			name:  "Arma 3",
			appID: 107410,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "107410")
				filter.Add(KeyName, " [ OFFICIAL ] *")
				filter.Add(KeyGameType, "bt")
				filter.Add(KeyGameType, "dt")
			},
			limit: 10,
		},
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			filter := NewFilter()
			scenario.configureFilter(filter)
			filterString, err := filter.String()
			if err != nil {
				t.Fatalf("build server filter: %v", err)
			}

			servers, err := client.List(ctx, ListQuery{
				Filter: filterString,
				Limit:  scenario.limit,
			})
			if err != nil {
				t.Fatalf("GetServerList request failed (key source: %s): %v", keySource, err)
			}
			if len(servers) == 0 {
				t.Fatal("GetServerList returned no servers for this filter")
			}
			if uint32(len(servers)) > scenario.limit {
				t.Fatalf("returned %d servers for limit %d", len(servers), scenario.limit)
			}

			versions := make(map[string]int)
			for _, server := range servers {
				if server.AppID != scenario.appID {
					t.Fatalf("returned appid %d, want %d", server.AppID, scenario.appID)
				}
				versions[server.Version]++
				t.Logf("game=%s addr=%s name=%q map=%s players=%d/%d tags=%s version=%s",
					server.GameDir,
					server.Addr,
					server.Name,
					server.Map,
					server.Players,
					server.MaxPlayers,
					strings.Join(server.GameType, ","),
					server.Version,
				)
			}

			versionNames := make([]string, 0, len(versions))
			for version := range versions {
				versionNames = append(versionNames, version)
			}
			sort.Strings(versionNames)
			for _, version := range versionNames {
				t.Logf("version=%q servers=%d", version, versions[version])
			}
			t.Logf("returned %d servers (key source: %s)", len(servers), keySource)
		})
	}
}

func TestGetServerListHighLimitProbe(t *testing.T) {
	apiKey, keySource := integrationAPIKey(t)
	client, err := New(apiKey, WithMaxResponseSize(64<<20))
	if err != nil {
		t.Fatalf("create high-limit integration client (error type %T)", err)
	}

	scenarios := []integrationScenario{
		{
			name:  "DayZ",
			appID: 221100,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "221100")
			},
			limit: 50000,
		},
		{
			name:  "Counter-Strike 1.6",
			appID: 10,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "10")
			},
			limit: 50000,
		},
		{
			name:  "Counter-Strike 2",
			appID: 730,
			configureFilter: func(filter *Filter) {
				filter.Add(KeyAppID, "730")
			},
			limit: 50000,
		},
	}

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			filter := NewFilter()
			scenario.configureFilter(filter)
			filterString, err := filter.String()
			if err != nil {
				t.Fatalf("build server filter: %v", err)
			}

			started := time.Now()
			addresses, err := client.ListAddresses(ctx, ListQuery{
				Filter: filterString,
				Limit:  scenario.limit,
			})
			if err != nil {
				t.Fatalf("GetServerList high-limit request failed (key source: %s): %v", keySource, err)
			}

			t.Logf("appid=%d requested_limit=%d returned=%d at_limit=%t duration=%s",
				scenario.appID,
				scenario.limit,
				len(addresses),
				uint32(len(addresses)) >= scenario.limit,
				time.Since(started).Round(time.Millisecond),
			)
		})
	}
}

// integrationAPIKey prefers the environment and then reads only STEAM_API_KEY
// from the module-local .env file without logging its value.
func integrationAPIKey(t *testing.T) (string, string) {
	t.Helper()
	if key := strings.TrimSpace(os.Getenv("STEAM_API_KEY")); key != "" {
		return key, "environment"
	}

	data, err := os.ReadFile(".env")
	if err != nil {
		if !os.IsNotExist(err) {
			t.Fatalf("read integration environment file (error type %T)", err)
		}
		t.Skip("STEAM_API_KEY is not set and .env is absent")
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "STEAM_API_KEY" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') ||
			(value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			return value, ".env"
		}
	}

	t.Skip("STEAM_API_KEY is not set in environment or .env")
	return "", ""
}
