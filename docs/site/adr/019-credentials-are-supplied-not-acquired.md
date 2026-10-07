---
search:
  boost: 0.3
---

# ADR-019: The server is given a credential; acquiring one is a separate concern

mm-mcp acts as one Mattermost identity, with one of three credentials: a personal access token, a bot account's token, or the session token Mattermost issues when a person logs in, which its web app keeps in the MMAUTHTOKEN cookie. All three are sent the same way, as a bearer token, and the live suite proves each of them works. They differ in how they are obtained and how long they live: an access token lasts until it is revoked, a session expires on the server's schedule.

The server is given its credential and never obtains one. Today it reads `MM_URL` and `MM_TOKEN` from its environment, which an MCP client sets in the server's env block. No flag, argument or tool takes a credential, because a flag is visible in the process list and in the client's configuration of arguments, and a tool's arguments pass through the model. Obtaining a credential is a separate component: a login command that drives a Chrome or Edge already installed on the person's machine through the DevTools protocol, lets the person complete their organisation's single sign-on and second factor, captures the session token, and stores it where the server can read it. It uses the browser the person has, so mm-mcp downloads none. How it stores the token, and how the server finds a stored one, is decided when it is built, and recorded here then.

Session login exists because many organisations disable personal access tokens and do not hand out bots, and a person on such a server has no other way to give an agent their access. A session token carries everything the person can do, so the safety rules for writes (ADR-021) apply to it as to any credential.

When adding a way to obtain or store a credential, keep it out of the serving path: the server must start and work from the environment alone. Never log a credential, never put one in an error message, and never return one from a tool. Keep a credential out of what a type prints, as `config.Config`'s String and GoString do.

## Not chosen

- **A login flow inside the MCP server**: A stdio server has no terminal and no browser of its own, and a server that can obtain credentials is a larger thing to trust than one that is handed one.
- **Mattermost OAuth as the only way in**: It needs an OAuth application registered by an administrator, which is exactly what the people who need session login cannot get.
- **Username and password in the configuration**: It fails under single sign-on and multi-factor authentication, and stores the one secret that unlocks everything.
