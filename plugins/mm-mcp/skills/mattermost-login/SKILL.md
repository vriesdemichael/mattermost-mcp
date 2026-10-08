---
name: mattermost-login
description: Log the person in to their Mattermost for the mm-mcp MCP server, or find out why mm-mcp does not work. Use when the mm-mcp tools answer that the token was refused or expired, when mm-mcp serve says no login is stored, when the person asks to connect mm-mcp to Mattermost, or when they cannot make a personal access token. Never handle the token yourself.
---

# Logging in to Mattermost for mm-mcp

mm-mcp acts as one Mattermost identity. `mm-mcp login` obtains a session for the
person and keeps it in their system's credential store, where `mm-mcp serve`
finds it when the MCP client sets `MM_URL` and leaves `MM_TOKEN` out. Your part
is to run the command and read what it says; the person logs in or approves.

## Rules

- **Never see, ask for, print or store a token.** Not in the conversation, not in a
  file, not in a command line, not in an environment variable you set. A session
  token is everything the person can do in Mattermost. When a token has to be
  pasted, the person types it into the command's own prompt, or pipes it in
  themselves.
- **Never fill in a login page or a password** for the person, and never drive the
  browser window mm-mcp opens. Tell them what to do in it.
- **Do not read browser profiles, cookie files or keychains.**

## Steps

1. **Find the server's address**: the `MM_URL` of the mm-mcp server in the MCP
   client's configuration, or ask the person for the address they open
   Mattermost at, such as `https://chat.example.com`.
2. **Check that mm-mcp is installed**: `mm-mcp version`. If not, point the person
   to https://vriesdemichael.github.io/mm-mcp/installation/ (Homebrew, WinGet,
   Scoop, or the release archives).
3. **Run the login in a terminal the person can see**, and tell them first what will
   happen:

   ```bash
   mm-mcp login --url https://chat.example.com
   ```

   mm-mcp reads how the server signs people in and picks the way:
   - It may open Mattermost in their **own browser** to approve mm-mcp (OAuth):
     tell them to approve it, and close the tab afterwards.
   - It may open a **browser window of its own** at the login page: tell them to log
     in there as they always do, single sign-on and second factor included. The
     window closes by itself.
   - It may ask for **a login and password** in the terminal, and the code from their
     authenticator app: the person types those, not you.

   The command waits up to ten minutes. When it prints `Logged in to … as @…`,
   it is done.
4. **Make sure the MCP client uses the login**: its mm-mcp server needs `MM_URL`
   set to the same address and no `MM_TOKEN`. Restart the MCP server, or the
   client, so it starts again; `mm-mcp serve` then prints `connected to … as @…`
   in the client's log.

## Checking the setup

Run this first when the tools answer that the token was refused or expired,
and after a login to confirm it took:

```bash
mm-mcp doctor --url https://chat.example.com
```

It checks the stored login, whether `MM_TOKEN` hides it, the way to the server,
and whose credential it is, and prints what to fix for each check that failed.
It never prints a token. `--json` gives you the checks to read. It reads this
terminal's environment, not the MCP client's: if it passes and the tools still
fail, call the mm-mcp server's `diagnose` tool, which checks the client's own
configuration from inside the server. With `ask_test_question`, and only when
the person agrees, it shows them a test question to find out whether their
client shows mm-mcp's questions before a write at all. If it does not, as
in the Claude desktop app, turn the plugin's Ask before posting setting off
(`MM_MCP_ASK_BEFORE_WRITES=false`): Claude Code's own permission prompt then
asks before each write instead.

## When the login fails

Read what the command printed; it names what failed and what works instead.

- **"no browser would let mm-mcp watch the login"**, or a browser that "opened no
  DevTools port": a company policy forbids remote control of Chrome or Edge, or
  the browser is a snap or Flatpak. Install Playwright's Chromium, which those
  policies do not reach, if the person agrees and Node is available:

  ```bash
  npx playwright install chromium
  ```

  then run `mm-mcp login` again. `--browser <path>` names any other Chrome, Edge,
  Chromium or Firefox.
- **The sign-on page refuses the fresh browser** (a managed device or browser
  required): only OAuth in the person's own browser, or a token, works. See the
  administrator step below.
- **"the account signs in through single sign-on and has no password"**: use the
  browser window: `mm-mcp login --with window --url …`.
- **As a last resort, a token the person pastes**: ask them to create a personal
  access token in Mattermost under Profile > Security > Personal Access Tokens, or
  to copy the value of the `MMAUTHTOKEN` cookie from their browser's developer
  tools while logged in (Application or Storage, then Cookies). Then run, in a
  terminal they type into:

  ```bash
  mm-mcp login --with paste --url https://chat.example.com
  ```

  The person pastes the token at the prompt. Do not ask them to give it to you.
- **For everyone in their organisation at once**: suggest the person asks their
  Mattermost administrator to turn on the OAuth 2.0 Service Provider and Dynamic
  Client Registration (System Console > Integrations > Integration Management),
  or to register a public OAuth app for mm-mcp with the callback
  `http://127.0.0.1:8766/callback` and share its client id, used as
  `mm-mcp login --client-id <id>`. Then everyone logs in through their own
  browser in one click.

## Afterwards

When Mattermost ends the session, the mm-mcp tools answer that the session
expired: run `mm-mcp login` again. `mm-mcp logout --url …` ends the session and
forgets it. More: https://vriesdemichael.github.io/mm-mcp/login/
