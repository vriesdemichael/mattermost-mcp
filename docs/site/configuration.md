# Configuration

mm-mcp reads its configuration from its environment, which an MCP client sets in
the server's env block. No setting is a command-line flag that carries a secret
([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)). A value a client
leaves as an unexpanded placeholder, such as `${user_config.mm_token}` for a
setting left empty, counts as unset.

## Environment variables

`MM_URL`
:   Required. The address you open Mattermost at, such as
    `https://chat.example.com`. An `http` or `https` address, without a query;
    the API's own `/api/v4` at its end is dropped. A plain `http` address
    beyond this machine is warned about at start, since the token would cross
    the network unencrypted. A redirect to another address is refused and
    named, so set the address Mattermost is served at.

`MM_TOKEN`
:   The credential mm-mcp acts with: a personal access token, a bot token, or
    a session token. The server acts as whoever owns it. Leave it unset once
    `mm-mcp login` keeps a token or a login for `MM_URL`, which is then used; a
    token set here goes first.

`MM_MCP_ALLOW_WRITES`
:   Optional, `false` by default. `true` offers the tools that post and change
    things in Mattermost. A change others see, such as a post, a reply, an
    edit, a deletion, a reaction or a pin, asks you before it acts, through a
    question your MCP client must be able to show
    ([Tools](tools.md#how-a-write-is-asked)), unless
    `MM_MCP_ASK_BEFORE_WRITES` leaves the asking to your client. A change
    that is yours alone or gone in seconds does not: following a thread, saving
    a post, setting a reminder, saving a draft, and showing that you are typing
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)). Accepts
    `true`, `false`, `1`, `0`, `yes`, `no`, `on` and `off`.

`MM_MCP_ASK_BEFORE_WRITES`
:   Optional, `true` by default. Whether mm-mcp asks you before each change
    others see, once `MM_MCP_ALLOW_WRITES` is true
    ([ADR-033](adr/033-who-asks-before-a-write-is-a-setting.md)). Changes that
    are yours alone or gone in seconds never ask, whatever it is set to.

    `false` leaves the asking to your MCP client: mm-mcp asks nothing, and the
    client's own approval of each tool call, as its permission rules decide, is
    the only check. Use it where your client does not show mm-mcp's question,
    such as the Claude desktop app, which declines it unseen, or for an agent
    you allow to post on its own. The model is told that mm-mcp does not ask,
    and mm-mcp says so in its log at start.

    !!! warning "With `false`, a client that approves on its own posts as you"
        Nothing in mm-mcp stops a post. A client that approves tools by itself,
        such as Claude Code in auto mode, after "don't ask again", or under a
        rule that allows the tool, posts under your name with nobody seeing it
        first. In Claude Code, an `ask` rule or
        `MM_MCP_FORCE_HUMAN_IN_THE_LOOP_IN_CLAUDE_CODE` keeps a person on every post.

    Claude Code's prompt shows a tool's input, so a channel or post named by its
    id shows as that id, and a direct message to yourself names nobody.

`MM_MCP_FORCE_HUMAN_IN_THE_LOOP_IN_CLAUDE_CODE`
:   Optional, `false` by default, and only with `MM_MCP_ASK_BEFORE_WRITES=false`;
    with mm-mcp asking too, you would be asked twice for one write, so mm-mcp
    does not start. `true` marks each tool that would ask so that Claude Code
    asks a person on every call, in every permission mode, auto mode included,
    and no allow rule or "don't ask again" skips it
    ([Claude Code](https://code.claude.com/docs/en/mcp#require-approval-for-a-specific-tool)).
    Use it to keep a person on every post, for example with a model you trust
    less. An agent that should post on its own cannot while it is on. It needs
    Claude Code 2.1.214 or later. An application built on Claude Code's SDK,
    such as the Claude desktop app, is handed each such call to approve and is
    expected to show it to a person; check that yours does before you rely on
    it. The mark is Claude Code's own: any other MCP client ignores it and asks
    as its own rules say.

    Without it, Claude Code's own rules can keep a person on these tools, in
    `.claude/settings.json`. An `ask` rule prompts in every permission mode,
    auto mode included, and comes before any `allow` rule, a "don't ask again"
    one included; an `allow` rule lets an agent post on its own
    ([Claude Code permissions](https://code.claude.com/docs/en/permissions)).
    With the server registered as `mattermost`:

    ```json
    {
      "permissions": {
        "ask": [
          "mcp__mattermost__create_post", "mcp__mattermost__dm",
          "mcp__mattermost__group_message", "mcp__mattermost__update_post",
          "mcp__mattermost__delete_post", "mcp__mattermost__add_reaction",
          "mcp__mattermost__remove_reaction", "mcp__mattermost__pin_post",
          "mcp__mattermost__join_channel", "mcp__mattermost__leave_channel",
          "mcp__mattermost__add_channel_members", "mcp__mattermost__create_channel",
          "mcp__mattermost__set_status", "mcp__mattermost__delete_draft"
        ]
      }
    }
    ```

`MM_MCP_MARK_AI_GENERATED`
:   Optional, `true` by default. Every post and edit the model writes carries
    Mattermost's own marker for text written with AI, which its web app shows
    beside the post's time
    ([ADR-031](adr/031-a-message-is-checked-before-anyone-is-asked-to-post-it.md)).
    `false` leaves it out.

`MM_MCP_DOWNLOAD_DIR`
:   Optional. The directory `save_file` writes attachments to, when the server
    runs over stdio on your own machine. Your Downloads directory by default.
    An existing file is never overwritten
    ([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)).

`MM_MCP_CA_FILE`
:   Optional. A PEM file of certificate authorities to trust beside your
    system's own, for a Mattermost whose certificate your organisation signed
    itself. `mm-mcp serve`, `login`, `logout` and `doctor` all trust it; set it
    in your terminal for `mm-mcp login` as in your MCP client for the server. A
    file that cannot be read, or holds no certificate, stops `serve`, `login`
    and `logout` at start and says so, and `doctor` reports it.

`MM_MCP_BLOCK_EXTERNAL_NETWORK`
:   For mm-mcp's own tests. While it is `1`, mm-mcp refuses to reach any address
    but the loopback ones
    ([ADR-006](adr/006-a-unit-test-inherits-nothing-and-reaches-nothing.md)).
    Leave it unset.

## Command line

`mm-mcp serve`
:   Runs the server over stdio, for a client that starts it as a process.

`mm-mcp serve --transport http [--host 127.0.0.1] [--port 8765]`
:   Runs it over Streamable HTTP, on a loopback address only.

`mm-mcp login [--url https://chat.example.com] [--with oauth|window|password|paste] [--browser path] [--client-id id] [--callback-port 8766]`
:   Logs you in the way the server allows, checks the session with Mattermost,
    and keeps it in your system's credential store for that server:
    OAuth in your own browser, a browser window of its own for single sign-on,
    your password in the terminal, or a token you paste
    ([Logging in](login.md)). `--with` picks one way, `--browser` the browser
    for a window, and `--client-id` the OAuth app an administrator registered
    for mm-mcp, whose callback is on `--callback-port`. `--url` is `MM_URL` when
    not given ([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).

    `--with paste` keeps a personal access token or a bot's token the same way,
    so it need not sit in your MCP client's configuration. On success it prints
    `Logged in to … as @you` on standard output and exits with status 0; when no
    way worked, it sums up each way it tried and why it failed, on standard
    error, and exits with status 1; a wrong flag exits with status 2.

`mm-mcp logout [--url https://chat.example.com]`
:   Ends the stored session at Mattermost, and forgets it.

`mm-mcp doctor [--url https://chat.example.com] [--json]`
:   Checks what mm-mcp needs, and says what to fix for each check that fails:
    every variable above, the login `mm-mcp login` stored for the address and
    whether `MM_TOKEN` hides it, the proxy, whether the server answers as a
    supported Mattermost, whose credential it is and whether that user belongs
    to a team, and how `mm-mcp login` would log you in there, with the browsers
    a login window could use. It reads the terminal's environment, which is not
    your MCP client's: `--url` names the server, and the `diagnose` tool
    ([Tools](tools.md#diagnose)) checks the client's own
    configuration from inside it. It changes nothing, and never prints the
    credential; it does print the address and your username. `--json` gives the
    checks to an agent. It exits with status 1 when a check failed.

`mm-mcp version`
:   Prints the installed version.

`mm-mcp help`
:   Prints the commands and where the configuration comes from. `--help` and
    `-h` do the same.

A configuration error exits with status 2 and says which setting is wrong.

Before it serves, `mm-mcp serve` asks Mattermost who the token belongs to, and
says so in the client's log. A token Mattermost refuses, or an address that
does not answer as Mattermost, stops it with status 2 and what to fix. A server
it cannot reach is only warned about, and the tools say so until it can be
reached. A server older than the oldest supported release
([Supported Mattermost releases](mattermost-releases.md)) is warned about too.
These are the checks `mm-mcp doctor` makes of the server, so the two agree.
