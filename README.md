# mm-mcp

[![CI](https://github.com/vriesdemichael/mm-mcp/actions/workflows/ci.yml/badge.svg?branch=next)](https://github.com/vriesdemichael/mm-mcp/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/vriesdemichael/mm-mcp/branch/next/graph/badge.svg)](https://codecov.io/gh/vriesdemichael/mm-mcp)

An MCP server for Mattermost. It lets an AI agent read and search your Mattermost
and, when you allow it, post and reply, as you or as a bot.

mm-mcp is an independent project. It is not affiliated with, endorsed by or
supported by Mattermost, Inc.; Mattermost is a trademark of Mattermost, Inc.

> **Young, and released.** The tools read channels, threads, unread posts,
> mentions and files, search posts, files, people and channels, and, once you
> allow writes, post, reply, edit, react, pin, keep drafts, join, leave and
> create channels, and set your status. The
> [tools page](docs/site/tools.md) lists every one. Views in the chat for
> clients that render MCP Apps are planned; the
> [issues](https://github.com/vriesdemichael/mm-mcp/issues) are the plan.

## Why it works the way it does

- **Read-only unless you say otherwise.** The tools that post or change anything
  are not offered until `MM_MCP_ALLOW_WRITES` is true. Then every change others
  see asks you first: an agent acting with your token speaks as you to your
  colleagues. Following a thread, saving a post, a reminder, a draft only you
  see and the typing indicator are yours alone or gone in seconds, and do not
  ask. The question is an MCP elicitation, so posting needs a client that shows
  one, such as Claude Code.
- **Your token, a bot's, or your own login.** A personal access token, a bot
  token, or the session token from logging in yourself, for servers where
  personal access tokens are switched off.
- **Mattermost's own client.** Requests and answers use the server's own Go
  client and types, not a hand-written description of the API.
- **Every supported release, tested.** Every tool runs against a real Mattermost
  Team Edition, on the Extended Support Release and the newest release on every
  change, and on every release between them each week.
- **stdio and Streamable HTTP.** A client starts it as a local process, or it
  serves Streamable HTTP on a loopback address.

## Quick start

Install mm-mcp with `brew install vriesdemichael/tap/mm-mcp`,
`winget install vriesdemichael.mm-mcp` or
`scoop install vriesdemichael/mm-mcp` (after
`scoop bucket add vriesdemichael https://github.com/vriesdemichael/scoop`), or
download the binary for your machine from the
[latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest).
Then get a personal access token in Mattermost under **Profile → Security →
Personal Access Tokens**.

```json
{
  "mcpServers": {
    "mattermost": {
      "command": "mm-mcp",
      "args": ["serve"],
      "env": {
        "MM_URL": "https://chat.example.com",
        "MM_TOKEN": "your-token"
      }
    }
  }
}
```

Or, for Claude Code:

```bash
claude mcp add mattermost --env MM_URL=https://chat.example.com --env MM_TOKEN=your-token -- mm-mcp serve
```

Claude Desktop users can open the `.mcpb` bundle for their machine from the
latest release instead.

The [documentation](https://vriesdemichael.github.io/mm-mcp/) has every
setting, the tools, and the supported Mattermost releases.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The decisions behind how this project is
built are in [docs/site/adr](docs/site/adr/index.md).

## License

[Apache-2.0](LICENSE)
