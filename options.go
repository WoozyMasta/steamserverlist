// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"net/http"
	"net/url"
)

const (
	// defaultMaxResponseSize bounds response allocation while leaving room for large listings.
	defaultMaxResponseSize = 16 << 20
	// defaultBaseURL is Steam's GetServerList endpoint.
	defaultBaseURL = "https://api.steampowered.com/IGameServersService/GetServerList/v1/"
)

// Option configures a Web API client.
type Option func(*clientConfig) error

// HandlerOption configures the Web API-compatible HTTP handler.
type HandlerOption func(*handlerConfig)

// clientConfig holds mutable configuration while New applies options.
type clientConfig struct {
	// httpClient sends requests to the endpoint.
	httpClient *http.Client
	// baseURL is the configured GetServerList endpoint.
	baseURL *url.URL
	// maxResponseBytes caps bytes read from a response body.
	maxResponseBytes int64
}

// handlerConfig holds request policy while [NewHandler] applies options.
type handlerConfig struct {
	// requiredKey authenticates incoming requests when non-empty.
	requiredKey string
	// maxLimit bounds the number of records accepted from callers.
	maxLimit uint32
}

// WithHTTPClient injects the HTTP client used for requests.
func WithHTTPClient(client *http.Client) Option {
	return func(config *clientConfig) error {
		if client == nil {
			return ErrNilHTTPClient
		}

		config.httpClient = client
		return nil
	}
}

// WithBaseURL replaces the full GetServerList endpoint URL.
// It is useful for compatible local handlers and deterministic tests.
func WithBaseURL(rawURL string) Option {
	return func(config *clientConfig) error {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
			return ErrInvalidBaseURL
		}

		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return ErrUnsupportedBaseURLScheme
		}

		config.baseURL = parsed
		return nil
	}
}

// WithMaxResponseSize limits the response body read by the client.
func WithMaxResponseSize(size int64) Option {
	return func(config *clientConfig) error {
		if size <= 0 {
			return ErrMaxResponseSizeMustBePositive
		}

		if size == int64(^uint64(0)>>1) {
			return ErrMaxResponseSizeOverflows
		}

		config.maxResponseBytes = size
		return nil
	}
}

// WithRequiredKey enables constant-time byte comparison for equal-length keys.
// An empty key disables authentication.
func WithRequiredKey(key string) HandlerOption {
	return func(config *handlerConfig) { config.requiredKey = key }
}

// WithMaxLimit bounds the limit accepted by the HTTP handler.
func WithMaxLimit(limit uint32) HandlerOption {
	return func(config *handlerConfig) {
		if limit > 0 {
			config.maxLimit = limit
		}
	}
}
