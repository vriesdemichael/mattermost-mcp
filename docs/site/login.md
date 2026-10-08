# Logging in

Where your Mattermost lets you make a personal access token, that token is the
simplest way in: put it in `MM_TOKEN` ([Installation](installation.md)). Many
organisations switch those tokens off. Then log in once with your own account:

```bash
mm-mcp login --url https://chat.example.com
```

and set only `MM_URL` for the server, without `MM_TOKEN`. mm-mcp keeps the
session in your system's credential store: the Windows Credential Manager, your
macOS keychain, or your Linux desktop's Secret Service. When Mattermost ends the
session, run `mm-mcp login` again; `mm-mcp logout` ends it yourself
([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).

## How mm-mcp logs you in

Before it opens anything, mm-mcp reads how your server signs people in, which
any server says to anyone: its login page's settings, and whether it is an OAuth
authorization server. Then it tries, in this order, what your server allows:

1. **OAuth, in your own browser.** Where your server's OAuth service is on and
   either lets mm-mcp register itself or has an app registered for it, mm-mcp
   opens Mattermost's authorization page in your default browser, whichever it
   is, Safari included, with your everyday profile. You are usually logged in
   there already, so you only approve mm-mcp. Mattermost sends your browser back
   to a page mm-mcp serves on `127.0.0.1` with a one-time code, which mm-mcp
   exchanges for a session. Nothing reads your browser's cookies.
2. **A browser window of mm-mcp's own**, for single sign-on: SAML, Entra ID,
   OpenID Connect, GitLab, Google. mm-mcp starts a Chrome, Edge, Chromium or
   Firefox installed on your machine, or the Chromium Playwright downloads, with
   a profile of its own that it deletes afterwards, at your server's login page.
   You log in as you always do, second factor included; mm-mcp watches the
   window's cookies until Mattermost sets its session cookie, and closes it.
   Because the profile is new, you log in fresh there, even when your everyday
   browser is logged in.
3. **Your password, in the terminal**, where your server keeps passwords: an
   email address or username, the password, and your authenticator's code when
   your account has a second factor. An account made through single sign-on has
   no password in Mattermost, and is told so.
4. **A token you paste**, as the last resort: a personal access token, a bot's
   token, or the value of the `MMAUTHTOKEN` cookie your browser holds for the
   server, from its developer tools while you are logged in (Application or
   Storage, then Cookies).

`--with oauth`, `--with window`, `--with password` or `--with paste` picks one.
Whatever way you log in, mm-mcp checks the session with Mattermost before it
keeps it.

## When the browser window will not open

A browser can refuse to be watched:

- **A company policy** that forbids remote debugging in Chrome or Edge. mm-mcp
  waits twenty seconds for the browser, names it, and tries the next one.
- **A snap or a Flatpak browser** on Linux, whose sandbox keeps it from using a
  profile of mm-mcp's.

On Ubuntu 24.04, a Chromium without an AppArmor profile, Playwright's included, is
refused its own sandbox; mm-mcp then starts it without one, as Playwright does,
and says so. The window shows only your login page and closes once you have
logged in.

Playwright's Chromium is a browser of your own, in your user folder, which those
policies do not reach. Install it once, with Node:

```bash
npx playwright install chromium
```

and run `mm-mcp login` again: mm-mcp finds it on its own. `--browser` names any
other Chrome, Edge, Chromium or Firefox, by path or command.

Your organisation's single sign-on may also require a managed device or browser,
as Entra ID's Conditional Access and Okta's device trust can. A fresh profile is
then refused at the sign-on page itself, Playwright's included; OAuth in your own
browser, or a pasted token, still works.

## What to ask your administrator

One change on the server lets everyone log in through their own browser, in
one click:

- **Turn on dynamic client registration**: System Console > Integrations >
  Integration Management > Enable OAuth 2.0 Service Provider, and Enable Dynamic
  Client Registration. mm-mcp then registers itself once per person and server.
- **Or register one OAuth app for mm-mcp**: a public client, without a secret,
  with the callback `http://127.0.0.1:8766/callback`, and share its client id.
  Everyone then runs `mm-mcp login --client-id <id>` once; mm-mcp remembers it
  for the server. `--callback-port` uses another port, when the app's callback
  names one.

To see what your server offers, open
`https://chat.example.com/.well-known/oauth-authorization-server`: an error page
means its OAuth service is off; a JSON answer means it is on, and with a
`registration_endpoint` in it, mm-mcp can register itself.

## With an AI agent

An agent can run `mm-mcp login` for you; you log in in the window or approve in
your browser. Never paste a token into the conversation: the agent should run
`mm-mcp login --with paste` and let you type or pipe the token into the command
itself. The [mm-mcp plugin for Claude Code](https://github.com/vriesdemichael/mm-mcp/tree/main/plugins/mm-mcp)
has a skill that guides an agent through this, including installing
Playwright's Chromium when no browser will do.
