// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrNilContext indicates an operation received a nil context.
	ErrNilContext = errors.New("web API context is nil")
	// ErrClientUninitialized indicates the client is nil or lacks required configuration.
	ErrClientUninitialized = errors.New("web API client is nil or uninitialized")
	// ErrConfigureClient marks failures applying client options.
	ErrConfigureClient = errors.New("configure Web API client")
	// ErrCreateRequest marks failures constructing the HTTP request.
	ErrCreateRequest = errors.New("create Web API request")
	// ErrExecuteRequest marks transport failures executing the HTTP request.
	ErrExecuteRequest = errors.New("execute Web API request")
	// ErrReadResponseBody marks failures reading an HTTP response body.
	ErrReadResponseBody = errors.New("read Web API response")
	// ErrResponseTooLarge indicates the response body exceeded its configured bound.
	ErrResponseTooLarge = errors.New("web API response exceeds configured size limit")
	// ErrDecodeResponse marks failures decoding the top-level JSON document.
	ErrDecodeResponse = errors.New("decode Web API response")
	// ErrMissingResponseEnvelope indicates the API response object is absent or null.
	ErrMissingResponseEnvelope = errors.New("web API response envelope is missing response")
	// ErrDecodeServerList marks failures decoding the servers array.
	ErrDecodeServerList = errors.New("decode Web API servers")
	// ErrDecodeServerRecord marks failures decoding one server record.
	ErrDecodeServerRecord = errors.New("decode Web API server")
	// ErrInvalidServerAddress indicates a record's address is not a valid endpoint.
	ErrInvalidServerAddress = errors.New("invalid server address")
	// ErrInvalidSteamID indicates a Steam ID is absent or not an unsigned decimal integer.
	ErrInvalidSteamID = errors.New("invalid steamid")
	// ErrInvalidAPIErrorCode indicates an API error code has an unsupported JSON value.
	ErrInvalidAPIErrorCode = errors.New("invalid API error code")
	// ErrNilHTTPClient indicates [WithHTTPClient] received nil.
	ErrNilHTTPClient = errors.New("HTTP client is nil")
	// ErrInvalidBaseURL indicates the configured endpoint is not an absolute HTTP URL.
	ErrInvalidBaseURL = errors.New("invalid Web API base URL")
	// ErrUnsupportedBaseURLScheme indicates the endpoint is not HTTP or HTTPS.
	ErrUnsupportedBaseURLScheme = errors.New("unsupported Web API base URL scheme")
	// ErrMaxResponseSizeMustBePositive indicates a non-positive response body limit.
	ErrMaxResponseSizeMustBePositive = errors.New("maximum response size must be positive")
	// ErrMaxResponseSizeOverflows indicates the configured size cannot be incremented safely.
	ErrMaxResponseSizeOverflows = errors.New("maximum response size is too large")
	// ErrNilProvider indicates the HTTP handler has no provider configured.
	ErrNilProvider = errors.New("provider is not configured")
	// ErrMethodNotAllowed identifies requests using an unsupported HTTP method.
	ErrMethodNotAllowed = errors.New("method not allowed")
	// ErrInvalidAPIKey identifies requests rejected by handler key validation.
	ErrInvalidAPIKey = errors.New("invalid API key")
	// ErrProviderFailure identifies a provider error returned as an HTTP response.
	ErrProviderFailure = errors.New("provider error")
	// ErrInvalidListLimit indicates the limit is not an unsigned 32-bit integer.
	ErrInvalidListLimit = errors.New("invalid limit")
	// ErrListLimitExceedsMaximum indicates the requested limit exceeds handler policy.
	ErrListLimitExceedsMaximum = errors.New("limit exceeds maximum")
	// ErrFilterCannotCombineNORAndNAND indicates incompatible exclusion groups.
	ErrFilterCannotCombineNORAndNAND = errors.New("filter cannot combine NOR and NAND conditions")
	// ErrInvalidDiscoveryOptions indicates invalid discovery limits or features.
	ErrInvalidDiscoveryOptions = errors.New("invalid discovery options")
	// ErrInvalidDiscoveryPlan indicates an unsupported or malformed saved plan.
	ErrInvalidDiscoveryPlan = errors.New("invalid discovery plan")
	// ErrInvalidQueryFilter indicates a validated filter contains malformed data.
	ErrInvalidQueryFilter = errors.New("invalid validated server filter")
	// ErrConflictingQueryFilter indicates predicates cannot match simultaneously.
	ErrConflictingQueryFilter = errors.New("conflicting server filter predicates")
	// ErrDiscoveryResponseOverLimit indicates the endpoint returned more rows than requested.
	ErrDiscoveryResponseOverLimit = errors.New("discovery response exceeds requested row limit")
	// ErrUnsupportedDiscoveryFilter indicates valid filter syntax outside the partition engine's subset.
	ErrUnsupportedDiscoveryFilter = errors.New("unsupported discovery filter expression")
	// ErrInvalidJSONBoolean indicates a projected Web API boolean field was neither true, false, nor null.
	ErrInvalidJSONBoolean = errors.New("invalid JSON boolean value")
)

// HTTPError reports a non-success HTTP response from Steam or a compatible endpoint.
type HTTPError struct {
	// Status is the HTTP status text, when provided by the response.
	Status string
	// StatusCode is the numeric HTTP status code.
	StatusCode int
}

// APIError reports an error returned in the Steam API response envelope.
type APIError struct {
	// Code is Steam's API error code, normalized to a decimal or textual string.
	Code string `json:"errorcode,omitempty"`
	// Description is the human-readable error detail returned by the API.
	Description string `json:"errordesc,omitempty"`
}

// rawAPIError holds the JSON representation accepted by APIError.
type rawAPIError struct {
	// Description contains the human-readable API error message.
	Description string `json:"errordesc"`
	// Code accepts Steam's string or numeric error-code representation.
	Code json.RawMessage `json:"errorcode"`
}

// Error returns the HTTP status description or code.
func (e *HTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.Status != "" {
		return "steam Web API returned HTTP status " + e.Status
	}

	return fmt.Sprintf("steam Web API returned HTTP status %d", e.StatusCode)
}

// UnmarshalJSON accepts Steam responses that encode errorcode
// as either a JSON string or a number.
func (e *APIError) UnmarshalJSON(data []byte) error {
	var raw rawAPIError
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAPIErrorCode, err)
	}

	e.Description = raw.Description
	code := bytes.TrimSpace(raw.Code)
	if len(code) == 0 || bytes.Equal(code, []byte("null")) {
		e.Code = ""
		return nil
	}

	if code[0] == '"' {
		if err := json.Unmarshal(code, &e.Code); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidAPIErrorCode, err)
		}
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(code, &number); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidAPIErrorCode, err)
	}

	e.Code = number.String()
	return nil
}

// Error returns the API-provided code and description.
func (e *APIError) Error() string {
	if e.Code == "" {
		return "steam Web API error: " + e.Description
	}

	return fmt.Sprintf("steam Web API error %s: %s", e.Code, e.Description)
}
