// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte(`{"response":{"servers":[]}}`))
	f.Add([]byte(`{
	"response": {
		"servers": [{
			"addr": "192.0.2.1:27015",
			"steamid": "76561198000000001",
			"gametype": "battleye, lqs0"
		}]
	}
}`))
	f.Add([]byte(`{
	"response": {
		"servers": [{
			"addr": "[2001:db8::1]:27015",
			"steamid": 76561198000000001
		}]
	}
}`))
	f.Add([]byte(`{
	"response": {
		"servers": [{
			"addr": "not-an-address",
			"steamid": "1"
		}]
	}
}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		servers, err := decodeResponse(data)
		if err != nil {
			return
		}

		for i, server := range servers {
			if !server.Addr.IsValid() {
				t.Fatalf("decoded server %d has invalid address", i)
			}
			for _, tag := range server.GameType {
				if tag == "" || strings.TrimSpace(tag) != tag {
					t.Fatalf("decoded server %d has invalid game-type tag %q", i, tag)
				}
			}
		}
	})
}

func FuzzParseSteamID(f *testing.F) {
	f.Add([]byte(`"76561198000000001"`))
	f.Add([]byte(`18446744073709551615`))
	f.Add([]byte(`null`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`1.5`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := parseSteamID(raw)
		if err != nil {
			return
		}

		encoded, err := json.Marshal(strconv.FormatUint(got, 10))
		if err != nil {
			t.Fatalf("marshal parsed SteamID: %v", err)
		}
		roundTrip, err := parseSteamID(encoded)
		if err != nil || roundTrip != got {
			t.Fatalf("SteamID round trip = %d, %v; want %d", roundTrip, err, got)
		}
	})
}

func FuzzServerAddress(f *testing.F) {
	f.Add("192.0.2.1:27015")
	f.Add("[2001:db8::1]:27015")
	f.Add("not-an-address")
	f.Add("")

	f.Fuzz(func(t *testing.T, address string) {
		server, err := (rawServer{Addr: address}).server()
		if err == nil && !server.Addr.IsValid() {
			t.Fatalf("accepted address %q but produced an invalid endpoint", address)
		}
	})
}

func FuzzDecodeDiscoveryProjections(f *testing.F) {
	f.Add([]byte(`{"response":{"servers":[]}}`))
	f.Add([]byte(`{
	"response": {
		"servers": [{
			"addr": "192.0.2.1:27015",
			"appid": 221100,
			"map": "chernarusplus",
			"gametype": "battleye"
		}]
	}
}`))
	f.Add([]byte(`{"response":{"servers":[{"addr":"not-an-address","appid":221100}]}}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		full, fullErr := decodeDiscoveryServers(data)
		lite, liteErr := decodeDiscoveryLite(data)
		addresses, addressesErr := decodeDiscoveryAddresses(data)

		if fullErr == nil {
			for i, record := range full {
				if !record.item.Addr.IsValid() || record.item.Addr != record.witness.addr {
					t.Fatalf("full record %d has inconsistent address", i)
				}
			}
		}
		if liteErr == nil {
			for i, record := range lite {
				if !record.item.Addr.IsValid() || record.item.Addr != record.witness.addr {
					t.Fatalf("lite record %d has inconsistent address", i)
				}
			}
		}
		if addressesErr == nil {
			for i, record := range addresses {
				if !record.item.IsValid() || record.item != record.witness.addr {
					t.Fatalf("address record %d has inconsistent address", i)
				}
			}
		}
	})
}

func FuzzDiscoveryPlanJSON(f *testing.F) {
	filter, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		f.Fatal(err)
	}
	validPlan, err := json.Marshal(newDiscoveryPlan(filter, 100))
	if err != nil {
		f.Fatal(err)
	}

	f.Add(validPlan)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"tree":{"active":true},"root":{},"limit":0}`))
	f.Add([]byte(`{
	"tree": {
		"active": true
	},
	"root": {
		"conditions": [{
			"key": "unknown",
			"value": "x"
		}]
	},
	"limit": 100
}`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var plan DiscoveryPlan
		if err := json.Unmarshal(data, &plan); err != nil {
			return
		}
		if err := plan.Validate(); err != nil {
			t.Fatalf("accepted plan failed validation: %v", err)
		}

		encoded, err := json.Marshal(plan)
		if err != nil {
			t.Fatalf("marshal accepted plan: %v", err)
		}
		var roundTrip DiscoveryPlan
		if err := json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatalf("unmarshal round-trip plan: %v", err)
		}
	})
}
