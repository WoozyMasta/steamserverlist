// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// Provider supplies rich records to the Web API-compatible handler.
type Provider interface {
	// List returns server records matching the query.
	List(ctx context.Context, query ListQuery) ([]Server, error)
}

// ProviderFunc adapts a function to the [Provider] interface.
type ProviderFunc func(ctx context.Context, query ListQuery) ([]Server, error)

// List calls f with the requested query.
// A nil function returns [ErrNilProvider].
func (f ProviderFunc) List(ctx context.Context, query ListQuery) ([]Server, error) {
	if f == nil {
		return nil, ErrNilProvider
	}

	return f(ctx, query)
}

// NewHandler exposes the supported GetServerList response shape over HTTP.
func NewHandler(provider Provider, opts ...HandlerOption) http.Handler {
	config := handlerConfig{maxLimit: DefaultLimit}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			writeHandlerError(w, http.StatusInternalServerError, ErrNilProvider.Error())
			return
		}

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeHandlerError(w, http.StatusMethodNotAllowed, ErrMethodNotAllowed.Error())
			return
		}

		if config.requiredKey != "" && !secureEqual(config.requiredKey, r.URL.Query().Get("key")) {
			writeHandlerError(w, http.StatusUnauthorized, ErrInvalidAPIKey.Error())
			return
		}

		query, err := parseListQuery(r, config.maxLimit)
		if err != nil {
			writeHandlerError(w, http.StatusBadRequest, err.Error())
			return
		}

		servers, err := provider.List(r.Context(), query)
		if err != nil {
			writeHandlerError(w, http.StatusInternalServerError, ErrProviderFailure.Error())
			return
		}

		if servers == nil {
			servers = []Server{}
		}

		type responseServer struct {
			GameType string `json:"gametype"`
			Server
		}
		responseServers := make([]responseServer, len(servers))
		for i, server := range servers {
			responseServers[i] = responseServer{
				Server:   server,
				GameType: strings.Join(server.GameType, ", "),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Response struct {
				Servers []responseServer `json:"servers"`
			} `json:"response"`
		}{Response: struct {
			Servers []responseServer `json:"servers"`
		}{Servers: responseServers}})
	})
}

// parseListQuery validates handler query parameters and applies the default limit.
func parseListQuery(r *http.Request, maxLimit uint32) (ListQuery, error) {
	values := r.URL.Query()
	// A server policy below the package default must still allow omitted limits.
	defaultLimit := min(maxLimit, DefaultLimit)
	query := ListQuery{
		Filter: values.Get("filter"),
		Limit:  defaultLimit,
	}

	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.ParseUint(rawLimit, 10, 32)
		if err != nil {
			return ListQuery{}, ErrInvalidListLimit
		}

		query.Limit = uint32(limit)
		if query.Limit == 0 {
			query.Limit = defaultLimit
		}
	}

	if query.Limit > maxLimit {
		return ListQuery{}, ErrListLimitExceedsMaximum
	}

	return query, nil
}

// secureEqual rejects unequal lengths before constant-time comparison of bytes.
func secureEqual(expected, actual string) bool {
	if len(expected) != len(actual) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

// writeHandlerError encodes a stable API-shaped error response.
func writeHandlerError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Response struct {
			Error APIError `json:"error"`
		} `json:"response"`
	}{Response: struct {
		Error APIError `json:"error"`
	}{Error: APIError{Description: message}}})
}
