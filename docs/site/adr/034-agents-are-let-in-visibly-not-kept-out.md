---
search:
  boost: 0.3
---

# ADR-034: Agents are let in visibly, not kept out

mm-mcp holds that the safe way for an agent to reach Mattermost is a proper way in that the server can see, and says so to administrators. It asks them for Mattermost's own OAuth, through one public app they register or dynamic client registration, and it keeps what makes it visible on by default: each post and edit marked as written with AI, each write others see asked first (ADR-021, ADR-033), and `User-Agent: mm-mcp/<version>` on every request it sends, its login's included. Where the proper way in is closed, `mm-mcp login` still logs the person in, through a browser window of its own, Playwright's Chromium or a pasted token (ADR-019), and the documentation says plainly what that means for the administrator's policy rather than presenting it as neutral.

The reason is that a policy cannot keep agents out any more. An agent with computer use or a browser extension opens Mattermost where the person is already logged in, and reads and types as them. Switching personal access tokens off, forbidding remote debugging, or requiring a managed device stops none of that. What a policy does decide is the way an agent comes in: through the person's browser it sees whatever is on the screen, posts unmarked, asks nobody, and cannot be told apart or ended without ending the person's own session; through mm-mcp it reaches Mattermost's API alone, is read-only until writes are allowed, asks before posts others see, marks them, names itself, and holds one session that can be ended on its own. Closing the proper ways in does not keep the agent out; it moves it to the way nobody can see.

This does not satisfy every security policy, and the documentation does not claim it does; the For administrators page makes the case. A change that makes mm-mcp harder for a server to recognise, such as a default that leaves posts unmarked or requests unnamed, goes against this record.

## Not chosen

- **Refusing to log in where personal access tokens are off**: It honours the policy on paper while the agent goes through the browser instead, unseen.
- **Offering the other ways in without saying what they mean for policy**: The administrator who has to decide would find out from the people who used them, not from mm-mcp.
- **Leaving visibility to settings that are off by default**: A mark or a name that most people never turn on does not let a server recognise the agent.
