# Tools

Every tool declares a title and the four MCP annotations: whether it only reads,
whether it may destroy something, whether repeating it changes nothing more, and
that it works in a closed domain, your one Mattermost server
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

Each tool also accounts for every parameter of the Mattermost endpoints it
calls: which argument sets it, or why it is fixed or left out
([ADR-028](adr/028-every-parameter-of-an-operation-a-tool-calls-is-accounted-for.md)).
Messages are returned as their authors wrote them, oldest first, with times in
UTC.

## Reading

`get_me`: Who am I
:   The Mattermost user the server acts as: id, username, name, nickname,
    position, roles, and whether it is a bot. Call it to check the connection
    and whose access the other tools use.

`list_teams`: List teams
:   The teams the user belongs to. A channel belongs to a team, so this is where
    finding a conversation starts.

`list_channels`: List channels
:   The channels the user belongs to, direct and group messages included, with
    how many messages and mentions are unread in each, most recently active
    first. A direct message is named after the other person. `team_id` keeps
    one team's channels, with the direct and group messages, which belong to no
    team; `unread_only` keeps the channels with something unread.

`read_channel`: Read channel
:   A channel's messages, the newest 30 by default and at most 200, returned
    oldest first with each author's username, replies counted, reactions and
    attachments noted. `before` pages back from a post, `after` catches up from
    one; `more_before` says whether older messages may exist.

`read_thread`: Read thread
:   A whole thread: the post that started it and every reply, oldest first.
    Give any post in it.

`get_user`: Get user
:   One user, by `username` or by `user_id`: name, nickname, position and
    whether they are a bot.

`search_users`: Search users
:   Users whose username, name, nickname or email address contains a term,
    optionally only within a team or a channel. Deactivated users are left out.

## Writing

None yet. Tools that write are offered only when `MM_MCP_ALLOW_WRITES` is true,
and each one asks you before it acts.
