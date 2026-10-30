// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"net/netip"
)

// ListQuery contains parameters for a rich server-list backend.
// A zero limit selects the default, capped by the handler's configured maximum.
type ListQuery struct {
	// Filter is the raw master-server filter expression sent to the backend.
	Filter string `json:"filter,omitempty" yaml:"filter,omitempty"`

	// Limit is the maximum number of records requested; zero uses the package default.
	Limit uint32 `json:"limit,omitempty" yaml:"limit,omitempty"`
}

// Server is the rich server record returned by a listing backend.
type Server struct {
	// Addr is the address and query port reported by the listing backend.
	Addr netip.AddrPort `yaml:"addr" json:"addr"`

	// Name is the server's advertised display name.
	Name string `yaml:"name" json:"name"`

	// GameDir is the server's game directory, such as "dayz" or "cstrike".
	GameDir string `yaml:"gamedir" json:"gamedir"`

	// Map is the map currently reported by the server.
	Map string `yaml:"map" json:"map"`

	// Product identifies the game product reported by the backend.
	Product string `yaml:"product" json:"product"`

	// Version is the game version reported by the server.
	Version string `yaml:"version" json:"version"`

	// OS is the operating-system marker reported by the backend.
	OS string `yaml:"os" json:"os"`

	// GameType contains the server's comma-separated game tags as separate values.
	GameType []string `yaml:"gametype" json:"gametype"`

	// SteamID is the server's Steam identifier.
	SteamID uint64 `yaml:"steamid" json:"steamid,string"`

	// AppID identifies the hosted Steam application.
	AppID uint32 `yaml:"appid" json:"appid"`

	// Region is the backend's numeric region identifier.
	Region int32 `yaml:"region,omitempty" json:"region,omitempty"`

	// Players is the current player count reported by the server.
	Players int32 `yaml:"players" json:"players"`

	// MaxPlayers is the advertised player capacity.
	MaxPlayers int32 `yaml:"max_players" json:"max_players"`

	// Bots is the current bot count reported by the server.
	Bots int32 `yaml:"bots,omitempty" json:"bots,omitempty"`

	// GamePort is the gameplay port reported separately from Addr.
	GamePort uint16 `yaml:"gameport" json:"gameport"`

	// SpectatorPort is the optional spectator port.
	SpectatorPort uint16 `yaml:"specport,omitempty" json:"specport,omitempty"`

	// Dedicated reports whether the backend marks the server as dedicated.
	Dedicated bool `yaml:"dedicated" json:"dedicated"`

	// Secure reports whether the server advertises secure authentication.
	Secure bool `yaml:"secure" json:"secure"`
}

// ServerLite contains the address and fields used by adaptive discovery.
type ServerLite struct {
	// Addr is the address and query port reported by the listing backend.
	Addr netip.AddrPort `yaml:"addr" json:"addr"`

	// Map is the map currently reported by the server.
	Map string `yaml:"map" json:"map"`

	// Version is the game version reported by the server.
	Version string `yaml:"version" json:"version"`

	// OS is the operating-system marker reported by the backend.
	OS string `yaml:"os" json:"os"`

	// GameType contains the server's comma-separated game tags as separate values.
	GameType []string `yaml:"gametype" json:"gametype"`

	// Players is the current player count reported by the server.
	Players int32 `yaml:"players" json:"players"`

	// MaxPlayers is the advertised player capacity.
	MaxPlayers int32 `yaml:"max_players" json:"max_players"`

	// Dedicated reports whether the backend marks the server as dedicated.
	Dedicated bool `yaml:"dedicated" json:"dedicated"`

	// Secure reports whether the server advertises secure authentication.
	Secure bool `yaml:"secure" json:"secure"`
}

// Servers is a collection of server records returned by a listing backend.
type Servers []Server

// GetVersionMap counts the servers for each reported game version.
func (servers Servers) GetVersionMap() map[string]uint32 {
	versions := make(map[string]uint32)
	for _, server := range servers {
		versions[server.Version]++
	}

	return versions
}
