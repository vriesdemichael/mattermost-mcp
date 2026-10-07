# Installation

mm-mcp is a single binary for Linux, macOS and Windows, published with every
[release](https://github.com/vriesdemichael/mattermost-mcp/releases).

## Get a token

mm-mcp needs the address of your Mattermost server and one of:

- **A personal access token.** In Mattermost, open **Profile → Security →
  Personal Access Tokens**. If the section is missing, your administrator has
  not enabled them for you.
- **A bot token**, if your administrator gives you a bot account.
- **A session token**, from logging in yourself. A login command that does this
  through your browser, single sign-on included, is planned.

See [Configuration](configuration.md) for every setting.

## Claude Desktop

Download the `.mcpb` bundle for your machine from the
[latest release](https://github.com/vriesdemichael/mattermost-mcp/releases/latest),
such as `mm-mcp_<version>_darwin_arm64.mcpb` for an Apple silicon Mac, and open
it. Claude Desktop asks for the address and the token, and whether to allow
posting.

## Any MCP client

Download the archive for your machine from the
[latest release](https://github.com/vriesdemichael/mattermost-mcp/releases/latest),
put `mm-mcp` somewhere on your `PATH`, and add a server that runs `mm-mcp serve`
with the settings in its env block. For a client configured with JSON:

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

For Claude Code:

```bash
claude mcp add mattermost --env MM_URL=https://chat.example.com --env MM_TOKEN=your-token -- mm-mcp serve
```

Ask the agent who it is connected as. It calls `get_me` and answers with your
Mattermost user.

On Debian or Ubuntu, and on RHEL or Fedora, the release has packages that put
`mm-mcp` in `/usr/bin`:

```bash
curl -LO "https://github.com/vriesdemichael/mattermost-mcp/releases/latest/download/mm-mcp_<version>_linux_amd64.deb"
sudo dpkg -i mm-mcp_<version>_linux_amd64.deb
```

With Go installed, `go install github.com/vriesdemichael/mattermost-mcp/cmd/mm-mcp@latest`
builds it from source instead; such a build reports its version as `dev`.

## Over HTTP

```bash
mm-mcp serve --transport http
```

The server listens on `http://127.0.0.1:8765/mcp`. It authenticates no client
yet, so it refuses to listen anywhere but a loopback address
([ADR-020](adr/020-stdio-and-streamable-http-single-tenant-first.md)).

## Verifying a download

Every archive and bundle is signed, and every archive's SBOM is attested. With
the GitHub CLI:

```bash
gh attestation verify mm-mcp_<version>_linux_amd64.tar.gz --repo vriesdemichael/mattermost-mcp
```
