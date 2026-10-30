# steamserverlist

[`steamserverlist`][pkg] is a Go client for Steam's
`IGameServersService/GetServerList` endpoint.
It retrieves server records,
supports Master Server Query Protocol filter expressions,
and can expose a compatible HTTP handler for a custom provider.

> [!WARNING]
> `GetServerList` is an undocumented Steam Web API endpoint.
> It is not the official Steamworks server-list interface,
> and its availability or behavior may change without notice.
> The [official matchmaking API][matchmaking] serves a different interface.

Package requires a Steam Web API key.
Endpoint and filter format are described
by community-maintained [GetServerList reference][get-server-list].

## Features

* Retrieve server records with a filter and result limit.
* Decode server addresses and Steam IDs into typed Go values.
* Expose a compatible HTTP handler backed by an application-defined provider.

## Installation

Add the module to your project:

```sh
go get github.com/woozymasta/steamserverlist@latest
```

## Usage

Create a client with a Steam Web API key and query servers.
Build a filter with `Filter` and pass its encoded expression to `List`.
This example selects DayZ servers tagged with BattlEye
while excluding servers tagged `external`.

```go
client, err := serverlist.New(apiKey)
if err != nil {
  return err
}

filter := serverlist.NewFilter()
filter.Add(serverlist.KeyAppID, "221100")
filter.Add(serverlist.KeyGameType, "battleye")
filter.AddNor(serverlist.KeyGameType, "external")
filterString, err := filter.String()
if err != nil {
  return err
}

servers, err := client.List(ctx, serverlist.ListQuery{
  Filter: filterString,
  Limit:  100,
})
if err != nil {
  return err
}

for _, server := range servers {
  fmt.Printf("%s at %s: %d/%d players\n",
    server.Name, server.Addr, server.Players, server.MaxPlayers)
}
```

`Server.GameType` contains the comma-separated game tags as `[]string`.
`Client.List` returns a `Servers` collection;
`GetVersionMap` counts records by reported game version.
`ListQuery.Limit == 0` uses the package default of 10,000 records.

## Discovery

`GetServerList` caps each response at **10,000** servers and has no pagination.
For games with larger server populations,
one `List` call cannot return the whole list.
Discovery addresses this by splitting capped queries
with observed server fields, collecting unique endpoints,
and returning a plan that can be reused on later polls.
The request budget is bounded; results can still be partial,
and Steam's changing list is not a consistent global snapshot.

With `client` and `ctx` already configured,
a minimal address discovery looks like this:

```go
root, err := serverlist.NewQueryFilter(
  []serverlist.Predicate{{Key: serverlist.KeyAppID, Value: "221100"}}, nil,
)
if err != nil {
  return err
}

result, err := client.DiscoverAddresses(ctx, root, serverlist.DiscoveryOptions{})
if err != nil {
  return err
}
fmt.Printf("%d addresses; complete: %t\n",
  len(result.Items), result.Status.Complete())
```

## Compatible HTTP handler

`NewHandler` adapts a `Provider` implementation to endpoint's response shape.
It can be used to serve an application-owned server list;
optional handler options support a required key and a maximum query limit.

## Related modules

* [`msqp`][msqp] implements the legacy UDP Master Server Query Protocol,
  including a client and a private master-server implementation.
* [`steamcm`][steamcm] queries server lists through Steam Connection Manager.

These packages have separate contracts and are not interchangeable.
Choose the backend that matches the game or Steam interface you need.

## Documentation

* [GetServerList endpoint reference][get-server-list]
* [Official Steamworks matchmaking API][matchmaking]
* [Steam Web API key registration][api-key]
* [`steamserverlist` on pkg.go.dev][pkg]

Run package tests with:

```sh
go test ./...
```

> [!NOTE]
> Regular tests do not contact Steam.
> The opt-in integration targets make up to four requests,
> one each for CS2, CS:S, DayZ, and Arma 3.  
> They print the returned servers and require `STEAM_API_KEY`
> in the environment or `.env`.

Run live checks explicitly with `make integration` or `make integration-race`.

<!-- links -->

[pkg]: https://pkg.go.dev/github.com/woozymasta/steamserverlist
[get-server-list]: https://steamapi.xpaw.me/#IGameServersService/GetServerList
[matchmaking]: https://partner.steamgames.com/doc/api/ISteamMatchmakingServers
[api-key]: https://steamcommunity.com/dev/apikey
[msqp]: https://github.com/woozymasta/msqp
[steamcm]: https://github.com/woozymasta/steamcm
