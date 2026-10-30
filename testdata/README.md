# Test fixtures

`dayz_steam_response_1000.json` contains 1,000 randomly sampled
DayZ server records (AppID 221100) from a saved Steam server-list result.
It uses `GetServerList` response shape and retains the fields used by discovery.
Sample selection is reproducible with seed `20260928`.

Server names and Steam IDs are synthetic.
Source IPs are mapped into the reserved `198.18.0.0/15` test range;
servers that shared a source IP still share its replacement,
and ports are retained.
The golden test replays these records through a local service
that applies the query filters and a 100-row page cap.
It checks complete discovery, exact endpoint coverage,
and reuse of the saved plan. No Steam requests are made by the test;
fixture does not describe the current server population.
