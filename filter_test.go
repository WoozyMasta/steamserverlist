// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"errors"
	"testing"
)

func TestFilterBuildsArma3Query(t *testing.T) {
	filter := &Filter{}
	filter.Add(KeyAppID, "107410")
	filter.Add(KeyName, "\xb6*")
	filter.Add(KeyGameType, "bt")
	filter.Add(KeyGameType, "dt")

	got, err := filter.String()
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	want := `appid\107410\name_match\` + "\xb6*" + `\gametype\bt\gametype\dt`
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFilterBuildsDayZQuery(t *testing.T) {
	filter := NewFilter()
	filter.Add(KeyAppID, "221100")
	filter.Add(KeyGameType, "battleye")
	filter.AddNor(KeyGameType, "external")

	got, err := filter.String()
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	want := `appid\221100\gametype\battleye\nor\1\gametype\external`
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFilterBuildsNANDQueryAfterRemoval(t *testing.T) {
	filter := &Filter{}
	filter.Add(KeyAppID, "730")
	filter.AddNand(KeyGameType, "insecure")
	filter.AddNand(KeyGameType, "empty")
	filter.RemoveNand(KeyGameType, "insecure")
	filter.AddNand(KeyGameType, "external")

	got, err := filter.String()
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	want := `appid\730\nand\2\gametype\empty\gametype\external`
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFilterRejectsCombiningNORAndNAND(t *testing.T) {
	filter := NewFilter()
	filter.AddNor(KeyGameType, "external")
	filter.AddNand(KeyGameType, "empty")

	if _, err := filter.String(); !errors.Is(err, ErrFilterCannotCombineNORAndNAND) {
		t.Fatalf("String() error = %v, want ErrFilterCannotCombineNORAndNAND", err)
	}
}

func TestFilterRemoveDeletesOnlyFirstMatch(t *testing.T) {
	filter := NewFilter()
	filter.Add(KeyGameType, "battleye")
	filter.Add(KeyGameType, "battleye")
	filter.Remove(KeyGameType, "battleye")

	got, err := filter.String()
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if want := `gametype\battleye`; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestFilterCloneIsIndependent(t *testing.T) {
	filter := NewFilter()
	filter.Add(KeyAppID, "221100")
	filter.AddNor(KeyMap, "chernarusplus")

	cloned := filter.Clone()
	cloned.Add(KeyMap, "enoch")
	cloned.RemoveNor(KeyMap, "chernarusplus")

	original, err := filter.String()
	if err != nil {
		t.Fatalf("original String() error = %v", err)
	}
	clone, err := cloned.String()
	if err != nil {
		t.Fatalf("clone String() error = %v", err)
	}
	if original == clone {
		t.Fatalf("Clone() did not isolate mutations: both filters are %q", original)
	}
	if want := `appid\221100\nor\1\map\chernarusplus`; original != want {
		t.Fatalf("original filter = %q, want %q", original, want)
	}
}
