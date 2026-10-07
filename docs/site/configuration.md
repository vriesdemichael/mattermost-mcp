# Configuration

mm-mcp reads its configuration from its environment, which an MCP client sets in
the server's env block. No setting is a command-line flag that carries a secret
([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).

## Environment variables

`MM_URL`
:   Required. The address you open Mattermost at, such as
    `https://chat.example.com`. An `http` or `https` address, without a query.

`MM_TOKEN`
:   Required. The credential mm-mcp acts with: a personal access token, a bot
    token, or a session token. The server acts as whoever owns it.

`MM_MCP_ALLOW_WRITES`
:   Optional, `false` by default. `true` offers the tools that post and change
    things in Mattermost, and each of them asks you before it acts
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)). Accepts
    `true`, `false`, `1`, `0`, `yes`, `no`, `on` and `off`.

`MM_MCP_BLOCK_EXTERNAL_NETWORK`
:   For mm-mcp's own tests. While it is `1`, mm-mcp refuses to reach any address
    but the loopback ones
    ([ADR-006](adr/006-a-unit-test-inherits-nothing-and-reaches-nothing.md)).
    Leave it unset.

## Command line

`mm-mcp serve`
:   Runs the server over stdio, for a client that starts it as a process.

`mm-mcp serve --transport http [--host 127.0.0.1] [--port 8765]`
:   Runs it over Streamable HTTP, on a loopback address only.

`mm-mcp version`
:   Prints the installed version.

A configuration error exits with status 2 and says which setting is wrong.
