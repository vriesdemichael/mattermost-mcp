---
search:
  boost: 0.3
---

# ADR-020: stdio and Streamable HTTP from one server, single-tenant first

`mm-mcp serve` runs the same server over stdio, for a client that starts it as a local process, or over Streamable HTTP with `--transport http`. Both serve one Mattermost server as one identity (ADR-019). Over HTTP the server authenticates no client yet, so anyone who reaches the port acts as that identity: it binds 127.0.0.1 by default and refuses an address that is not a loopback address.

Hosted, multi-user operation is the direction, and the code is kept ready for it in one way: every tool reaches Mattermost through the `server.ClientFor` it was registered with, the one place that decides which identity a call acts as. A single-tenant server passes `server.Single`, which answers with its one client; a multi-tenant deployment passes a lookup keyed by the authenticated MCP client, and adds client authentication to the HTTP handler; no tool changes. Until then, a feature that would hold state across calls belongs to the identity, not to the process.

Reach Mattermost from a tool only through its ClientFor, never through a client held in a package variable or captured from elsewhere. Do not lift the loopback restriction without adding client authentication in the same change, and a record of how it works.

stdio is how desktop and IDE clients start a local server; HTTP is how a remote client, or a team, reaches a shared one. An unauthenticated HTTP server bound to a network address hands the configured identity to the network.

## Not chosen

- **Multi-tenant from the start**: It needs client authentication, a credential store per user and an operator to run it, before the tools exist that make it worth running.
- **stdio only**: The hosted future needs HTTP, and the SDK serves it from the same server with no second code path.
- **Bind any address and warn**: A warning in a log nobody reads is no protection for a token that acts as a person.
