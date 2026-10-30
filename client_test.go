// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// providerFunc adapts a function to the Provider interface for focused tests.
type providerFunc func(context.Context, ListQuery) ([]Server, error)

// List calls the wrapped provider function.
func (f providerFunc) List(ctx context.Context, query ListQuery) ([]Server, error) {
	return f(ctx, query)
}

func TestListRejectsUninitializedClientAndNilContext(t *testing.T) {
	var client *Client
	if _, err := client.List(context.Background(), ListQuery{}); !errors.Is(err, ErrClientUninitialized) {
		t.Fatalf("nil client error = %v, want ErrClientUninitialized", err)
	}

	client, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var nilContext context.Context
	if _, err := client.List(nilContext, ListQuery{}); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context error = %v, want ErrNilContext", err)
	}
}

func TestListRedactsAPIKeyFromTransportError(t *testing.T) {
	const apiKey = "private-test-key"
	wantCause := errors.New("transport sentinel")
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: wantCause}
	})

	client, err := New(apiKey,
		WithBaseURL("https://example.invalid/list"),
		WithHTTPClient(&http.Client{Transport: transport}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.List(context.Background(), ListQuery{})
	if !errors.Is(err, wantCause) {
		t.Fatal("transport cause was not preserved")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatal("transport error exposed API key")
	}
}

func TestListReplacesBaseURLAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys := r.URL.Query()["key"]
		if len(keys) != 1 || keys[0] != "configured-key" {
			t.Errorf("key query values = %#v, want only configured key", keys)
		}
		_, _ = w.Write([]byte(`{"response":{"servers":[]}}`))
	}))
	defer server.Close()

	client, err := New("configured-key", WithBaseURL(server.URL+"?key=stale-key&key=duplicate"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.List(context.Background(), ListQuery{}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

func TestListLiteDecodesOnlyDiscoveryFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
	"response":{
		"servers":[{
			"addr":"203.0.113.4:2302",
			"map":"chernarusplus",
			"version":"1.28",
			"os":"l",
			"gametype":"battleye, lqs0",
			"players":4,
			"max_players":60,
			"dedicated":true,
			"secure":true,
			"steamid":"not-needed",
			"name":"not-needed"
		}]
	}
}`))
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := client.ListLite(context.Background(), ListQuery{})
	if err != nil {
		t.Fatalf("ListLite() error = %v", err)
	}

	want := []ServerLite{{
		Addr:       netip.MustParseAddrPort("203.0.113.4:2302"),
		Map:        "chernarusplus",
		Version:    "1.28",
		OS:         "l",
		GameType:   []string{"battleye", "lqs0"},
		Players:    4,
		MaxPlayers: 60,
		Dedicated:  true,
		Secure:     true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListLite() = %#v, want %#v", got, want)
	}
}

func TestListAddressesReturnsOnlyParsedAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
	"response":{
		"servers":[
			{"addr":"203.0.113.4:2302","steamid":"not-needed"},
			{"addr":"[2001:db8::1]:27015","name":"not-needed"}
		]
	}
}`))
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := client.ListAddresses(context.Background(), ListQuery{})
	if err != nil {
		t.Fatalf("ListAddresses() error = %v", err)
	}
	want := []netip.AddrPort{
		netip.MustParseAddrPort("203.0.113.4:2302"),
		netip.MustParseAddrPort("[2001:db8::1]:27015"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListAddresses() = %#v, want %#v", got, want)
	}
}

func TestProjectedListMethodsRejectInvalidAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"servers":[{"addr":"missing-port"}]}}`))
	}))
	defer server.Close()

	client, err := New("", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name string
		list func() error
	}{
		{
			name: "lite",
			list: func() error {
				_, err := client.ListLite(context.Background(), ListQuery{})
				return err
			},
		},
		{
			name: "addresses",
			list: func() error {
				_, err := client.ListAddresses(context.Background(), ListQuery{})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.list()
			if !errors.Is(err, ErrInvalidServerAddress) || !errors.Is(err, ErrDecodeServerRecord) {
				t.Fatalf("list error = %v, want invalid server record address", err)
			}
		})
	}
}

func TestParseSteamIDSupportsStringAndJSONInteger(t *testing.T) {
	for _, raw := range []string{`"18446744073709551615"`, `18446744073709551615`} {
		got, err := parseSteamID([]byte(raw))
		if err != nil || got != ^uint64(0) {
			t.Fatalf("parseSteamID(%s) = %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"null", `""`, `"+1"`, `-1`, `1.5`, `true`} {
		if _, err := parseSteamID([]byte(raw)); !errors.Is(err, ErrInvalidSteamID) {
			t.Fatalf("parseSteamID(%s) error = %v, want ErrInvalidSteamID", raw, err)
		}
	}
}

func TestClientOptionValidation(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want error
	}{
		{
			name: "nil HTTP client",
			opt:  WithHTTPClient(nil),
			want: ErrNilHTTPClient,
		},
		{
			name: "invalid URL",
			opt:  WithBaseURL("https://user:pass@example.invalid"),
			want: ErrInvalidBaseURL,
		},
		{
			name: "unsupported scheme",
			opt:  WithBaseURL("ftp://example.invalid"),
			want: ErrUnsupportedBaseURLScheme,
		},
		{
			name: "zero body limit",
			opt:  WithMaxResponseSize(0),
			want: ErrMaxResponseSizeMustBePositive,
		},
		{
			name: "overflowing body limit",
			opt:  WithMaxResponseSize(int64(^uint64(0) >> 1)),
			want: ErrMaxResponseSizeOverflows,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New("", tt.opt); !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want errors.Is(_, %v)", err, tt.want)
			}
		})
	}
}

func TestParseGameTypesTrimsAndDropsEmptyTags(t *testing.T) {
	got := parseGameTypes(" , battleye, , lqs0 ,, ")
	want := []string{"battleye", "lqs0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseGameTypes() = %#v, want %#v", got, want)
	}
	if got := parseGameTypes(" , , "); got != nil {
		t.Fatalf("parseGameTypes() for empty tags = %#v, want nil", got)
	}
}

func TestDecodeResponseParsesGameTypeTags(t *testing.T) {
	servers, err := decodeResponse([]byte(`
{"response":{"servers":[{"addr":"192.0.2.1:27015","gametype":"battleye, lqs0"}]}}`))
	if err != nil {
		t.Fatalf("decodeResponse() error = %v", err)
	}
	want := []string{"battleye", "lqs0"}
	if !reflect.DeepEqual(servers[0].GameType, want) {
		t.Fatalf("GameType = %#v, want %#v", servers[0].GameType, want)
	}
}

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper for a test function.
func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientAndHandler(t *testing.T) {
	wantQuery := ListQuery{Filter: `\appid\221100`, Limit: 42}
	wantServers := Servers{{
		Addr:       netip.MustParseAddrPort("192.0.2.1:27015"),
		SteamID:    76561198000000001,
		AppID:      221100,
		Name:       "Test server",
		GameType:   []string{"battleye", "lqs0"},
		Players:    3,
		MaxPlayers: 60,
		Secure:     true,
	}}

	server := httptest.NewServer(NewHandler(providerFunc(func(_ context.Context, query ListQuery) ([]Server, error) {
		if !reflect.DeepEqual(query, wantQuery) {
			t.Errorf("provider query = %#v, want %#v", query, wantQuery)
		}
		return wantServers, nil
	}), WithRequiredKey("secret")))
	defer server.Close()

	client, err := New("secret", WithBaseURL(server.URL))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := client.List(context.Background(), wantQuery)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, wantServers) {
		t.Fatalf("List() = %#v, want %#v", got, wantServers)
	}
}

func TestClientErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		maxBody  int64
		wantHTTP bool
		wantIs   error
		wantAlso error
	}{
		{
			name:     "status",
			status:   http.StatusBadGateway,
			body:     "upstream",
			wantHTTP: true,
		},
		{
			name:   "malformed JSON",
			status: http.StatusOK,
			body:   `{`,
			wantIs: ErrDecodeResponse,
		},
		{
			name:   "missing response",
			status: http.StatusOK,
			body:   `{"other":true}`,
			wantIs: ErrMissingResponseEnvelope,
		},
		{
			name:     "missing address",
			status:   http.StatusOK,
			body:     `{"response":{"servers":[{"steamid":"1"}]}}`,
			wantIs:   ErrDecodeServerRecord,
			wantAlso: ErrInvalidServerAddress,
		},
		{
			name:   "malformed server list",
			status: http.StatusOK,
			body:   `{"response":{"servers":{}}}`,
			wantIs: ErrDecodeServerList,
		},
		{
			name:    "oversized",
			status:  http.StatusOK,
			body:    `{"response":{"servers":[]}}`,
			maxBody: 1,
			wantIs:  ErrResponseTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			opts := []Option{WithBaseURL(server.URL)}
			if tt.maxBody != 0 {
				opts = append(opts, WithMaxResponseSize(tt.maxBody))
			}

			client, err := New("", opts...)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.List(context.Background(), ListQuery{})
			if err == nil {
				t.Fatal("List() error = nil, want error")
			}

			var httpErr *HTTPError
			if errors.As(err, &httpErr) != tt.wantHTTP {
				t.Fatalf("HTTPError presence = %t, want %t; error = %v", errors.As(err, &httpErr), tt.wantHTTP, err)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.wantIs)
			}
			if tt.wantAlso != nil && !errors.Is(err, tt.wantAlso) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.wantAlso)
			}
		})
	}
}

func TestHandlerRejectsInvalidRequests(t *testing.T) {
	handler := NewHandler(providerFunc(func(context.Context, ListQuery) ([]Server, error) {
		return nil, nil
	}), WithRequiredKey("secret"))

	tests := []struct {
		name   string
		method string
		target string
		want   int
	}{
		{
			name:   "method",
			method: http.MethodPost,
			target: "/?key=secret",
			want:   http.StatusMethodNotAllowed,
		},
		{
			name:   "key",
			method: http.MethodGet,
			target: "/?key=wrong",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "limit",
			method: http.MethodGet,
			target: "/?key=secret&limit=bad",
			want:   http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.target, nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestHandlerUsesBoundedDefaultForEmptyResults(t *testing.T) {
	for _, target := range []string{"/?key=secret", "/?key=secret&limit=0"} {
		t.Run(target, func(t *testing.T) {
			handler := NewHandler(providerFunc(func(_ context.Context, query ListQuery) ([]Server, error) {
				if query.Limit != 5 {
					t.Errorf("provider limit = %d, want 5", query.Limit)
				}
				return nil, nil
			}), WithRequiredKey("secret"), WithMaxLimit(5))

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if !strings.Contains(recorder.Body.String(), `"servers":[]`) {
				t.Fatalf("response does not contain an empty server array: %s", recorder.Body.String())
			}
		})
	}
}
