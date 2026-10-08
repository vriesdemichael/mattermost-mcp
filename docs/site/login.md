# Logging in

Where your Mattermost lets you make a personal access token, that token is the
simplest way in: `mm-mcp login --with paste` keeps it in your system's
credential store ([Installation](installation.md#get-a-token)). Many
organisations switch those tokens off, often to keep programs and agents out;
[that no longer works](administrators.md#why-letting-agents-in-is-the-safer-choice),
and an administrator can open a proper way in instead. Until then, log in once
with your own account.

## Step by step

1. **Install mm-mcp** with a package manager ([Installation](installation.md)).
2. **In a terminal, run**

    ```bash
    mm-mcp login --url https://chat.example.com
    ```

    with the address you open Mattermost at. One of two things happens:

    - **Your browser asks you to approve mm-mcp.** Approve it, and close the tab.
    - **A browser window opens at your sign-in page.** Log in as you always do,
      second factor included. The window closes by itself.

    Where your server keeps passwords, mm-mcp may ask for yours in the terminal
    instead.
3. **It says `Logged in to https://chat.example.com as @you`.** mm-mcp keeps
   the session in your system's credential store: the Windows Credential
   Manager, your macOS keychain, or your Linux desktop's Secret Service.
4. **In your AI app, set only the address**, `MM_URL`, and leave the token
   empty. Restart the app, so it starts mm-mcp again.
5. **Ask it who you are in Mattermost.** It answers with your username.

When step 2 fails, mm-mcp ends with what it tried and why each failed, and what
to do: most often, [install Playwright's Chromium](#when-the-window-will-not-open),
or [ask your administrator](#what-to-ask-your-administrator) for a change that
lets everyone in through their own browser.
`mm-mcp doctor --url https://chat.example.com` checks the login at any time
([below](#when-the-tools-still-do-not-work)). When Mattermost ends the session,
run `mm-mcp login` again; `mm-mcp logout` ends it yourself.

## How mm-mcp logs you in

Before it opens anything, mm-mcp reads how your server signs people in, which
any server says to anyone: its login page's settings, and whether it is an OAuth
authorization server. From that it makes a plan, a list of ways to log in, and
tries them one after the other until one gives it a session Mattermost accepts.

```mermaid
flowchart TD
    start(["mm-mcp login"]) --> discover["Read how the server<br>signs people in"]
    discover -->|"could not read it"| guess["Warn, and assume<br>single sign-on"]
    discover --> plan["Plan the ways to try,<br>or take the one<br>--with names"]
    guess --> plan
    plan --> next{"A way left<br>to try?"}
    next --> try["Try the next way"]
    try -->|a token| check{"Does Mattermost<br>say whose it is?"}
    check -->|yes| store["Keep it in the<br>credential store"]
    store --> done(["Logged in as @you"])
    store -->|"no credential<br>store here"| env(["Put the token<br>in MM_TOKEN instead"])
    try -. "failed, and says why" .-> next
    check -. "no" .-> next
    try -->|"ten minutes passed,<br>or Ctrl+C"| advice
    next -->|"none left"| advice(["Sum up what failed,<br>and what works instead"])
```

Every way that fails says why before the next is tried. When none is left,
mm-mcp sums up each way it tried and why it failed, and what would work for your
server: Playwright's Chromium, a token, or the change to ask your administrator
for. The whole login waits at most ten minutes.

```text
mm-mcp found no way to log in to https://chat.example.com that worked:
  - browser window: no browser would let mm-mcp watch the login: Chrome opened no DevTools port within 20s; …
  - pasted token: no token was given

What works from here:
  - A browser window: install Playwright's Chromium, which no company policy for Chrome or Edge reaches, with
      npx playwright install chromium
    and run mm-mcp login again.
  …
```

### The order it tries them in

| What your server offers | What mm-mcp tries, in order |
|---|---|
| Single sign-on, perhaps beside passwords | a browser window, then your password if the server keeps passwords or uses LDAP, then a pasted token |
| Passwords or LDAP, without single sign-on | your password, then a browser window, then a pasted token |
| Neither that mm-mcp can see, or it could not read the server | a browser window, then a pasted token |

When the server offers OAuth, it is the only way tried: its OAuth service is
on, and mm-mcp has a client to log in as, because the server lets it register
one itself, you gave one with `--client-id`, or an earlier login remembered
one. An administrator who opened that way in chose it, so mm-mcp does not fall
back to the others on its own. `--with oauth`, `--with window`,
`--with password` or `--with paste` tries only the way it names.

### The four ways

1. **OAuth, in your own browser.** mm-mcp opens Mattermost's authorization
   page in your default browser, whichever it is, Safari included, with your
   everyday profile. You are usually logged in there already, so you only
   approve mm-mcp. Mattermost sends your browser back to a page mm-mcp serves on
   `127.0.0.1` with a one-time code, which mm-mcp exchanges for a session.
   Nothing reads your browser's cookies ([below](#oauth-step-by-step)). This is
   Mattermost's own OAuth service, Mattermost granting mm-mcp access, not the
   sign-on your organisation logs in with, such as Okta or Entra ID; it works
   beside it.
2. **A browser window of mm-mcp's own**, for single sign-on: SAML, Entra ID,
   OpenID Connect, GitLab, Google. mm-mcp starts a Chrome, Edge, Chromium or
   Firefox installed on your machine, or the Chromium Playwright downloads, with
   a profile of its own that it deletes afterwards, at your server's login page.
   You log in as you always do, second factor included; mm-mcp watches the
   window's cookies until Mattermost sets its session cookie, and closes it.
   Because the profile is new, you log in fresh there, even when your everyday
   browser is logged in ([below](#the-browser-window)).
3. **Your password, in the terminal**, where your server keeps passwords or
   signs people in with LDAP: an email address or username, the password, and
   your authenticator's code when your account has a second factor. An account
   made through single sign-on has no password in Mattermost, and is told so.
4. **A token you paste**, as the last resort: a personal access token, a bot's
   token, or the value of the `MMAUTHTOKEN` cookie your browser holds for the
   server, from its developer tools while you are logged in (Application or
   Storage, then Cookies). The terminal does not show what you type, and a
   token piped into the command is read as it is.

Whatever way you log in, mm-mcp asks Mattermost whose the session is before it
keeps it, and says so: `Logged in to https://chat.example.com as @you`.

## OAuth, step by step

mm-mcp logs in as a program on your machine does under OAuth (RFC 8252), with
PKCE in place of a client secret, so a code another program catches on the way
is of no use to it:

```mermaid
sequenceDiagram
    autonumber
    participant M as mm-mcp
    participant B as Your browser
    participant S as Mattermost
    opt No client to log in as yet
        M->>S: Register mm-mcp as a public client,<br>with a callback on 127.0.0.1
        S-->>M: Its client id, remembered for the next login
    end
    Note over M: Listen on 127.0.0.1,<br>make a secret verifier
    M->>B: Open the authorization page
    B->>S: You approve mm-mcp, logged in as you already are
    S-->>B: Back to 127.0.0.1/callback, with a one-time code
    B->>M: The code
    M->>S: The code and the verifier
    S-->>M: A session token
```

The page on `127.0.0.1` says whether mm-mcp is logged in; you can close the tab
then. A callback that does not answer the login mm-mcp started is turned away.

## The browser window

mm-mcp drives none of the login page itself: it only starts the browser, opens
the page, and reads the browser's cookies, a Chromium's through its DevTools
protocol and a Firefox's through WebDriver BiDi.

```mermaid
flowchart TD
    find["Find the browsers"] --> next{"A browser<br>left?"}
    next --> confined{"A snap or<br>a Flatpak?"}
    confined -->|no| launch["Start it with a new profile,<br>at the server's login page"]
    launch --> answer{"Can mm-mcp reach it<br>within twenty seconds?"}
    answer -->|yes| wait["You log in; mm-mcp reads<br>the cookies twice a second"]
    answer -->|"a Chromium refused<br>its sandbox"| bare["Start it again without<br>its sandbox, and say so"] --> wait
    wait -->|"Mattermost set<br>MMAUTHTOKEN"| got(["Close the browser,<br>delete the profile"])
    wait -->|"you closed<br>the window"| closed(["The next way<br>is tried"])
    confined -. "yes: skip it" .-> next
    answer -. "no: a policy forbids it,<br>or it exited" .-> next
    next -->|"none left"| none(["No browser would let<br>mm-mcp watch the login:<br>the next way is tried"])
```

The browsers are the one `--browser` names, alone, or else every Chrome, Edge,
Firefox and Chromium installed, in that order, and then Playwright's Chromium,
newest first. On Windows and macOS, mm-mcp looks where each browser's installer
puts it; on Linux, for `google-chrome`, `microsoft-edge`, `firefox` and
`chromium` on your `PATH`. Playwright's Chromium is in your user folder, or
under `PLAYWRIGHT_BROWSERS_PATH` when you set it.

### When the window will not open

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
policies do not reach. Install it once, with [Node.js](https://nodejs.org/):

```bash
npx playwright install chromium
```

and run `mm-mcp login` again: mm-mcp finds it on its own. `--browser` names any
other Chrome, Edge, Chromium or Firefox, by path or command.

Your organisation's single sign-on may also require a managed device or browser,
as Entra ID's Conditional Access and Okta's device trust can. A fresh profile is
then refused at the sign-on page itself, Playwright's included; OAuth in your own
browser, or a pasted token, still works.

## When the login fails

mm-mcp names what failed, before it tries the next way:

| It says | Which means | What to do |
|---|---|---|
| `warning: reading how … lets people sign in` | The server's login settings did not answer; mm-mcp assumes single sign-on | Check the address is the one you open Mattermost at |
| `… opened no DevTools port within 20s`, or `started no remote agent` | A company policy forbids watching that browser | Install Playwright's Chromium ([above](#when-the-window-will-not-open)) |
| `… is installed as a snap or a Flatpak` | Its sandbox keeps out mm-mcp's profile | Install Playwright's Chromium, or name another browser with `--browser` |
| `no browser would let mm-mcp watch the login` | Every browser found refused | The same |
| `the browser was closed before the login finished` | The window closed before Mattermost set its session cookie | Run `mm-mcp login` again, and log in before you close it |
| `the server lets no OAuth client register itself, and no client id was given` | OAuth is on, but mm-mcp has no client to log in as | Ask your administrator for [one of two changes](#what-to-ask-your-administrator) |
| `listening for the OAuth callback on port …` | Another program holds the callback's port | Close it, or use `--callback-port` when the app's callback names another |
| `mm-mcp was not authorized` | You, or the server, refused mm-mcp on the authorization page | Approve it, or try `--with window` |
| `the account signs in through single sign-on and has no password` | Your account has no password in Mattermost | `mm-mcp login --with window` |
| `nothing could be read here` | The password or token prompt runs where nobody types, as in an AI agent's shell | Run `mm-mcp login` in a terminal of your own |
| `the token does not work` | Mattermost refused what the way obtained | The next way is tried; a pasted token may be mistyped or revoked |
| `… Set the token as MM_TOKEN in the MCP client's env block instead` | Your system has no credential store mm-mcp can use, as on a Linux without a desktop | Put the token in `MM_TOKEN`, from `--with paste` or a personal access token |

## Where the session is kept, and when it ends

mm-mcp keeps one session per server, under the name `mm-mcp`, in the Windows
Credential Manager, your login keychain on macOS, or your desktop's Secret
Service on Linux, such as GNOME Keyring or KWallet. It also keeps, under
`mm-mcp-oauth-client`, the OAuth client it logged in as, which is no secret, so
the next login uses it again.

`mm-mcp serve` uses the stored session whenever `MM_URL` names that server and
`MM_TOKEN` is not set; a token in `MM_TOKEN` always goes first. A session ends
when Mattermost expires it, or when it is revoked, by you under Profile >
Security or by an administrator. How long a session lasts is your server's to
say: an administrator sets it under System Console > Environment > Session
Lengths, often to days or weeks. A personal access token or a bot's token you
pasted lasts until it is revoked. Once it ends, `mm-mcp serve` stops at start with
`refused the session mm-mcp login stored`; run `mm-mcp login` again, and restart
the MCP server. `mm-mcp logout` ends the session at Mattermost and forgets it.

## When the tools still do not work

```bash
mm-mcp doctor --url https://chat.example.com
```

checks the login you stored and everything around it, and says what to fix:
whether a login is stored under that address in your credential store, whether
the store can be read at all, whether an `MM_TOKEN` hides the login, whether
the server can be reached and still accepts the session, and how
`mm-mcp login` would log you in. The most common causes:

- **`MM_TOKEN` is still set** in your MCP client's configuration. It wins over
  a stored login; remove it.
- **Another spelling of the address.** A login is stored under the address you
  logged in with: `http` is not `https`, and another host name for the same
  server is another entry. Log in with the address `MM_URL` names.
- **No credential store.** On Linux, the Secret Service runs with your desktop
  session; over SSH, in WSL or in a container there is none. Use a token in
  `MM_TOKEN` there.

The command reads your terminal's environment, not your MCP client's. From
inside the client, ask the model to call the `diagnose` tool: it makes the same
checks with the client's own configuration, and says whether the client can
show the question mm-mcp asks before each write.

## What to ask your administrator

One change on the server lets everyone log in through their own browser, in
one click: turning on dynamic client registration, or registering one OAuth app
for mm-mcp. Send your administrator [For administrators](administrators.md),
which has the steps, and why letting agents in this way is the safer choice.
With a registered app, run `mm-mcp login --client-id <id>` once, with the id
your administrator gives you; mm-mcp remembers it for the server.
`--callback-port` uses another port, when the app's callback names one.

To see what your server offers, open
`https://chat.example.com/.well-known/oauth-authorization-server`: an error page
means its OAuth service is off; a JSON answer means it is on, and with a
`registration_endpoint` in it, mm-mcp can register itself.

## With an AI agent

An agent can run `mm-mcp login` for you: OAuth and the browser window need
nothing typed, so you log in in the window or approve in your browser. Your
password and a pasted token are typed into a terminal, which an agent's shell is
not; there, mm-mcp says to run it in a terminal you can type in. Never paste a
token into the conversation: run `mm-mcp login --with paste` yourself and paste
it at the command's own prompt. When nothing works, mm-mcp ends with what it
tried and what to do next, which an agent can read to you.

For the agent:

- **Give the login time.** It waits up to ten minutes for the person, longer
  than a shell's usual timeout. Run it with a timeout of ten minutes or in the
  background, and tell the person first what will open.
- **Check that it took** with `mm-mcp doctor --url https://chat.example.com --json`,
  which exits with status 1 when a check failed.
- **Restart the MCP client**, or reconnect its server, after adding mm-mcp or
  changing its settings: a client reads both when it starts. On Windows, a shell
  started before mm-mcp was installed does not find it either.
