// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import "testing"

func TestServersGetVersionMap(t *testing.T) {
	servers := Servers{
		{Version: "1.2.3"},
		{Version: "1.2.3"},
		{Version: "1.2.4"},
		{},
	}

	got := servers.GetVersionMap()
	want := map[string]uint32{
		"":      1,
		"1.2.3": 2,
		"1.2.4": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("GetVersionMap() = %#v, want %#v", got, want)
	}
	for version, count := range want {
		if got[version] != count {
			t.Errorf("GetVersionMap()[%q] = %d, want %d", version, got[version], count)
		}
	}
}

func TestEmptyServersGetVersionMapReturnsEmptyMap(t *testing.T) {
	for _, servers := range []Servers{nil, {}} {
		if got := servers.GetVersionMap(); got == nil || len(got) != 0 {
			t.Errorf("GetVersionMap() = %#v, want non-nil empty map", got)
		}
	}
}
