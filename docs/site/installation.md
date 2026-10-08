# Installation

mm-mcp is a single binary for Linux, macOS and Windows, published with every
[release](https://github.com/vriesdemichael/mm-mcp/releases).

## Install

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

On macOS, an app started from the Dock may not look where Homebrew puts
programs at all. Where your MCP client says it cannot find `mm-mcp`, give the
command by its full path, which `which mm-mcp` prints on macOS and Linux and
`where.exe mm-mcp` on Windows.

## Get a token

mm-mcp needs the address of your Mattermost server and one of:

- **A personal access token.** In Mattermost, open **Profile → Security →
  Personal Access Tokens**. If the section is missing, your administrator has
  switched them off, under System Console > Integrations > Integration
  Management > Enable Personal Access Tokens, or has not let your account make
  them, under System Console > User Management > Users > your account > Manage
  roles.
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
manager ([above](#install)), also when Claude Desktop runs the copy in its
bundle. See [Configuration](configuration.md) for every setting.

## Claude Code

The mm-mcp plugin sets up the server. With mm-mcp installed with a package
manager ([above](#install)), in Claude Code:

```text
/plugin marketplace add vriesdemichael/mm-mcp
/plugin install mm-mcp@mm-mcp
```

It asks for your Mattermost's address, and a token: leave it empty once
`mm-mcp login` keeps one ([above](#get-a-token)). Use the plugin or
`claude mcp add` ([below](#any-mcp-client)), not both: each adds a server named
`mattermost`.

## Claude Desktop

Download the `.mcpb` bundle for your machine from the
[latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest),
such as `mm-mcp_<version>_darwin_arm64.mcpb` for an Apple silicon Mac or
`mm-mcp_<version>_windows_amd64.mcpb` for Windows, and open it. Claude Desktop
asks for the address and a token, which you leave empty once `mm-mcp login`
keeps one ([above](#get-a-token)); whether to allow posting; whether mm-mcp asks
before posting; whether to mark posts as written with AI; and where to save
files. mm-mcp's question before a post has been tried in the Claude desktop
app only in its Code tab, where Claude Code declines it without showing it.
Where your app does not show it either, turn asking off, and the app's own
approval of each tool call is the check instead
([Configuration](configuration.md)); the `diagnose` tool, with
`ask_test_question`, tells you which ([Tools](tools.md#diagnose)).

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

For two Mattermost servers, such as work and an open-source community, add a
second server under another name, with its own `MM_URL`. `mm-mcp login` keeps
a login for each address, and `mm-mcp logout --url` names the one to end.

With Go installed, `go install github.com/vriesdemichael/mm-mcp/cmd/mm-mcp@latest`
builds it from source instead; such a build reports its version as `dev`.

## First steps

Once your AI app is connected, ask it in your own words. Some to start with, and
the [tools](tools.md) it reaches for:

| Ask | What it does |
|---|---|
| Who am I in Mattermost? | Checks the connection, with `get_me` |
| What did I miss? Catch me up on my unread channels. | Finds the channels with something unread, with `get_user_channels`, and reads each from where you stopped, with `read_unread`, without marking anything read |
| What mentions me since Monday? | `list_mentions` |
| What did we decide about the release date? | `search_posts`, and `read_post` for the thread around a match |
| Draft a reply to Sam in ~release-planning that I can send myself. | Puts a draft in your message box in Mattermost, with `save_draft`, once writes are allowed |
| Reply in that thread that the notes are ready. | Posts with `create_post`, once writes are allowed, asking you first |

Writes are off until you allow them, with `MM_MCP_ALLOW_WRITES` or your app's
"Allow posting" setting ([Configuration](configuration.md)).

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

Over HTTP, what reaches your disk is not offered, since a client of an HTTP
server need not be you at this machine: `save_file`, and attaching a file by
its `path`. A file the model writes, given a name and its content, is still
attached. Anything on your machine that reaches the port acts as mm-mcp's
account
([Security](security.md#over-http)).

## Verifying a download

Every archive and bundle is signed, and every archive's SBOM is attested. With
the GitHub CLI:

```bash
gh attestation verify mm-mcp_<version>_linux_amd64.tar.gz --repo vriesdemichael/mm-mcp
```
