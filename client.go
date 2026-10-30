// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

// Package serverlist implements Steam's unofficial GetServerList Web API.
package serverlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// DefaultLimit is the default maximum number of records requested
// by the compatible HTTP handler when the query omits limit.
const DefaultLimit uint32 = 10000

// Client is a stateless Steam Web API client safe for concurrent use.
type Client struct {
	// httpClient sends requests to the configured endpoint.
	httpClient *http.Client
	// baseURL is the GetServerList endpoint with any caller-supplied parameters.
	baseURL *url.URL
	// apiKey authenticates requests and is redacted from returned errors.
	apiKey string
	// maxResponseSize bounds memory use while reading a response body.
	maxResponseSize int64
}

// rawServer matches the field names and integer widths used by GetServerList.
type rawServer struct {
	// Addr is the server endpoint in address:port form.
	Addr string `json:"addr"`
	// Name is the user-visible server name.
	Name string `json:"name"`
	// GameDir identifies the server's game directory.
	GameDir string `json:"gamedir"`
	// Map is the currently configured map.
	Map string `json:"map"`
	// Product identifies the game product.
	Product string `json:"product"`
	// Version is the server's reported version.
	Version string `json:"version"`
	// OS is the server operating-system marker.
	OS string `json:"os"`
	// GameType contains comma-separated server tags.
	GameType string `json:"gametype"`
	// SteamID accepts Steam's string or integer representation.
	SteamID steamIDValue `json:"steamid"`
	// AppID identifies the hosted game.
	AppID uint32 `json:"appid"`
	// Region is Steam's numeric region identifier.
	Region int32 `json:"region"`
	// Players is the current player count.
	Players int32 `json:"players"`
	// MaxPlayers is the advertised player capacity.
	MaxPlayers int32 `json:"max_players"`
	// Bots is the current bot count.
	Bots int32 `json:"bots"`
	// GamePort is the gameplay port reported by Steam.
	GamePort uint16 `json:"gameport"`
	// SpectatorPort is the optional spectator port.
	SpectatorPort uint16 `json:"specport"`
	// Dedicated reports whether the server is dedicated.
	Dedicated optionalJSONBool `json:"dedicated"`
	// Secure reports whether the server advertises secure authentication.
	Secure optionalJSONBool `json:"secure"`
}

// rawLiteServer holds only fields retained by ServerLite.
type rawLiteServer struct {
	// Addr is the server endpoint in address:port form.
	Addr string `json:"addr"`
	// Map is the currently configured map.
	Map string `json:"map"`
	// Version is the server's reported game version.
	Version string `json:"version"`
	// OS is the server operating-system marker.
	OS string `json:"os"`
	// GameType contains comma-separated server tags.
	GameType string `json:"gametype"`
	// AppID identifies the hosted game for planner membership checks.
	AppID uint32 `json:"appid"`
	// Players is the current player count.
	Players int32 `json:"players"`
	// MaxPlayers is the advertised player capacity.
	MaxPlayers int32 `json:"max_players"`
	// Dedicated reports whether the server is dedicated.
	Dedicated optionalJSONBool `json:"dedicated"`
	// Secure reports whether the server advertises secure authentication.
	Secure optionalJSONBool `json:"secure"`
}

// responseEnvelope models the top-level GetServerList response for a server list type.
type responseEnvelope[T any] struct {
	// Response is nil when the response is missing or explicitly null.
	Response *responsePayload[T] `json:"response"`
}

// responsePayload holds the API error and selected server-list representation.
type responsePayload[T any] struct {
	// Error carries an API-level failure when present.
	Error *APIError `json:"error"`
	// Servers contains records in the representation selected by the decoder.
	Servers T `json:"servers"`
}

// rawServerList decodes server records directly from the response payload.
type rawServerList []rawServer

// rawLiteServerList decodes the compact server records.
type rawLiteServerList []rawLiteServer

// rawAddressServerList decodes endpoint-only server records.
type rawAddressServerList []rawAddressServer

// rawAddressServer contains only the server endpoint.
type rawAddressServer struct {
	// Addr is the server endpoint in address:port form.
	Addr string `json:"addr"`
}

// optionalJSONBool preserves presence without allocating a bool pointer per row.
type optionalJSONBool struct {
	// value is the parsed boolean, defaulting to false when absent.
	value bool
	// known records whether JSON explicitly supplied true or false.
	known bool
}

// steamIDValue accepts Steam IDs encoded as strings or exact JSON integers.
type steamIDValue uint64

// secretRedactingError hides credentials while preserving the original error chain.
type secretRedactingError struct {
	// err is the underlying error retained for [errors.Is] and [errors.As].
	err error
	// secret is the API key that must not appear in diagnostics.
	secret string
}

// New creates a Web API client.
func New(apiKey string, opts ...Option) (*Client, error) {
	baseURL, err := url.Parse(defaultBaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidBaseURL, err)
	}

	config := clientConfig{
		httpClient:       http.DefaultClient,
		baseURL:          baseURL,
		maxResponseBytes: defaultMaxResponseSize,
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&config); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrConfigureClient, err)
		}
	}

	return &Client{
		apiKey:          apiKey,
		httpClient:      config.httpClient,
		baseURL:         config.baseURL,
		maxResponseSize: config.maxResponseBytes,
	}, nil
}

// List retrieves rich server records from GetServerList.
func (c *Client) List(ctx context.Context, query ListQuery) (Servers, error) {
	return listDecoded(ctx, c, query, decodeResponse)
}

// ListLite retrieves only the address and fields used by adaptive discovery.
func (c *Client) ListLite(ctx context.Context, query ListQuery) ([]ServerLite, error) {
	return listDecoded(ctx, c, query, decodeLiteResponse)
}

// ListAddresses retrieves only server addresses from GetServerList.
func (c *Client) ListAddresses(ctx context.Context, query ListQuery) ([]netip.AddrPort, error) {
	return listDecoded(ctx, c, query, decodeAddressResponse)
}

// listDecoded shares request handling while allowing each public listing method
// to decode only the fields its result contract needs.
func listDecoded[T any](
	ctx context.Context,
	c *Client,
	query ListQuery,
	decode func([]byte) (T, error),
) (result T, resultErr error) {
	if c == nil || c.baseURL == nil || c.httpClient == nil || c.maxResponseSize <= 0 {
		return result, ErrClientUninitialized
	}
	if ctx == nil {
		return result, ErrNilContext
	}

	// Transport and API errors may include the request URL, which carries the key.
	defer func() {
		if resultErr != nil && c.apiKey != "" {
			resultErr = &secretRedactingError{err: resultErr, secret: c.apiKey}
		}
	}()

	params := c.baseURL.Query()
	params.Del("key")
	if c.apiKey != "" {
		params.Set("key", c.apiKey)
	}

	params.Set("format", "json")
	params.Set("filter", query.Filter)
	limit := query.Limit
	if limit == 0 {
		limit = DefaultLimit
	}

	params.Set("limit", strconv.FormatUint(uint64(limit), 10))
	requestURL := *c.baseURL
	requestURL.RawQuery = params.Encode()

	// The base URL is explicitly supplied by the caller or the package default.
	//nolint:gosec // configurable API endpoint is intentional
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrCreateRequest, err)
	}

	//nolint:gosec // configurable API endpoint is intentional
	response, err := c.httpClient.Do(request)
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrExecuteRequest, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, &HTTPError{StatusCode: response.StatusCode, Status: response.Status}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseSize+1))
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrReadResponseBody, err)
	}
	if int64(len(body)) > c.maxResponseSize {
		return result, fmt.Errorf("%w: maximum %d bytes", ErrResponseTooLarge, c.maxResponseSize)
	}

	return decode(body)
}

// decodeResponse validates the response envelope and converts its server records.
func decodeResponse(body []byte) (Servers, error) {
	var envelope responseEnvelope[rawServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}

	payload := envelope.Response
	if payload.Error != nil {
		return nil, payload.Error
	}
	if payload.Servers == nil {
		return Servers{}, nil
	}

	servers := make(Servers, 0, len(payload.Servers))
	for i, raw := range payload.Servers {
		server, err := raw.server()
		if err != nil {
			return nil, fmt.Errorf("%w %d: %w", ErrDecodeServerRecord, i, err)
		}

		servers = append(servers, server)
	}

	return servers, nil
}

// decodeLiteResponse projects server records onto the discovery fields.
func decodeLiteResponse(body []byte) ([]ServerLite, error) {
	var envelope responseEnvelope[rawLiteServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}
	if envelope.Response.Error != nil {
		return nil, envelope.Response.Error
	}

	if envelope.Response.Servers == nil {
		return []ServerLite{}, nil
	}

	servers := make([]ServerLite, len(envelope.Response.Servers))
	for index, raw := range envelope.Response.Servers {
		addr, err := netip.ParseAddrPort(raw.Addr)
		if err != nil {
			return nil, fmt.Errorf(
				"%w %d: %w %q: %w",
				ErrDecodeServerRecord,
				index,
				ErrInvalidServerAddress,
				raw.Addr,
				err,
			)
		}

		servers[index] = ServerLite{
			Addr:       addr,
			Map:        raw.Map,
			Version:    raw.Version,
			OS:         raw.OS,
			GameType:   parseGameTypes(raw.GameType),
			Players:    raw.Players,
			MaxPlayers: raw.MaxPlayers,
			Dedicated:  raw.Dedicated.value,
			Secure:     raw.Secure.value,
		}
	}

	return servers, nil
}

// decodeAddressResponse projects server records onto parsed network endpoints.
func decodeAddressResponse(body []byte) ([]netip.AddrPort, error) {
	var envelope responseEnvelope[rawAddressServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}
	if envelope.Response.Error != nil {
		return nil, envelope.Response.Error
	}

	if envelope.Response.Servers == nil {
		return []netip.AddrPort{}, nil
	}

	addresses := make([]netip.AddrPort, len(envelope.Response.Servers))
	for index, raw := range envelope.Response.Servers {
		addr, err := netip.ParseAddrPort(raw.Addr)
		if err != nil {
			return nil, fmt.Errorf(
				"%w %d: %w %q: %w",
				ErrDecodeServerRecord,
				index,
				ErrInvalidServerAddress,
				raw.Addr,
				err,
			)
		}

		addresses[index] = addr
	}

	return addresses, nil
}

// UnmarshalJSON classifies malformed compact server arrays separately.
func (servers *rawLiteServerList) UnmarshalJSON(data []byte) error {
	var decoded []rawLiteServer
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	*servers = decoded
	return nil
}

// UnmarshalJSON classifies malformed endpoint arrays separately.
func (servers *rawAddressServerList) UnmarshalJSON(data []byte) error {
	var decoded []rawAddressServer
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	*servers = decoded
	return nil
}

// UnmarshalJSON classifies malformed server arrays separately from envelopes.
func (servers *rawServerList) UnmarshalJSON(data []byte) error {
	var decoded []rawServer
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	*servers = decoded
	return nil
}

// server validates fields that need stronger types before building a record.
func (raw rawServer) server() (Server, error) {
	addr, err := netip.ParseAddrPort(raw.Addr)
	if err != nil {
		return Server{}, fmt.Errorf("%w %q: %w", ErrInvalidServerAddress, raw.Addr, err)
	}

	return Server{
		Addr:          addr,
		GamePort:      raw.GamePort,
		SpectatorPort: raw.SpectatorPort,
		SteamID:       uint64(raw.SteamID),
		AppID:         raw.AppID,
		Name:          raw.Name,
		GameDir:       raw.GameDir,
		Map:           raw.Map,
		Product:       raw.Product,
		Version:       raw.Version,
		OS:            raw.OS,
		GameType:      parseGameTypes(raw.GameType),
		Region:        raw.Region,
		Players:       raw.Players,
		MaxPlayers:    raw.MaxPlayers,
		Bots:          raw.Bots,
		Dedicated:     raw.Dedicated.value,
		Secure:        raw.Secure.value,
	}, nil
}

// parseGameTypes converts Steam's comma-separated tags to a trimmed slice.
func parseGameTypes(raw string) []string {
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	tags := parts[:0]
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}

	if len(tags) == 0 {
		return nil
	}

	return tags
}

// UnmarshalJSON validates Steam's string and numeric SteamID encodings.
func (id *steamIDValue) UnmarshalJSON(data []byte) error {
	parsed, err := parseSteamID(data)
	if err != nil {
		return err
	}

	*id = steamIDValue(parsed)
	return nil
}

// UnmarshalJSON records explicit booleans and treats null as absent.
func (value *optionalJSONBool) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true":
		value.value = true
		value.known = true

	case "false":
		value.value = false
		value.known = true

	case "null":
		*value = optionalJSONBool{}

	default:
		return ErrInvalidJSONBoolean
	}

	return nil
}

// parseSteamID accepts the quoted form used by Steam and exact JSON integers.
func parseSteamID(raw json.RawMessage) (uint64, error) {
	var value string
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, ErrInvalidSteamID
	}

	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrInvalidSteamID, err)
		}
	} else {
		value = string(trimmed)
	}

	if strings.TrimSpace(value) == "" {
		return 0, ErrInvalidSteamID
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidSteamID
		}
	}

	steamID, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidSteamID, err)
	}

	return steamID, nil
}

// Error returns the wrapped message with literal and URL-escaped secrets removed.
func (e *secretRedactingError) Error() string {
	message := e.err.Error()
	for _, secret := range []string{e.secret, url.QueryEscape(e.secret), url.PathEscape(e.secret)} {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}

	return message
}

// Unwrap exposes the original error for standard error inspection.
func (e *secretRedactingError) Unwrap() error {
	return e.err
}
