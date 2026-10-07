# mm-mcp

mm-mcp is an MCP server for Mattermost. It lets an AI agent read and search your
Mattermost, and, when you allow it, post and reply, as you or as a bot.

mm-mcp is an independent project. It is not affiliated with, endorsed by or
supported by Mattermost, Inc.; Mattermost is a trademark of Mattermost, Inc.

!!! warning "Early development"
    mm-mcp is young: its [tools](tools.md) read, search, post and handle files,
    and are tested against a real Mattermost, but nothing is released yet. The
    [issues](https://github.com/vriesdemichael/mm-mcp/issues) are the plan.

## What it is for

- **Reading and searching** channels, threads, unread messages and mentions, and
  looking up people and channels.
- **Posting and replying**, editing and deleting your own posts, reacting and
  pinning, once you allow writes, each confirmed by you before it is sent.
- **Your own things**: following threads, saving posts, reminders, drafts and the
  typing indicator, once you allow writes, without a question each time.
- **Files and attachments**, read, uploaded and saved to your machine.

Planned, not yet built: **views in the chat**, so that in a client that renders
MCP Apps a thread appears as Mattermost lays it out and a draft appears before
it is posted
([ADR-022](adr/022-mcp-apps-views-through-one-show-tool.md)).

## How it behaves

- **Read-only unless you say otherwise.** The tools that post or change anything
  are not even offered until `MM_MCP_ALLOW_WRITES` is true. Then each change
  others see asks you first; following a thread, saving a post, a reminder, a
  draft only you see and the typing indicator do not
  ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).
- **One server, one identity.** It acts as whoever owns the token it is given: a
  personal access token, a bot's token, or a session token from your own login
  ([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).
- **stdio or HTTP.** A desktop or IDE client starts it as a local process; over
  Streamable HTTP it serves a loopback address for now
  ([ADR-020](adr/020-stdio-and-streamable-http-single-tenant-first.md)).
- **Tested against a real Mattermost.** Every tool is exercised against Team
  Edition, on the current Extended Support Release and the newest release
  ([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).

## Next to Mattermost's own MCP server

Mattermost publishes an MCP server of its own, inside its Agents plugin. mm-mcp
differs on purpose:

- **Any edition, no plugin.** mm-mcp talks to Mattermost's REST API as a client
  does, so it runs against Team Edition as well; the other's write tools need an
  Enterprise licence, and it runs inside a plugin on the server.
- **As you.** It acts with your own token or a bot's, and every post says it was
  written with AI.
- **Asks before every write others see**, through your MCP client, showing what
  will be posted, where and as whom.
- **Tools shaped for an agent** rather than one per endpoint: a post comes with
  its author, channel, team, files and reactions, and names are found as a
  person writes them ([ADR-030](adr/030-names-are-matched-leniently-and-a-refusal-names-the-next-step.md)).

Start with [Installation](installation.md).
