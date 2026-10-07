---
search:
  boost: 0.3
---

# ADR-001: Go, the official MCP SDK, and Mattermost's own client

mm-mcp is written in Go, at the toolchain go.mod pins, and serves MCP through the official Go SDK, github.com/modelcontextprotocol/go-sdk. It reaches Mattermost through Mattermost's own Go client and types, model.Client4 and the model package from the server's public module (ADR-024). It ships as one static binary per platform (ADR-023).

The decisive reason is the data layer. Client4 and the model structs are the server's own code: the structs are what the server serialises, and Mattermost's API test suite drives the server through Client4 on every commit. Mattermost's published OpenAPI specification is written by hand and tested by nothing, and is wrong in ways only a live call reveals: its User schema has no is_bot field, which the struct cannot lack. The server's source at each release tag is also the most exact record of what changed between releases. The public module is Apache-2.0, as this project is.

Write handlers with typed input and output structs, so the SDK derives and validates both schemas. Use the model types for what mm-mcp sends to and reads from Mattermost, and a struct of mm-mcp's own for what a tool returns, holding only what the model needs. Do not add a second MCP library or a second Mattermost client.

## Not chosen

- **Python on FastMCP, with a client generated from the OpenAPI specification**: Built and measured first. The specification needed seven repairs before a generator would accept it and still lacked fields the server sends, and every repair was a belief to keep current. FastMCP's ready-made HTTP authentication is the real loss; it is written here when hosted operation needs it (ADR-020).
- **TypeScript**: Mattermost's web client is TypeScript, but the server's own Go types and client are what its API tests exercise.
