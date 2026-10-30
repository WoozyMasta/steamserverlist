// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandlerAcceptsProviderFunc(t *testing.T) {
	wantQuery := ListQuery{Filter: `\appid\221100`, Limit: 25}
	var gotQuery ListQuery
	provider := ProviderFunc(func(_ context.Context, query ListQuery) ([]Server, error) {
		gotQuery = query
		return []Server{}, nil
	})
	handler := NewHandler(provider)
	request := httptest.NewRequest(http.MethodGet, "/?filter=%5Cappid%5C221100&limit=25", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want %d", response.Code, http.StatusOK)
	}
	if gotQuery != wantQuery {
		t.Fatalf("provider query = %#v, want %#v", gotQuery, wantQuery)
	}
}

func TestNilProviderFuncReturnsErrNilProvider(t *testing.T) {
	var provider ProviderFunc
	_, err := provider.List(context.Background(), ListQuery{})
	if !errors.Is(err, ErrNilProvider) {
		t.Fatalf("nil provider error = %v, want %v", err, ErrNilProvider)
	}
}
