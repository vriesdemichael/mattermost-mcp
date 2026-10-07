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

`search_posts`: Search posts
:   The messages the user can read that match a search, most recent first, each
    with its author and channel, across every team or within one. The terms use
    Mattermost's search syntax: words, `"a phrase"`, `-excluded`, `#hashtag`,
    `@username` for mentions, and `from:`, `in:`, `on:`, `before:` and
    `after:`. `match_any` finds posts with any of the words instead of all of
    them. Returns at most `limit` posts, 20 by default and at most 100, and says
    when more matched: Team Edition's search does not page, so narrow the terms
    to see the rest.

`get_user`: Get user
:   One user, by `username` or by `user_id`: name, nickname, position and
    whether they are a bot.

`search_users`: Search users
:   Users whose username, name, nickname or email address contains a term,
    optionally only within a team or a channel. Deactivated users are left out.

## Writing

Tools that write are offered only when `MM_MCP_ALLOW_WRITES` is true. Each call
asks you first, through your MCP client: it shows what will be written, where,
and under whose name, and acts only when you tick the box and accept. A client
that cannot show the question gets an error, and nothing is written
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

`post_message`: Post message
:   Posts a message in a channel, or with `root_id` replies in the thread of
    any post in it. The question shows the message as it will be sent, the
    channel or direct message it goes to and, for a reply, the start of the
    thread. Returns the post.

`add_reaction`: Add reaction
:   Reacts to a post with an emoji, such as `thumbsup`. The question shows the
    emoji and the start of the post. Reacting again with the same emoji
    changes nothing.
