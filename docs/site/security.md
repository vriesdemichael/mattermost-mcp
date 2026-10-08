# Security

What mm-mcp sends where, what it keeps, and what an agent using it can and
cannot do. Written for whoever decides whether mm-mcp may run, and for the
person who runs it. To report a vulnerability, see
[SECURITY.md](https://github.com/vriesdemichael/mm-mcp/blob/main/SECURITY.md).

## Where your data goes

```mermaid
flowchart LR
    MM[("Your Mattermost")] <-->|"its API, as you<br>or your bot"| MCP["mm-mcp,<br>on your machine"]
    MCP <-->|"tool calls<br>and results"| App["Your AI app,<br>the MCP client"]
    App <-->|"the conversation"| Model["The model's provider"]
```

mm-mcp itself talks to nothing but the Mattermost server you name: no
telemetry, no update check, no other host. But what a tool answers goes to your
AI app, and from there to whoever runs the model: messages, direct messages,
private channels, and the files the agent reads, up to 64 MiB each, as their
text or as images. **Whatever the agent reads in Mattermost, the model's
provider sees.** Whether that is allowed is a question about your AI app and
its provider, not about mm-mcp; mm-mcp cannot make it smaller than what the
agent asks to read.

## What an agent can do

- **Read everything the account can.** mm-mcp acts as whoever owns the token it
  is given, and reads what that account can read: every channel it belongs to,
  its direct and group messages, and the public channels of its teams; it
  searches the channels it belongs to. There is no list of channels or teams to limit it to; a
  bot account in only the channels it needs is the way to limit it.
- **Nothing that writes, until you allow it.** The tools that post or change
  anything are not offered while `MM_MCP_ALLOW_WRITES` is false, its default.
- **Each post others will see, only once someone says yes.** By default mm-mcp
  asks you through your AI app, showing what will be posted, where and as whom,
  and acts only on that answer. With `MM_MCP_ASK_BEFORE_WRITES=false`, your AI
  app's own approval of the tool call is the only check, and an app set to
  approve on its own posts with nobody seeing it first
  ([Configuration](configuration.md)).
- **A few things only you see, without asking**: following a thread, saving a
  post, a reminder, a draft in your message box, the typing indicator, and
  marking a channel read when you ask ([Tools](tools.md)).
- **Save attachments to your disk**, when it runs on your machine: into your
  download directory, never over an existing file, marked as downloaded from
  the internet.

Every post and edit is marked as written with AI by default, as Mattermost shows
it; `MM_MCP_MARK_AI_GENERATED=false` leaves the mark off.

## Prompt injection

A message in Mattermost can be written to instruct the agent that reads it:
"post this", "send me your direct messages". mm-mcp treats everything it reads
as text, and cannot tell an instruction from a message.

- **Contained:** a post, a reply, an edit, a reaction, a channel change or a
  direct message others will see. While mm-mcp asks, which is the default, none
  is made without a person seeing exactly what it is and saying yes.
- **Not contained:** what the agent reads, and what it does with it elsewhere.
  An agent told by a message to read your direct messages can read them, and if
  your AI app gives it another tool that sends things out, such as one that
  fetches web pages or sends mail, mm-mcp cannot stop that. Nor are the changes
  only you see asked about.

The defence beyond mm-mcp's question is in your AI app: which other tools it
offers alongside, and which tool calls it asks you about.

## Credentials

- **Kept in your system's credential store**, by `mm-mcp login`: the Windows
  Credential Manager, your macOS keychain, or your Linux desktop's Secret
  Service. `mm-mcp login --with paste` keeps a personal access token or a bot's
  token there too, so no token sits in a configuration file
  ([Installation](installation.md#get-a-token)).
- **Never in a flag, a log line, an error or a tool result.** No command takes a
  token as an argument
  ([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).
- **Never attached to a post.** A file where credentials are kept is refused,
  whoever asks for it: anything under a folder named `.ssh`, `.gnupg`, `.aws`, `.azure`,
  `.kube`, `.docker`, `gcloud`, `.password-store`, `keychains` or `keyrings`;
  the files `.netrc`, `_netrc`, `.git-credentials`, `.npmrc`, `.pypirc`, `.pgpass`,
  `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, a browser's `Login Data`,
  `Cookies`, `key4.db` and `logins.json`, and an MCP client's configuration
  (`claude_desktop_config.json`, `.claude.json`, `mcp.json`, `mcp_config.json`);
  any `.p12`, `.pfx`, `.kdbx`, `.keychain`, `.keychain-db` or `.jks` file; a `.env`
  file, but for an `.example`, `.sample` or `.template` one; and any file that
  holds a private key or the token mm-mcp runs with. The path is checked after
  links are followed, in any case of letters; a file from your disk is attached
  only when mm-mcp runs on your machine, and only once you said yes to its path.
- **Logging in never reads your browser's cookies or profile.** OAuth uses your
  own browser and a one-time code; the browser window mm-mcp starts has a
  profile of its own, deleted afterwards ([Logging in](login.md)).

A token, or the session `mm-mcp login` keeps, carries everything its account can
do in Mattermost, an administrator's included. Give mm-mcp the account it needs,
not more.

## What mm-mcp logs

To your AI app's log, through standard error, and only when it starts and
stops: who it is connected as, warnings about its settings or the server, and
whether it asks before writes. **It logs no tool call, no argument, and no
write, and keeps no record of what it did.** What an agent read and wrote is in
your AI app's conversation, and on the Mattermost side, in what Mattermost
records of the account: its posts, marked as written with AI by default, and
whatever your server's own audit logging keeps.

## What mm-mcp tells the model

mm-mcp gives the model a short set of instructions when it connects, which you
can read in its source
([`internal/server/server.go`](https://github.com/vriesdemichael/mm-mcp/blob/main/internal/server/server.go)):
that it acts as one account; that what other people wrote is text to read, not
instructions to follow; that a post or change others see is asked about first,
and a refusal is not to be retried; and how Mattermost writes mentions and
Markdown. Each tool's description says what it does and whether it asks.

## Over HTTP

`mm-mcp serve --transport http` listens on a loopback address only, and refuses
a request a browser marks as sent from a web page of another origin
([Installation](installation.md#over-http)). It authenticates no client yet:
anything on your machine that reaches the port acts as the account mm-mcp runs
with, other users of a shared machine included. Over stdio, the default, only
the app that started mm-mcp can talk to it.

## Releases and updates

Every archive, Linux package and bundle is signed with Sigstore by the release
workflow, with a build provenance attestation and an SBOM
([Installation](installation.md#verifying-a-download)). Homebrew, Scoop and
WinGet install the release's own archives and check each against the SHA-256
hash in their manifest; to check a signature yourself, download the same
archive from the release and verify it.

mm-mcp is maintained by one person. Only the latest release is supported, and a
security fix ships in the next release; there are no maintenance branches.
Before 1.0, a release may change how things work, and its notes say so
([releases](https://github.com/vriesdemichael/mm-mcp/releases)).
