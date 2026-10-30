// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	slist "github.com/woozymasta/steamserverlist"
)

func ExampleClient_List() {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("filter") != `\appid\221100\secure\1` || query.Get("limit") != "100" {
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}

		_, _ = fmt.Fprint(w, `{
	"response":{
		"servers":[{
			"addr":"192.0.2.1:27015",
			"name":"Example DayZ server",
			"players":3,
			"max_players":60,
			"gametype":"battleye, lqs0"
		}]
	}
}`)
	}))
	defer api.Close()

	client, err := slist.New("example-api-key", slist.WithBaseURL(api.URL))
	if err != nil {
		panic(err)
	}

	servers, err := client.List(context.Background(), slist.ListQuery{
		Filter: `\appid\221100\secure\1`,
		Limit:  100,
	})
	if err != nil {
		panic(err)
	}

	for _, server := range servers {
		fmt.Printf("%s: %d/%d [%s]\n",
			server.Name,
			server.Players,
			server.MaxPlayers,
			strings.Join(server.GameType, ", "),
		)
	}

	// Output:
	// Example DayZ server: 3/60 [battleye, lqs0]
}

func ExampleClient_DiscoverAddresses() {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"response":{"servers":[
			{"addr":"192.0.2.1:27015","appid":221100},
			{"addr":"192.0.2.2:27015","appid":221100}
		]}}`)
	}))
	defer api.Close()

	client, err := slist.New("example-api-key", slist.WithBaseURL(api.URL))
	if err != nil {
		panic(err)
	}
	filter, err := slist.NewQueryFilter(
		[]slist.Predicate{{Key: slist.KeyAppID, Value: "221100"}},
		nil,
	)
	if err != nil {
		panic(err)
	}

	discovery, err := client.DiscoverAddresses(
		context.Background(),
		filter,
		slist.DiscoveryOptions{},
	)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Found %d server addresses:\n", len(discovery.Items))
	for _, address := range discovery.Items {
		fmt.Println(address)
	}
	fmt.Printf("All query branches drained: %t\n", discovery.Status.Complete())

	// Output:
	// Found 2 server addresses:
	// 192.0.2.1:27015
	// 192.0.2.2:27015
	// All query branches drained: true
}

func ExampleServers_GetVersionMap() {
	servers := slist.Servers{
		{Version: "1.2.3"},
		{Version: "1.2.3"},
		{Version: "1.2.4"},
	}
	versions := servers.GetVersionMap()

	fmt.Printf("1.2.3: %d\n", versions["1.2.3"])
	fmt.Printf("1.2.4: %d\n", versions["1.2.4"])

	// Output:
	// 1.2.3: 2
	// 1.2.4: 1
}
