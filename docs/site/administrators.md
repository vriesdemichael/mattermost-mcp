# For administrators

This page is for those who run the Mattermost server people want to use mm-mcp
with. It explains why a supported way in is recommended, how to provide one,
how to recognise mm-mcp and end its access, and what it costs the server.
[Security](security.md) describes what data leaves a person's machine and what
an agent can do.

## Why a visible way in is the safer choice

An AI agent no longer needs an integration to reach Mattermost. An agent that
can operate a browser, through computer use or a browser extension, can open
Mattermost where the person is already logged in, and read and write as them.
Disabling personal access tokens or remote debugging does not prevent this;
only controls on the agents and extensions themselves, such as endpoint
management, can. Where such agents are permitted, the remaining question is how
they reach Mattermost:

| | Through the person's browser | Through mm-mcp |
|---|---|---|
| What it reads | Whatever is on the screen, Mattermost or not | Mattermost, through its API, nothing else |
| Posting | As the person, unmarked | Not offered until writes are allowed |
| Before a post others see | Nobody is asked | A person is asked by default |
| Telling its posts apart | Not possible | Marked as written with AI, by default |
| Telling its requests apart | Not possible | `User-Agent: mm-mcp/<version>` |
| Ending it | Ending the person's own session | Deleting its OAuth app, or ending its session |

mm-mcp is designed to make the second column the convenient one. Once the
server offers Mattermost's OAuth, the person logs in through their own browser
in one step, and `mm-mcp login` uses no other way unless the person explicitly
asks for one: not a browser window of its own, not a Chromium outside your
browser policies, not a pasted token.

This reasoning will not fit every organisation's policy, and it is not offered
as a guarantee. Its point is narrower: closing the supported ways in does not,
on its own, keep agents out, and it can leave them with ways in that are harder
to see. mm-mcp follows this as a design principle
([ADR-034](adr/034-agents-are-let-in-visibly-not-kept-out.md)).

## Providing a way in

Both options below let people log in through Mattermost's own OAuth service. It
works alongside your single sign-on rather than replacing it: the person signs
in as usual, then approves mm-mcp on Mattermost's authorization page. Both
require System Console > Integrations > Integration Management > Enable OAuth
2.0 Service Provider, which every supported release offers
([Supported releases](mattermost-releases.md)).

To check what a server offers, open
`https://chat.example.com/.well-known/oauth-authorization-server`. An error page
means its OAuth service is off; a JSON answer means it is on, and a
`registration_endpoint` in it means dynamic client registration is on as well.

### One OAuth app for everyone

This option keeps the most under your control. Register one public client, an
app without a secret whose authorization codes are protected by PKCE, with the
callback `http://127.0.0.1:8766/callback`. Mattermost's API creates a public
client with `is_public`, as mm-mcp's own tests do. With your token in
`MM_ADMIN_TOKEN`, a personal access token or your own session's:

```bash
printf 'Authorization: Bearer %s\n' "$MM_ADMIN_TOKEN" | curl -H @- -X POST https://chat.example.com/api/v4/oauth/apps \
  -H "Content-Type: application/json" \
  -d '{"name": "mm-mcp", "description": "MCP server for AI agents", "homepage": "https://github.com/vriesdemichael/mm-mcp", "callback_urls": ["http://127.0.0.1:8766/callback"], "is_public": true}'
```

`printf` is built into the shell, so the token appears in no process list; curl
reads the header from its input. The response has an empty `client_secret`,
and its `id` is the client id to share. Each person then runs
`mm-mcp login --client-id <id> --url https://chat.example.com` once, and mm-mcp
remembers the client for the server. Leave the app untrusted, its default, so
that each person approves mm-mcp on the authorization page.

### Or dynamic client registration

Integration Management > Enable Dynamic Client Registration. mm-mcp then
registers an app of its own the first time each person logs in, and no client
id needs to be shared. Be aware that any program that can reach the server can
then register an OAuth app, as the standard intends, and that there will be one
app per person who uses mm-mcp. A registered app obtains nothing until a person
logged in to Mattermost approves it.

## Recognising mm-mcp

- **Its posts** are marked as written with AI by default, as Mattermost's web
  app shows beside the post's time. Each person can turn the mark off; the
  server cannot require it.
- **Its requests** carry `User-Agent: mm-mcp/<version>`, visible in a proxy's or
  load balancer's logs, including those of its login. The header is declared by
  mm-mcp itself: it identifies mm-mcp, but cannot enforce anything. A login
  through mm-mcp's own browser window happens in that browser, so that session
  looks like any browser's.
- **Its OAuth app**, when people log in through OAuth: the one you registered,
  or one per person with dynamic registration.

## Ending access

- **For everyone who logged in through OAuth:** delete the OAuth app. This ends
  every session made through it at once, which the live test suite verifies on
  each supported release. With dynamic client registration, each person's app is
  deleted separately; disabling registration prevents new ones.
- **For one person:** revoke their sessions in System Console > User Management >
  Users, or the personal access token they gave mm-mcp. Revoking sessions also
  ends the person's browser sessions. mm-mcp then stops at start and asks the
  person to log in again; a revocation does not prevent a new login.
- **A pasted token** is the person's own credential: a personal access token, or
  the session of their browser. `mm-mcp logout` forgets it without ending it.
- **Personal access tokens:** Integration Management > Enable Personal Access
  Tokens, and each account's permission to create them, under User Management >
  Users > Manage roles.

## What it costs the server

mm-mcp sends its requests one at a time, never in parallel, and caches nothing
between tool calls. A tool call makes a small number of API requests: about
five to read a channel; for a search, one plus one for each channel and team in
the results; about a dozen for a post, across asking and posting. On a `429`
response, it waits as `Retry-After` indicates, up to ten seconds and at most
three times, then reports the error. A request times out after 30 seconds, a
file transfer after 15 minutes.

## Rolling it out

mm-mcp has no central policy of its own. Each person installs it, and its
settings live in their AI application's configuration
([Configuration](configuration.md)). What can be decided centrally:

- **In Mattermost:** which accounts may create personal access tokens, which bots
  exist and which channels they belong to, how long sessions last (Environment >
  Session Lengths), and which of the two ways in above is offered.
- **In the AI application**, where it supports managed settings: Claude Code's
  [managed settings](https://code.claude.com/docs/en/settings) can set the
  environment and the permission rules for everyone, for example keeping writes
  off, or requiring a person to approve every post.

## What is tested

Every tool is tested against Mattermost Team Edition, on the current Extended
Support Release and the newest release, on every change
([Supported releases](mattermost-releases.md)). mm-mcp also works with the
licensed editions, which serve the same API, but offers no tools for features
only they have, such as scheduled posts, and those are not tested.

The login is the exception. Single sign-on through SAML, Entra ID or OpenID
Connect is a licensed feature, and mm-mcp logs in through it. The test suite
covers the login against Team Edition's own login page in every browser it
uses, on every system it ships for; single sign-on through a real identity
provider has so far been tested by hand, on Windows.
