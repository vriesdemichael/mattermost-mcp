---
search:
  boost: 0.3
---

# ADR-033: Who asks before a write is a setting, and mm-mcp asks unless told otherwise

Two settings decide who asks the person before a write others see:

- `MM_MCP_ASK_BEFORE_WRITES`, `true` by default: mm-mcp asks through MCP elicitation, every call, as ADR-021 describes. `false` leaves the asking to the MCP client: mm-mcp asks nothing, and the client's own approval of the tool call is the only check. The client's permission rules decide. A rule that asks keeps a person in the loop, and a rule that allows lets an agent write with nobody watching.
- `MM_MCP_FORCE_HUMAN_IN_THE_LOOP`, `false` by default, and only with `MM_MCP_ASK_BEFORE_WRITES=false`: each tool that would ask carries `_meta["anthropic/requiresUserInteraction"]: true`. Claude Code then shows its own prompt on every call, in every permission mode, and no allow rule or "don't ask again" skips it. It is for someone who wants a person on every write when nothing else enforces it, such as with a model they trust less. A client that does not read the mark asks as its own rules say.

Both set to `true` stops the server at start with what to change. mm-mcp would ask, and the client would prompt as well, so the person would be asked twice for one write.

The settings change only who asks. A tool that does not ask under ADR-021 does not ask under any setting, and which tools are offered still depends on `MM_MCP_ALLOW_WRITES` alone. While mm-mcp does not ask, the server's instructions and the description of each tool that would ask say so, so the model does not tell the person they will be asked. The server also says so in its log at start.

A refusal to an elicitation tells the model that either the person declined or their client answered without showing the question. It names `save_draft` and `MM_MCP_ASK_BEFORE_WRITES` as the ways forward. mm-mcp cannot tell these apart: a client that declares form elicitation and declines every question unseen looks exactly like a person saying no.

Claude Code is the client most people use mm-mcp from, and in the Claude desktop app's Code tab it declares form elicitation and then declines every question without showing it ([mm-mcp#20](https://github.com/vriesdemichael/mm-mcp/issues/20); upstream [anthropics/claude-code#89858](https://github.com/anthropics/claude-code/issues/89858) and [#96043](https://github.com/anthropics/claude-code/issues/96043)). With ADR-021 as it stood, no write could be made from there, and there is no date for a fix. That client does gate each tool call with its own prompt, and its permission rules can be set per tool, so a person can still be asked. It is the client asking rather than mm-mcp.

ADR-021 turned down trusting the client's own approval, because some clients approve automatically and show arguments rather than the message as it will read. Both remain true, and the default keeps ADR-021's behaviour. What changed is that the main client's own prompt is the only one that works there. Someone who sets `MM_MCP_ASK_BEFORE_WRITES=false` chooses that prompt, and the configuration page says plainly that a client set to approve automatically then posts under their name with nobody seeing it first.

Not every write has a person in the loop. An agent that works alone, such as a scheduled report, needs to be allowed by the client's rules. That is why not asking sets no mark, and forcing a human in the loop is a setting of its own: a mark that overrides allow rules makes unattended writing impossible.

The names say what the person gets. "Ask before writes" names who asks and when, and "force human in the loop" names the outcome, a person on every call, and that it overrides what the client would otherwise allow. Each is a yes or no. The one combination that contradicts itself is refused rather than resolved silently.

The inputs stay as they are. A channel or post can be named by id, and a client's prompt then shows an id rather than a name. Requiring a readable name in the input would make the prompt easier to read, but a name is matched leniently (ADR-030) and could reach somewhere the model only imagined, while an id cannot. Since mm-mcp cannot tell whether a person sees the client's prompt, it does not trade the id's exactness for readability.

## Not chosen

- **One setting with three values**: Values naming who asks, such as `mm-mcp` or `client`, read as something to install or connect rather than as a choice about asking.
- **The mark on every write by default**: It overrides allow rules, so no agent could write unattended.
- **The mark while mm-mcp asks too**: The person would be asked twice for one post, wearing out the attention the question depends on.
- **A readable name in every write's input**: See above; an id cannot reach the wrong place.
- **Detect the client and stop asking it**: A client's name says nothing reliable about whether it shows questions, and the same client behaves differently from one mode to another.
