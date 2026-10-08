# Installation

mm-mcp is a single binary for Linux, macOS and Windows, published with every
[release](https://github.com/vriesdemichael/mm-mcp/releases).

## Get a token

mm-mcp needs the address of your Mattermost server and one of:

- **A personal access token.** In Mattermost, open **Profile → Security →
  Personal Access Tokens**. If the section is missing, your administrator has
  not enabled them for you.
- **A bot token**, if your administrator gives you a bot account.
- **Your own login**, for when personal access tokens are switched off.

Keep it in your system's credential store rather than in your MCP client's
configuration, where it would sit in a file as plain text:

=== "A token"

    ```bash
    mm-mcp login --with paste --url https://chat.example.com
    ```

    asks for the token without showing what you type or paste, checks it with
    Mattermost, and keeps it.

=== "Your own login"

    ```bash
    mm-mcp login --url https://chat.example.com
    ```

    logs you in the way your server allows: through your own browser where it
    offers OAuth, a browser window of its own for single sign-on, or your
    password in the terminal, and keeps the session.
    [Logging in](login.md) has every way, and what to ask your administrator.

Then your MCP client's configuration names only `MM_URL`, and `mm-mcp serve`
finds the token for it. Where there is no credential store, as over SSH, in WSL
or in a container, put the token in `MM_TOKEN` in the client's configuration
instead.

Both need the `mm-mcp` command in a terminal, so install mm-mcp with a package
manager (below), also when Claude Desktop runs the copy in its bundle. See
[Configuration](configuration.md) for every setting.

## Claude Code

The mm-mcp plugin sets up the server. With mm-mcp installed with a package
manager (below), in Claude Code:

```text
/plugin marketplace add vriesdemichael/mm-mcp
/plugin install mm-mcp@mm-mcp
```

It asks for your Mattermost's address, and a token: leave it empty once
`mm-mcp login` keeps one ([above](#get-a-token)).

## Claude Desktop

Download the `.mcpb` bundle for your machine from the
[latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest),
such as `mm-mcp_<version>_darwin_arm64.mcpb` for an Apple silicon Mac or
`mm-mcp_<version>_windows_amd64.mcpb` for Windows, and open it. Claude Desktop
asks for the address and a token, which you leave empty once `mm-mcp login`
keeps one ([above](#get-a-token)); whether to allow posting; whether mm-mcp asks
before posting; whether to mark posts as written with AI; and where to save
files. Where Claude Desktop does not show mm-mcp's question before a post, turn
asking off, and Claude Desktop's own approval of each tool call is the check
instead ([Configuration](configuration.md)).

## With a package manager

=== "Homebrew"

    On macOS or Linux:

    ```bash
    brew install vriesdemichael/tap/mm-mcp
    ```

=== "WinGet"

    On Windows:

    ```powershell
    winget install vriesdemichael.mm-mcp
    ```

=== "Scoop"

    On Windows:

    ```powershell
    scoop bucket add vriesdemichael https://github.com/vriesdemichael/scoop
    scoop install vriesdemichael/mm-mcp
    ```

=== "Debian, Ubuntu, RHEL, Fedora"

    The release has packages that put `mm-mcp` in `/usr/bin`:

    ```bash
    curl -LO "https://github.com/vriesdemichael/mm-mcp/releases/latest/download/mm-mcp_<version>_linux_amd64.deb"
    sudo dpkg -i mm-mcp_<version>_linux_amd64.deb
    ```

Each puts `mm-mcp` on your `PATH`, and `brew upgrade`, `winget upgrade` and
`scoop update` bring it to the newest release. WinGet takes a day or two to
review each new version, so it can lag the release by that long.

A program reads `PATH` when it starts. On Windows especially, a terminal, an AI
agent's shell or an MCP client that was already running does not find `mm-mcp`
until it is started again.

## Any MCP client

Install mm-mcp with a package manager, or download the archive for your machine
from the [latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest)
and put `mm-mcp` somewhere on your `PATH`. Then add a server that runs
`mm-mcp serve` with the settings in its env block, once `mm-mcp login` keeps
your token ([above](#get-a-token)):

=== "JSON configuration"

    ```json
    {
      "mcpServers": {
        "mattermost": {
          "command": "mm-mcp",
          "args": ["serve"],
          "env": {
            "MM_URL": "https://chat.example.com"
          }
        }
      }
    }
    ```

=== "Claude Code"

    ```bash
    claude mcp add mattermost --scope user --env MM_URL=https://chat.example.com -- mm-mcp serve
    ```

Restart the client, or reconnect the server, so it starts mm-mcp. Then ask the
agent who it is connected as: it calls `get_me` and answers with your
Mattermost user. When it does not, `mm-mcp doctor --url https://chat.example.com`
says what to fix.

With Go installed, `go install github.com/vriesdemichael/mm-mcp/cmd/mm-mcp@latest`
builds it from source instead; such a build reports its version as `dev`.

## Over HTTP

```bash
mm-mcp serve --transport http
```

The server listens on `http://127.0.0.1:8765/mcp`, and says so when it starts.
It authenticates no client yet, so it refuses to listen anywhere but a
loopback address
([ADR-020](adr/020-stdio-and-streamable-http-single-tenant-first.md)), and
refuses a request a browser marks as sent from a web page of another origin,
so a page you open cannot use it.

## Verifying a download

Every archive and bundle is signed, and every archive's SBOM is attested. With
the GitHub CLI:

```bash
gh attestation verify mm-mcp_<version>_linux_amd64.tar.gz --repo vriesdemichael/mm-mcp
```
