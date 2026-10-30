// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// discoveryBenchmarkPage stores one local service response for cheap replay.
type discoveryBenchmarkPage struct {
	body       []byte
	status     string
	statusCode int
	header     http.Header
}

// discoveryBenchmarkTransport records one warm-up and replays its exact pages.
type discoveryBenchmarkTransport struct {
	base   http.RoundTripper
	pages  map[string]discoveryBenchmarkPage
	replay bool
	calls  int
}

// Discovery benchmark sinks keep one final result live for retained-heap profiles.
var (
	benchmarkDiscoveryFullResult    DiscoveryResult[Server]
	benchmarkDiscoveryLiteResult    DiscoveryResult[ServerLite]
	benchmarkDiscoveryAddressResult DiscoveryResult[netip.AddrPort]
	benchmarkCollectFullResult      Result[Server]
	benchmarkCollectLiteResult      Result[ServerLite]
	benchmarkCollectAddressResult   Result[netip.AddrPort]
)

// RoundTrip records fixture responses before timing and replays them in benchmarks.
func (transport *discoveryBenchmarkTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	key := request.URL.Query().Get("filter") + "\x00" + request.URL.Query().Get("limit")
	if transport.replay {
		page, ok := transport.pages[key]
		if !ok {
			return nil, fmt.Errorf(
				"benchmark has no recorded response for filter %q",
				request.URL.Query().Get("filter"))
		}

		return &http.Response{
			Status:        page.status,
			StatusCode:    page.statusCode,
			Header:        page.header.Clone(),
			Body:          io.NopCloser(bytes.NewReader(page.body)),
			ContentLength: int64(len(page.body)),
			Request:       request,
		}, nil
	}

	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}

	transport.pages[key] = discoveryBenchmarkPage{
		body:       body,
		status:     response.Status,
		statusCode: response.StatusCode,
		header:     response.Header.Clone(),
	}
	response.Body = io.NopCloser(bytes.NewReader(body))

	return response, nil
}

func BenchmarkDecodeResponse100Servers(b *testing.B) {
	body := benchmarkResponse(100)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := decodeResponse(body); err != nil {
			b.Fatal("decode benchmark fixture failed")
		}
	}
}

func BenchmarkDecodeLiteResponse10000Servers(b *testing.B) {
	body := benchmarkResponse(10000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := decodeLiteResponse(body); err != nil {
			b.Fatal("lite decode benchmark fixture failed")
		}
	}
}

func BenchmarkDecodeAddressResponse10000Servers(b *testing.B) {
	body := benchmarkResponse(10000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := decodeAddressResponse(body); err != nil {
			b.Fatal("address decode benchmark fixture failed")
		}
	}
}

func BenchmarkDecodeDiscoveryFull10000Servers(b *testing.B) {
	benchmarkDiscoveryDecode(b, decodeDiscoveryServers)
}

func BenchmarkDecodeDiscoveryLite10000Servers(b *testing.B) {
	benchmarkDiscoveryDecode(b, decodeDiscoveryLite)
}

func BenchmarkDecodeDiscoveryAddress10000Servers(b *testing.B) {
	benchmarkDiscoveryDecode(b, decodeDiscoveryAddresses)
}

func benchmarkDiscoveryDecode[T any](b *testing.B, decode func([]byte) ([]discoveryRecord[T], error)) {
	body := benchmarkResponse(10000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := decode(body); err != nil {
			b.Fatal("discovery decode benchmark fixture failed")
		}
	}
}

func BenchmarkDecodeResponse10000Servers(b *testing.B) {
	body := benchmarkResponse(10000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if _, err := decodeResponse(body); err != nil {
			b.Fatal("full decode benchmark fixture failed")
		}
	}
}

func BenchmarkClientList100ServersLocal(b *testing.B) {
	body := benchmarkResponse(100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		b.Fatal("create local benchmark client")
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := client.List(ctx, ListQuery{Limit: 100}); err != nil {
			b.Fatal("local client benchmark request failed")
		}
	}
}

func BenchmarkDiscoveryProjectionModes3000Servers(b *testing.B) {
	ctx := context.Background()
	servers := discoveryTestServers(3000)
	for index := range servers {
		switch index / 600 {
		case 0:
			servers[index].Map = "map-a"
		case 1:
			servers[index].Map = "map-b"
		case 2:
			servers[index].Map = "map-c"
		case 3:
			servers[index].Map = "map-d"
		default:
			servers[index].Map = "map-e"
		}
	}

	service := &discoveryTestService{servers: servers}
	httpServer := httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	b.Cleanup(httpServer.Close)
	transport := &discoveryBenchmarkTransport{
		base:  http.DefaultTransport,
		pages: make(map[string]discoveryBenchmarkPage),
	}
	client, err := New(
		"",
		WithBaseURL(httpServer.URL),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		b.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		b.Fatal(err)
	}

	options := DiscoveryOptions{
		Limit:          1000,
		MaxRequests:    12,
		MaxPlanQueries: 8,
		Features:       []SplitFeature{{Key: KeyMap, Weight: 1}},
	}
	if _, err := client.DiscoverAddresses(ctx, root, options); err != nil {
		b.Fatal(err)
	}

	httpServer.Close()
	transport.replay = true
	transport.calls = 0
	for _, mode := range []string{"full", "lite", "addresses"} {
		b.Run(mode, func(b *testing.B) {
			var last DiscoveryStatus
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				switch mode {
				case "full":
					result, err := client.Discover(ctx, root, options)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkDiscoveryFullResult = result
					last = result.Status

				case "lite":
					result, err := client.DiscoverLite(ctx, root, options)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkDiscoveryLiteResult = result
					last = result.Status

				case "addresses":
					result, err := client.DiscoverAddresses(ctx, root, options)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkDiscoveryAddressResult = result
					last = result.Status
				}
			}

			b.ReportMetric(float64(last.Requests), "requests/op")
			b.ReportMetric(float64(last.Unresolved), "unresolved/op")
		})
	}
}

func BenchmarkCollectProjectionModes3000Servers(b *testing.B) {
	ctx := context.Background()
	servers := discoveryTestServers(3000)
	for index := range servers {
		servers[index].Map = fmt.Sprintf("map-%d", index/600)
	}

	service := &discoveryTestService{servers: servers}
	httpServer := httptest.NewServer(http.HandlerFunc(service.serveHTTP))
	b.Cleanup(httpServer.Close)

	transport := &discoveryBenchmarkTransport{
		base:  http.DefaultTransport,
		pages: make(map[string]discoveryBenchmarkPage),
	}

	client, err := New(
		"",
		WithBaseURL(httpServer.URL),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		b.Fatal(err)
	}

	root, err := NewQueryFilter([]Predicate{{Key: KeyAppID, Value: "221100"}}, nil)
	if err != nil {
		b.Fatal(err)
	}

	discovered, err := client.DiscoverAddresses(ctx, root, DiscoveryOptions{
		Limit:          1000,
		MaxRequests:    12,
		MaxPlanQueries: 8,
		Features:       []SplitFeature{{Key: KeyMap, Weight: 1}},
	})
	if err != nil {
		b.Fatal(err)
	}

	httpServer.Close()
	transport.replay = true
	transport.calls = 0
	for _, mode := range []string{"full", "lite", "addresses"} {
		b.Run(mode, func(b *testing.B) {
			var last DiscoveryStatus
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				switch mode {
				case "full":
					result, err := client.Collect(ctx, discovered.Plan, CollectOptions{})
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCollectFullResult = result
					last = result.Status

				case "lite":
					result, err := client.CollectLite(ctx, discovered.Plan, CollectOptions{})
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCollectLiteResult = result
					last = result.Status

				case "addresses":
					result, err := client.CollectAddresses(ctx, discovered.Plan, CollectOptions{})
					if err != nil {
						b.Fatal(err)
					}
					benchmarkCollectAddressResult = result
					last = result.Status
				}
			}

			b.ReportMetric(float64(last.Requests), "requests/op")
			b.ReportMetric(float64(len(discovered.Plan.Queries())), "plan-leaves/op")
		})
	}
}

func BenchmarkFilterBuildAndEncodeArma3(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		filter := &Filter{}
		filter.Add(KeyAppID, "107410")
		filter.Add(KeyName, "\xb6*")
		filter.Add(KeyGameType, "bt")
		filter.Add(KeyGameType, "dt")
		if _, err := filter.String(); err != nil {
			b.Fatal("encode Arma 3 filter")
		}
	}
}

func BenchmarkFilterBuildAndEncodeDayZ(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		filter := &Filter{}
		filter.Add(KeyAppID, "221100")
		filter.Add(KeyGameType, "battleye")
		filter.AddNor(KeyGameType, "external")
		if _, err := filter.String(); err != nil {
			b.Fatal("encode DayZ filter")
		}
	}
}

func BenchmarkFilterBuildAndEncodeSingleCondition(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		filter := &Filter{}
		filter.Add(KeyAppID, "221100")
		if _, err := filter.String(); err != nil {
			b.Fatal("encode single-condition filter")
		}
	}
}

func BenchmarkFilterString32Conditions(b *testing.B) {
	filter := NewFilter()
	for i := range 32 {
		filter.Add(KeyGameType, fmt.Sprintf("tag-%d", i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := filter.String(); err != nil {
			b.Fatal("encode multi-condition filter")
		}
	}
}

// benchmarkResponse builds deterministic local JSON fixtures;
// benchmarks never contact Steam or consume API quota.
func benchmarkResponse(count int) []byte {
	body := make([]byte, 0, count*220+32)
	body = append(body, `{"response":{"servers":[`...)
	for i := range count {
		if i > 0 {
			body = append(body, ',')
		}
		body = fmt.Appendf(body, `{
	"addr":"192.0.2.%d:27015",
	"steamid":"%d",
	"appid":221100,
	"name":"Benchmark server %d",
	"gamedir":"dayz",
	"map":"chernarusplus",
	"product":"dayz",
	"version":"1.28",
	"os":"l",
	"gametype":"battleye, lqs0",
	"players":3,
	"max_players":60,
	"bots":0,
	"gameport":2302,
	"specport":0,
	"dedicated":true,
	"secure":true
}`, i%254+1, 76561198000000000+i, i)
	}

	return append(body, ']', '}', '}')
}
