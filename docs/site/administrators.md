# For administrators

For whoever runs the Mattermost server people want to use mm-mcp with: why to
let it in, how, how to recognise and end it, and what it costs the server.
[Security](security.md) says what leaves a person's machine and what an agent
can do.

## Why letting agents in is the safer choice

An AI agent no longer needs mm-mcp, or any token, to reach Mattermost. Ask a
coding agent today to do something in Mattermost, and without an integration it
drives a browser: through computer use or a browser extension, it opens
Mattermost where the person is already logged in, and reads and types as them.
Switching personal access tokens off, forbidding remote debugging, or requiring
a managed device stops none of that. As long as an agent can drive the
browser a person is logged in to, Mattermost is open to it.

What a policy does decide is the way the agent comes in:

| | Through the person's browser | Through mm-mcp |
|---|---|---|
| What it reads | Whatever is on the screen, Mattermost or not | Mattermost, through its API, nothing else |
| Posting | As the person, unmarked | Not offered until writes are allowed |
| Before a post others see | Nobody is asked | A person is asked by default |
| Telling its posts apart | Impossible | Marked as written with AI, as Mattermost shows it, by default |
| Telling its requests apart | Impossible | `User-Agent: mm-mcp/<version>` while it serves |
| Ending it | Ending the session the person works in | Ending the one session mm-mcp keeps |

So the safer choice is to make the agent visible and give it a proper way in,
rather than to try to keep it out. mm-mcp then logs in through the person's own
browser, in one click, and its other ways in, a browser window of its own, a
Chromium outside your browser policies, a pasted token, are not needed.

This will not satisfy every security policy, and it is not meant to. It is the
honest trade: a policy that closes the proper ways in does not keep agents
out. It moves them to the way nobody can see. Large organisations may take
years to come to that; the sooner they do, the less of their Mattermost is used
unseen.

## Letting mm-mcp in

Either change lets people log in through Mattermost's own OAuth service, which
works beside your single sign-on, not instead of it: the person signs in as
always, then approves mm-mcp on Mattermost's authorization page. Both need
System Console > Integrations > Integration Management > Enable OAuth 2.0
Service Provider.

To see what your server offers, open
`https://chat.example.com/.well-known/oauth-authorization-server`: an error page
means its OAuth service is off; a JSON answer means it is on, and with a
`registration_endpoint` in it, dynamic client registration is on too.

### One OAuth app for everyone

The change that keeps the most in your hands. Register one public client, an app
without a secret whose codes are protected by PKCE instead, with the callback
`http://127.0.0.1:8766/callback`. Mattermost's API makes a public client with
`is_public`, as mm-mcp's own tests do; with your own token in
`MM_ADMIN_TOKEN`:

```bash
curl -X POST https://chat.example.com/api/v4/oauth/apps \
  -H "Authorization: Bearer $MM_ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "mm-mcp", "description": "MCP server for AI agents", "homepage": "https://github.com/vriesdemichael/mm-mcp", "callback_urls": ["http://127.0.0.1:8766/callback"], "is_public": true}'
```

The answer's `client_secret` is empty, and its `id` is the client id to share.
Each person then runs `mm-mcp login --client-id <id> --url https://chat.example.com`
once; mm-mcp remembers the client for the server. Leave the app untrusted, its
default, so each person approves mm-mcp themselves on the authorization page.

### Or dynamic client registration

Integration Management > Enable Dynamic Client Registration. mm-mcp then
registers an app of its own the first time each person logs in, and nobody
needs a client id. Any program that reaches the server can then register an
OAuth app, as the standard intends, and there is one app per person who uses
mm-mcp; an app gets nothing until a person logged in to Mattermost approves it.

## Recognising mm-mcp

- **Its posts**, marked as written with AI by default, as Mattermost's web app
  shows beside the post's time. Each person can turn the mark off; the server
  cannot require it.
- **Its requests while it serves** carry `User-Agent: mm-mcp/<version>`, in your
  proxy's or load balancer's logs. A login through mm-mcp's own browser window
  makes its session in that browser, so the session looks like a browser's.
- **Its OAuth app**, when people log in through OAuth: the one you registered,
  or one per person with dynamic registration.

## Ending access

- **One person's**: revoke their sessions in System Console > User Management >
  Users, or revoke the personal access token they gave mm-mcp. mm-mcp then stops
  at start and says to log in again.
- **Logging in through OAuth, for everyone**: turning the OAuth 2.0 Service
  Provider off stops new OAuth logins; end the sessions already made by revoking
  them.
- **Personal access tokens**: Integration Management > Enable Personal Access
  Tokens, and each account's permission to make them, under User Management >
  Users > Manage roles.

## What it costs the server

mm-mcp makes its requests one after another, never in parallel, and keeps
nothing between tool calls. A tool call is a handful of API requests: reading a
channel about five, a search one plus one for each channel and team in the
answer, and a post about a dozen across asking and posting. Answered with 429,
it waits as `Retry-After` says, up to ten seconds, at most three times, then
reports the error. A request gives up after 30 seconds, a file transfer after 15
minutes.

## Rolling it out

mm-mcp has no central policy. Each person installs it, and its settings live in
their AI app's configuration ([Configuration](configuration.md)). What you can
decide centrally:

- **In Mattermost**: which accounts may make personal access tokens, which bots
  exist and which channels they are in, how long sessions last (Environment >
  Session Lengths), and which of the two ways in above you open.
- **In the AI app**, where it has managed settings: Claude Code's
  [managed settings](https://code.claude.com/docs/en/settings) can set the
  environment and the permission rules for everyone, for example keeping writes
  off, or requiring a person on every post.

## What is tested

Every tool runs against Mattermost Team Edition, on the current Extended Support
Release and the newest release, on every change
([Supported releases](mattermost-releases.md)). The login runs against Team
Edition's own login page in every browser it uses, on every system it ships for.
Single sign-on through a real identity provider, SAML or OpenID Connect, has
been tried by hand, not in the test suite; features only the licensed editions
have are not supported.
