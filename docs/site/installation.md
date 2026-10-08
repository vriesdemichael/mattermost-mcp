# Installation

mm-mcp is a single binary for Linux, macOS and Windows, published with every
[release](https://github.com/vriesdemichael/mm-mcp/releases).

## Get a token

mm-mcp needs the address of your Mattermost server and one of:

- **A personal access token.** In Mattermost, open **Profile → Security →
  Personal Access Tokens**. If the section is missing, your administrator has
  not enabled them for you.
- **A bot token**, if your administrator gives you a bot account.
- **Your own login**, for when personal access tokens are switched off:

    ```bash
    mm-mcp login --url https://chat.example.com
    ```

    opens Mattermost's login page in a Chrome or Edge window of its own. Log in
    as you always do, single sign-on and second factor included; the window
    closes once you have, and the session is kept in your system's credential
    store: the Windows Credential Manager, your macOS keychain, or your Linux
    desktop's Secret Service. Then set only `MM_URL` for the server, without
    `MM_TOKEN`. When Mattermost ends the session, run `mm-mcp login` again;
    `mm-mcp logout` ends it yourself. Install mm-mcp with a package manager
    for this, also when Claude Desktop runs its bundle.

See [Configuration](configuration.md) for every setting.

## Claude Desktop

Download the `.mcpb` bundle for your machine from the
[latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest),
such as `mm-mcp_<version>_darwin_arm64.mcpb` for an Apple silicon Mac, and open
it. Claude Desktop asks for the address and the token, whether to allow
posting, whether to mark posts as written with AI, and where to save files.

## With a package manager

On macOS or Linux, with Homebrew:

```bash
brew install vriesdemichael/tap/mm-mcp
```

On Windows, with WinGet:

```powershell
winget install vriesdemichael.mm-mcp
```

or with Scoop:

```powershell
scoop bucket add vriesdemichael https://github.com/vriesdemichael/scoop
scoop install vriesdemichael/mm-mcp
```

Each puts `mm-mcp` on your `PATH`, and `brew upgrade`, `winget upgrade` and
`scoop update` bring it to the newest release. WinGet takes a day or two to
review each new version, so it can lag the release by that long.

## Any MCP client

Install mm-mcp with a package manager, or download the archive for your machine
from the [latest release](https://github.com/vriesdemichael/mm-mcp/releases/latest)
and put `mm-mcp` somewhere on your `PATH`. Then add a server that runs
`mm-mcp serve` with the settings in its env block. For a client configured with
JSON:

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
curl -LO "https://github.com/vriesdemichael/mm-mcp/releases/latest/download/mm-mcp_<version>_linux_amd64.deb"
sudo dpkg -i mm-mcp_<version>_linux_amd64.deb
```

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
