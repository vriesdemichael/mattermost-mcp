---
search:
  boost: 0.3
---

# ADR-030: Names are matched leniently, and a refusal names the next step

A tool that finds something takes the name a person calls it by. `get_channel_info`, `get_team_info`, `get_users`, `dm`, `group_message` and the `in` filter of the searches take an id or a name, and a name matches without regard to case: a whole display name or address name first, then a part of one. One match is the answer. More than one is refused with every candidate, each with its id, so the model asks the person or chooses by id. None is refused with the closest names by edit distance, so the model corrects itself on the next call. The same holds for an @mention in a message, a username nobody has, and an emoji name Mattermost does not know. Every other tool's id arguments take names by the same rule: a `channel_id` or `team_id` a name, a channel's with ~ or # before it or not, and a `post_id` or `root_id` the address the post opens at. They are read into ids in one place, `toolSpec`, before the tool runs, and each argument's description says what it takes. A tool that acts on something is held to the same refusal of an ambiguous name, so what it acts on is never a guess, and one whose change others see names the channel it found in the question the person answers (ADR-021).

Every refusal says what to do instead. A name that matches nothing names the closest ones and the tool that lists them; an archived channel is said to be archived, and that it can be read but not posted in; a draft already where a new one would go is quoted; a message too long for the server says its length and the server's limit. A failure Mattermost reports is passed on with its status and id, which is how the model tells a missing permission from a missing thing.

Lists are bounded: each list tool has a small default and a hard maximum, and says when it cut something off. A tool's description says what it is for and how it behaves, including what it refuses and whether it asks; each argument is described in the input schema, and the description repeats neither the arguments nor an example call, which every session would pay for.

A model asks for things by the names a person uses, and guesses ids badly. An error that says only "not found" leaves it to guess again; one that names the closest match ends the guessing in one call. Matching a part of a name helps only until it is ambiguous, and an ambiguous name resolved silently posts to the wrong channel, so ambiguity is refused rather than settled by a rule the person never sees.

## Not chosen

- **Check a post's destination against display names the model passes, as Mattermost's own MCP server does**: the confirmation already shows the person where the post goes, by name, and the extra arguments cost every call.
- **Pick the most likely of several matches**: The likeliest channel is still a guess, and a post in the wrong channel cannot be taken back unseen.
- **Search for tools before loading them**: Mattermost's own agent finds tools with a search tool inside its plugin. Every client mm-mcp serves loads the catalogue itself; revisit when the catalogue outgrows that.
- **Leave bots out of lists unless asked**: Each user says whether it is a bot, which a model reads as easily, and a bot is sometimes what the person is looking for.
- **Ids only for a tool that acts**: A model that passed a channel's name to `read_channel` or `create_post` got Mattermost's "invalid channel_id" and had to find the id first; an ambiguous name is refused all the same, and the question shows where a post goes, so taking a name guesses nothing.
