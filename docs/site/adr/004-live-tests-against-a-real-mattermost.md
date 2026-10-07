---
search:
  boost: 0.3
---

# ADR-004: Live tests against a real Mattermost are the correctness gate

Behaviour that depends on Mattermost is proven against a real Mattermost, in the live suite under tests/live, which carries the `live` build tag. What the server does is authoritative. Client4 and the model types say what a request sends and an answer holds (ADR-024), and the OpenAPI specification says what an endpoint accepts (ADR-026); neither says what the server does with it. Every MCP tool is called by at least one live test that asserts on what it returned, and `TestEveryToolIsCalledByALiveTest` fails when one is not. A tool counts as called only through an `mcp.CallToolParams{Name: "..."}` literal the scan can read, so keep the tool's name in the literal.

A live test that writes reads the result back with a separate request, as another user where that is what the tool claims, and writes a value other than the default. A 2xx proves nothing: Mattermost accepts a field it does not know and answers success, and a default reads back the same whether or not the write landed.

When a change does anything beyond parsing or local validation, add or update a live test. To fix a bug, reproduce it with a live test first, then look for the same assumption elsewhere and reproduce that too, then fix it, then break each new test by reverting the fix and watch it fail.

A test built from what its author believes about the API agrees with the code whenever that belief is the bug; only the server can disagree. Permissions, channel membership and server configuration behave in ways no description of the API captures.

## Not chosen

- **Contract tests or recorded fixtures**: They encode what the server was believed or seen to do, and keep passing when it does something else.
- **Trust Client4 because Mattermost tests it**: Mattermost tests that its client and server agree, not that a tool asks for what its user meant, nor how an older release answers.
