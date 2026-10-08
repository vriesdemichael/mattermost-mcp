---
search:
  boost: 0.3
---

# ADR-021: Read-only by default, and every write others see asks the person

A server starts read-only. A tool that changes anything in Mattermost is registered only when `MM_MCP_ALLOW_WRITES` is true, so a read-only server does not list it and a client cannot call it. Once writes are allowed, what such a tool changes decides whether it asks.

A change other people see asks, every call: posting, replying, editing, deleting, reacting, pinning, uploading, joining or leaving a channel, adding people to one, creating one, and setting a status. The tool asks the person to confirm through MCP elicitation before it acts, showing what will be sent where and under whose name, and acts only when the person ticks the box and accepts. A client that cannot show an elicitation gets the error MCP defines for a missing client capability, and nothing is written. That is the default; `MM_MCP_ASK_BEFORE_WRITES=false` leaves the asking to the MCP client's own approval of each tool call instead (ADR-033).

A change that is the user's alone, or gone within seconds, does not ask: following a thread, saving a post, writing a draft only they see, marking a channel read when they ask, or showing that they are typing. Such a tool names why in its spec's `Unasked`, and may not be destructive. Asking for each of these would teach the person to tick the box without reading, which is what the question for a post depends on them not doing.

What the person has read is not the agent's to change. Mattermost's unread counts, read marks and "mark as unread" record whether a person read something, and a model ingesting a channel is not the person reading it. Reading never marks anything read as a side effect, and no tool marks anything unread. One tool, `mark_channel_read`, marks a channel read, and its description holds the model to doing so only when the person asks: a person triaging through an agent says which channels they are done with, and badges they cannot clear without opening each channel themselves would make the agent's catch-up useless. It changes only the person's own record, so it does not ask.

Every tool declares a title and all four hints, each set by what the MCP specification defines it to mean, not by whether the tool asks. openWorldHint is false throughout, because one configured Mattermost server is a closed domain. Whether a tool changes Mattermost is its readOnlyHint, and nothing else: `server.Exposed` offers a tool not annotated read-only only when writes are allowed. A tool that writes files on the person's machine is offered on its own terms (ADR-029). The governance tests hold all of this (ADR-015), and the live suite reads back that a declined or unanswerable confirmation leaves Mattermost unchanged.

What a write acts on is what the person was asked about. A question is bound to the call's input and to what the call acts on: the files' bytes, the people a username or email means, the text of a post to edit, the post to delete and the replies that go with it, the text of a draft to delete. The answer accepts the call only while that is unchanged, and the tool checks once more just before it acts, so a reply posted, a file rewritten or a draft typed on while the person reads the question stops the write instead of going with it unseen.

Annotate a tool that changes Mattermost as not read-only, which keeps it out of a read-only server through `server.Exposed`. When others see the change, wrap the tool in the one confirmation helper, `asking`, and build the question from its arguments, quoting text the person or the model wrote as text. When only the user sees it, or it lasts seconds, mark the spec `unasked` with the reason. Never skip the confirmation for a client by what it says it can do, and never vary which tools are listed by it.

The credential may be a person's own session or token, on their employer's server, and a post appears under their name to colleagues. Reading is what most requests need; writing is the exception, and the person, not the model, decides each write others will see. A tool that is missing because writes are off is visible in the configuration; a write that happened without the person seeing it is not visible until a colleague reads it. A followed thread or a saved post is visible to no one else, and a typing indicator is gone before anyone could act on it.

## Not chosen

- **Trust the client's own tool approval by default**: Some clients approve tools automatically, and their prompt shows arguments as JSON rather than the message as it will read. ADR-033 lets a person choose it, for a client that does not show mm-mcp's question.
- **Confirm only in an MCP Apps view**: A client that renders no views would then have no way to ask.
- **A flag that writes without asking**: It makes the change with nobody asked; a client that cannot confirm a write should not be making it. `MM_MCP_ASK_BEFORE_WRITES=false` (ADR-033) is not that flag: it moves the asking to the client's own approval, which the person sets up.
- **Ask for every change, the user's own included**: A question for each followed thread or typing indicator wears out the attention the question for a post needs.
- **Mark what the model read as read**: The unread count is the person's record of what they have seen; the model reading a channel for them does not mean they have.
