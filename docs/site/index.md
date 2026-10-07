# mm-mcp

mm-mcp is an MCP server for Mattermost. It lets an AI agent read and search your
Mattermost, and, when you allow it, post and reply, as you or as a bot.

mm-mcp is an independent project. It is not affiliated with, endorsed by or
supported by Mattermost, Inc.; Mattermost is a trademark of Mattermost, Inc.

!!! warning "Early development"
    mm-mcp is at the start of its life. One tool exists today, `get_me`, which
    proves the connection and the credential. Reading, searching and posting are
    being built. The [issues](https://github.com/vriesdemichael/mm-mcp/issues)
    are the plan.

## What it is for

- **Reading and searching** channels, threads, unread messages and mentions, and
  looking up people and channels.
- **Posting and replying**, editing your own posts and reacting, once you allow
  writes, with each one confirmed by you before it is sent.
- **Files and attachments**, read and uploaded.
- **Views in the chat**: in a client that renders MCP Apps, a thread appears as
  Mattermost lays it out, and a draft appears before it is posted.

## How it behaves

- **Read-only unless you say otherwise.** The tools that post or change anything
  are not even offered until `MM_MCP_ALLOW_WRITES` is true, and then each call asks
  you first ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).
- **One server, one identity.** It acts as whoever owns the token it is given: a
  personal access token, a bot's token, or a session token from your own login
  ([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).
- **stdio or HTTP.** A desktop or IDE client starts it as a local process; over
  Streamable HTTP it serves a loopback address for now
  ([ADR-020](adr/020-stdio-and-streamable-http-single-tenant-first.md)).
- **Tested against a real Mattermost.** Every tool is exercised against Team
  Edition, on the current Extended Support Release and the newest release
  ([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).

Start with [Installation](installation.md).
