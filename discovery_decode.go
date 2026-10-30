// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
)

// discoveryWitness is the compact immutable evidence used to validate partitions.
type discoveryWitness struct {
	// addr is the stable key for deduplication and coverage checks.
	addr netip.AddrPort
	// mapName is the observed map used by map partitions.
	mapName string
	// version is the exact observed game version.
	version string
	// os is the observed operating-system marker.
	os string
	// gameType is Steam's original comma-separated tag field.
	gameType string
	// appID verifies that a response remains inside the requested game.
	appID uint32
	// secure is the decoded secure flag when its field is present.
	secure bool
	// dedicated is the decoded dedicated flag when its field is present.
	dedicated bool
	// secureKnown distinguishes an explicit false from an absent field.
	secureKnown bool
	// dedicatedKnown distinguishes an explicit false from an absent field.
	dedicatedKnown bool
}

// discoveryAddressServer is the minimum data needed for address output and splitting.
type discoveryAddressServer struct {
	// Addr is parsed into the retained endpoint.
	Addr string `json:"addr"`
	// Map supports map partition membership.
	Map string `json:"map"`
	// Version supports exact version partition membership.
	Version string `json:"version"`
	// OS supports Linux partition membership.
	OS string `json:"os"`
	// GameType supports tag partition membership.
	GameType string `json:"gametype"`
	// AppID verifies the requested game scope.
	AppID uint32 `json:"appid"`
	// Dedicated retains whether its source field was present.
	Dedicated optionalJSONBool `json:"dedicated"`
	// Secure retains whether its source field was present.
	Secure optionalJSONBool `json:"secure"`
}

// discoveryRecord pairs the selected public output with transient planner evidence.
type discoveryRecord[T any] struct {
	// item is moved into the deduplicated result.
	item T
	// witness is retained only while discovery validates partitions.
	witness discoveryWitness
}

// discoveryAddressServerList contains direct endpoint records and planner witnesses.
type discoveryAddressServerList []discoveryRecord[netip.AddrPort]

// discoveryFullServerList converts each raw row directly to its retained record.
type discoveryFullServerList []discoveryRecord[Server]

// discoveryLiteServerList converts each raw row directly to a lite record.
type discoveryLiteServerList []discoveryRecord[ServerLite]

// UnmarshalJSON decodes each lite row directly into its retained record.
func (servers *discoveryLiteServerList) UnmarshalJSON(data []byte) error {
	decoded, err := decodeDiscoveryRows(data, func(raw rawLiteServer, index int) (discoveryRecord[ServerLite], error) {
		addr, err := netip.ParseAddrPort(raw.Addr)
		if err != nil {
			return discoveryRecord[ServerLite]{}, fmt.Errorf(
				"%w %d: %w %q: %w",
				ErrDecodeServerRecord,
				index,
				ErrInvalidServerAddress,
				raw.Addr,
				err,
			)
		}

		item := ServerLite{
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
		witness := discoveryWitness{
			addr:           addr,
			appID:          raw.AppID,
			mapName:        raw.Map,
			version:        raw.Version,
			os:             raw.OS,
			gameType:       raw.GameType,
			dedicatedKnown: raw.Dedicated.known,
			dedicated:      raw.Dedicated.value,
			secureKnown:    raw.Secure.known,
			secure:         raw.Secure.value,
		}

		return discoveryRecord[ServerLite]{item: item, witness: witness}, nil
	})
	if err != nil {
		return err
	}

	*servers = decoded
	return nil
}

// UnmarshalJSON decodes each rich row directly into its retained record.
func (servers *discoveryFullServerList) UnmarshalJSON(data []byte) error {
	decoded, err := decodeDiscoveryRows(data, func(raw rawServer, index int) (discoveryRecord[Server], error) {
		item, err := raw.server()
		if err != nil {
			return discoveryRecord[Server]{}, fmt.Errorf("%w %d: %w", ErrDecodeServerRecord, index, err)
		}

		return discoveryRecord[Server]{item: item, witness: witnessFromRaw(raw, item.Addr)}, nil
	})
	if err != nil {
		return err
	}

	*servers = decoded
	return nil
}

// UnmarshalJSON converts each address projection directly into its retained record.
func (servers *discoveryAddressServerList) UnmarshalJSON(data []byte) error {
	decoded, err := decodeDiscoveryRows(
		data,
		func(raw discoveryAddressServer, index int) (discoveryRecord[netip.AddrPort], error) {
			addr, err := netip.ParseAddrPort(raw.Addr)
			if err != nil {
				return discoveryRecord[netip.AddrPort]{}, fmt.Errorf(
					"%w %d: %w %q: %w",
					ErrDecodeServerRecord,
					index,
					ErrInvalidServerAddress,
					raw.Addr,
					err,
				)
			}

			witness := discoveryWitness{
				addr:           addr,
				appID:          raw.AppID,
				mapName:        raw.Map,
				version:        raw.Version,
				os:             raw.OS,
				gameType:       raw.GameType,
				dedicatedKnown: raw.Dedicated.known,
				dedicated:      raw.Dedicated.value,
				secureKnown:    raw.Secure.known,
				secure:         raw.Secure.value,
			}

			return discoveryRecord[netip.AddrPort]{item: addr, witness: witness}, nil
		},
	)
	if err != nil {
		return err
	}

	*servers = decoded
	return nil
}

// decodeDiscoveryRows parses a bounded server array without retaining a second row slice.
func decodeDiscoveryRows[Raw any, Item any](data []byte, convert func(Raw, int) (Item, error)) ([]Item, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("%w: expected server array", ErrDecodeServerList)
	}

	// Estimate capacity from address fields to reduce growth, but cap the hint for untrusted input.
	capacity := min(bytes.Count(trimmed, []byte(`"addr"`)), int(DefaultLimit))
	rows := make([]Item, 0, capacity)
	decoder := json.NewDecoder(bytes.NewReader(trimmed))

	// Consume the opening array token before decoding each row independently.
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	for decoder.More() {
		if len(rows) >= int(DefaultLimit) {
			return nil, fmt.Errorf("%w: response exceeds %d rows", ErrDecodeServerList, DefaultLimit)
		}

		var raw Raw
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrDecodeServerList, err)
		}

		row, err := convert(raw, len(rows))
		if err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	// Consume the closing array token, then reject any trailing JSON value.
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing JSON value", ErrDecodeServerList)
		}

		return nil, fmt.Errorf("%w: %w", ErrDecodeServerList, err)
	}

	return rows, nil
}

// decodeDiscoveryServers returns rich records with transient partition witnesses.
func decodeDiscoveryServers(body []byte) ([]discoveryRecord[Server], error) {
	var envelope responseEnvelope[discoveryFullServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}
	if envelope.Response.Error != nil {
		return nil, envelope.Response.Error
	}

	return envelope.Response.Servers, nil
}

// decodeDiscoveryLite avoids rich-only fields while retaining planner evidence.
func decodeDiscoveryLite(body []byte) ([]discoveryRecord[ServerLite], error) {
	var envelope responseEnvelope[discoveryLiteServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}
	if envelope.Response.Error != nil {
		return nil, envelope.Response.Error
	}

	return envelope.Response.Servers, nil
}

// decodeDiscoveryAddresses retains endpoints and only transient fields used for splitting.
func decodeDiscoveryAddresses(body []byte) ([]discoveryRecord[netip.AddrPort], error) {
	var envelope responseEnvelope[discoveryAddressServerList]
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecodeResponse, err)
	}
	if envelope.Response == nil {
		return nil, ErrMissingResponseEnvelope
	}
	if envelope.Response.Error != nil {
		return nil, envelope.Response.Error
	}

	return envelope.Response.Servers, nil
}

// witnessFromRaw extracts planner evidence without retaining the rich raw payload.
func witnessFromRaw(raw rawServer, addr netip.AddrPort) discoveryWitness {
	return discoveryWitness{
		addr:           addr,
		appID:          raw.AppID,
		mapName:        raw.Map,
		version:        raw.Version,
		os:             raw.OS,
		gameType:       raw.GameType,
		secureKnown:    raw.Secure.known,
		secure:         raw.Secure.value,
		dedicatedKnown: raw.Dedicated.known,
		dedicated:      raw.Dedicated.value,
	}
}
