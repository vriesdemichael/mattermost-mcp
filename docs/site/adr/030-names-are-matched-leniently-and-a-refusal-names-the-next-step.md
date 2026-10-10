---
search:
  boost: 0.3
---

# ADR-030: Names are matched leniently, and a refusal names the next step

A tool that finds something takes the name a person calls it by. `get_channel_info`, `get_team_info`, `get_users`, `dm`, `group_message` and the `in` filter of the searches take an id or a name, and a name matches without regard to case: a whole display name or address name first, then a part of one. One match is the answer. More than one is refused with every candidate, each with its id, so the model asks the person or chooses by id. None is refused with the closest names by edit distance, so the model corrects itself on the next call. The same holds for an @mention in a message, a username nobody has, and an emoji name Mattermost does not know. Every other tool's id arguments take names by the same rule: a `channel_id` or `team_id` a name, a channel's with ~ or # before it or not, and a `post_id` or `root_id` the address the post opens at. They are read into ids in one place, `toolSpec`, before the tool runs, and each argument's description says what it takes. A tool that acts on something is held to the same refusal of an ambiguous name, so what it acts on is never a guess, and one whose change others see names the channel it found in the question the person answers (ADR-021).

Every id argument also takes any link Mattermost shows a person, read in the same place: a channel's, `/<team>/channels/<name>`; a direct message's, `/<team>/messages/@<username>`; a group message's, `/<team>/messages/<name>`; a post's, by `/pl/`, `/threads/` or the channel's link with the post after it; a team's, `/<team>`, or any link into a team for a team argument; and a file's, or its resource address (ADR-029). A link's names are matched whole, never in part, and a channel's is looked up by its team's and its own name together, so an archived channel opens as it does in Mattermost. A link is read, never fetched, and only one to the configured server is taken: another server's could name a channel of the same name here. A link of another kind than the argument takes is refused with what it is.

Every post, channel and team an answer describes carries the link it opens at, made from names as Mattermost's web app makes it, so the model never builds one: a channel's in its team, a direct or group message's, which belongs to no team, in the first of the person's teams as `get_user_teams` lists them, and a post's in its channel's team, as Mattermost's Copy Link gives it. A post has no name, so its link is the one with an id in it; without a team it is the address Mattermost sends on to one. An answer whose posts share a channel names the channel's link once, beside its name. The server's instructions and each link's description say to cite a message by its post's link when saying where something was said, and never to build a link from an id.

Every refusal says what to do instead. A name that matches nothing names the closest ones and the tool that lists them; an archived channel is said to be archived, and that it can be read but not posted in; a draft already where a new one would go is quoted; a message too long for the server says its length and the server's limit. A failure Mattermost reports is passed on with its status and id, which is how the model tells a missing permission from a missing thing.

Lists are bounded: each list tool has a small default and a hard maximum, and says when it cut something off. A tool's description says what it is for and how it behaves, including what it refuses and whether it asks; each argument is described in the input schema, and the description repeats neither the arguments nor an example call, which every session would pay for.

A model asks for things by the names a person uses, and guesses ids badly. A person pastes what Mattermost shows them, which is a link; asked where something was said, a model that has only ids links the conversation, a direct message's whole history, rather than the post. An error that says only "not found" leaves it to guess again; one that names the closest match ends the guessing in one call. Matching a part of a name helps only until it is ambiguous, and an ambiguous name resolved silently posts to the wrong channel, so ambiguity is refused rather than settled by a rule the person never sees.

## Not chosen

- **Check a post's destination against display names the model passes, as Mattermost's own MCP server does**: the confirmation already shows the person where the post goes, by name, and the extra arguments cost every call.
- **Pick the most likely of several matches**: The likeliest channel is still a guess, and a post in the wrong channel cannot be taken back unseen.
- **Search for tools before loading them**: Mattermost's own agent finds tools with a search tool inside its plugin. Every client mm-mcp serves loads the catalogue itself; revisit when the catalogue outgrows that.
- **Leave bots out of lists unless asked**: Each user says whether it is a bot, which a model reads as easily, and a bot is sometimes what the person is looking for.
- **Links made from ids**: Mattermost opens `/<team>/channels/<id>` too, but a person reads nothing in it, and a direct message's id is the conversation, which is the link a model wrongly gave for a post.
- **Only `/_redirect/pl/<id>` for a post**: it works without a team, and reads as internal; it is kept for a post whose team is not known.
- **A direct message's link in the team an answer is about**: the same conversation would have a different link in each answer, and an answer about several teams has no one team.
- **A team's link on every post and channel**: the channel's link holds the team, and a link repeated on every post costs every answer; a team's link comes with the team itself.
- **A tool that turns a link into an id**: every id argument takes the link, so the model needs no extra call.
- **Take a link to another server by its names**: a channel or team of the same name here is not the one the person meant, and a post there is not one here.
- **Ids only for a tool that acts**: A model that passed a channel's name to `read_channel` or `create_post` got Mattermost's "invalid channel_id" and had to find the id first; an ambiguous name is refused all the same, and the question shows where a post goes, so taking a name guesses nothing.
