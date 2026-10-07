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

`MM_MCP_MARK_AI_GENERATED`
:   Optional, `true` by default. Every post and edit the model writes carries
    Mattermost's own marker for text written with AI, which its web app shows
    beside the post's time
    ([ADR-031](adr/031-a-message-is-checked-before-anyone-is-asked-to-post-it.md)).
    `false` leaves it out.

`MM_MCP_DOWNLOAD_DIR`
:   Optional. The directory `save_file` writes attachments to, when the server
    runs over stdio on your own machine. Your Downloads directory by default.
    An existing file is never overwritten
    ([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)).

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
