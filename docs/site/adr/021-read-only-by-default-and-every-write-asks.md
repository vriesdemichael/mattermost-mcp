---
search:
  boost: 0.3
---

# ADR-021: Read-only by default, and every write asks the person

A server starts read-only. A tool that changes anything in Mattermost, such as posting, replying, editing, deleting, reacting or uploading, is registered only when `MM_MCP_ALLOW_WRITES` is true, so a read-only server does not list it and a client cannot call it. Once writes are allowed, each call to such a tool asks the person to confirm it through MCP elicitation before it acts, showing what will be sent where and under whose name; the tool acts only when the person accepts. A client that cannot show an elicitation gets the error MCP defines for a missing client capability, and nothing is written. There is no setting that writes without asking.

Every tool declares a title and all four hints, each set by what the MCP specification defines it to mean, not by whether the tool asks. openWorldHint is false throughout, because one configured Mattermost server is a closed domain. Whether a tool writes is its readOnlyHint, and nothing else: `server.Exposed` offers a tool not annotated read-only only when writes are allowed. The governance tests hold all of this (ADR-015), and the first write tool brings a test that a declined or unanswerable confirmation leaves Mattermost unchanged, read back live.

Annotate a write tool as not read-only, which keeps it out of a read-only server through `server.Exposed`, wrap it in the one confirmation helper, and build the confirmation from its arguments. Quote text the person or the model wrote as text. Never skip the confirmation for a client, and never vary which tools are listed by what a client can do.

The credential may be a person's own session or token, on their employer's server, and a post appears under their name to colleagues. Reading is what most requests need; writing is the exception, and the person, not the model, decides each one. A tool that is missing because writes are off is visible in the configuration; a write that happened without the person seeing it is not visible until a colleague reads it.

## Not chosen

- **Trust the client's own tool approval**: Some clients approve tools automatically, and their prompt shows arguments as JSON rather than the message as it will read.
- **Confirm only in an MCP Apps view**: A client that renders no views would then have no way to ask.
- **A flag that writes without asking**: It makes the change with nobody asked; a client that cannot confirm a write should not be making it.
