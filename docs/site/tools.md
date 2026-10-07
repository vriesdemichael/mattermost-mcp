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
    attachments listed. `before` pages back from a post, `after` catches up from
    one; `more_before` says whether older messages may exist.
    `collapse_threads` leaves the replies out and shows each thread by the post
    that started it, with its reply count and last reply, as Mattermost shows a
    channel with collapsed reply threads.

`read_unread`: Read unread posts
:   Catches up on a channel from where you stopped reading: a few posts you have
    read, then the ones you have not, oldest first, with the first unread one
    named. It marks nothing read: what you have read is yours to record
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

`read_thread`: Read thread
:   A whole thread: the post that started it and every reply, oldest first.
    Give any post in it.

`list_threads`: List threads
:   The threads you follow, as Mattermost's threads view lists them, most
    recently replied to first: the post that started each, who took part, and
    how many replies and mentions you have not read. `team_id` keeps one team's,
    `unread_only` those with unread replies.

`list_pinned`: List pinned posts
:   The posts pinned to a channel.

`list_saved`: List saved posts
:   The posts you saved to come back to, each with its channel.

`search_posts`: Search posts
:   The messages the user can read that match a search, most recent first, each
    with its author and channel, across every team or within one. The terms use
    Mattermost's search syntax: words, `"a phrase"`, `-excluded`, `#hashtag`,
    `@username` for mentions, and `from:`, `in:`, `on:`, `before:` and
    `after:`. `match_any` finds posts with any of the words instead of all of
    them. Returns at most `limit` posts, 20 by default and at most 100, and says
    when more matched: Team Edition's search does not page, so narrow the terms
    to see the rest.

`read_file`: Read file
:   A file attached to a post, as content the model can read
    ([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)):
    text in windows of numbered lines, chosen with `start_line` and
    `line_count`; Word, PowerPoint and Excel files as their text; zip and tar
    archives as a listing; images as images, turned upright and scaled down
    when large; small audio and video as themselves. Anything else, a PDF
    included, is described by its type and size, with the post's link for you
    to open. Files over 64 MiB are described without being read.

`search_files`: Search files
:   Files attached to posts you can read, found by name and with Mattermost's
    search syntax, such as `ext:pdf`, `from:` and `in:`, each with the post and
    channel it is in.

`get_user`: Get user
:   One user, by `username` or by `user_id`: name, nickname, position and
    whether they are a bot.

`search_users`: Search users
:   Users whose username, name, nickname or email address contains a term,
    optionally only within a team or a channel. Deactivated users are left out.

`get_status`: Get status
:   Whether people are around: online, away, do not disturb or offline, when
    they were last active, and the status message they set.

## Writing

Tools that change Mattermost are offered only when `MM_MCP_ALLOW_WRITES` is
true. What a tool changes decides whether it asks you first
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

### What others see: asked every time

Each call asks you through your MCP client: it shows what will change, where,
and under whose name, and acts only when you tick the box and accept. A client
that cannot show the question gets an error, and nothing is written.

`post_message`: Post message
:   Posts a message in a channel, in a direct message with `to_user`, or with
    `root_id` as a reply in the thread of any post in it. The question shows
    the message as it will be sent, the channel or direct message it goes to
    and, for a reply, the start of the thread. Posting takes down the typing
    indicator `typing` put up there. Returns the post.

    `files` attaches up to ten files: text the model writes, given a `name` and
    `content`, or, when the server runs on your machine over stdio, a file by
    its full `path`. The question lists each file with its size, type and the
    path it was read from. The files are uploaded only once you accept, and a
    file changed after you were asked is refused.

`edit_post`: Edit post
:   Replaces the text of one of your own posts. The question shows the old text
    and the new. Another person's post is refused, even with an administrator's
    credential.

`delete_post`: Delete post
:   Deletes one of your own posts. Deleting the post that starts a thread
    deletes its replies, other people's included, and the question says how
    many.

`add_reaction`: Add reaction
:   Reacts to a post with an emoji, such as `thumbsup`. The question shows the
    emoji and the start of the post. Reacting again with the same emoji
    changes nothing.

`remove_reaction`: Remove reaction
:   Takes back one of your reactions.

`pin_post`: Pin post
:   Pins a post to its channel for everyone, or unpins it with `pinned` false.

### Yours alone, or gone in seconds: not asked

`typing`: Show typing
:   Shows you typing in a channel or thread while a message is written, kept up
    until `post_message` posts there, `stop` is sent, or a minute passes.

`follow_thread`: Follow thread
:   Follows a thread, so its replies notify you and it appears in
    `list_threads`, or stops following it with `following` false.

`save_post`: Save post
:   Saves a post among your saved posts, or removes it with `saved` false.

`draft_message`: Draft message
:   Puts a message in your message box in Mattermost as a draft, in a channel, a
    direct message or a thread, for you to change and send yourself. A
    different draft already there is left alone.

## On your machine

Offered only when the server runs on your own machine, started by your client
over stdio, whether writes are allowed or not: these tools write to your disk,
not to Mattermost
([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)).

`save_file`: Save file
:   Saves a file attached to a post into your download directory, set with
    `MM_MCP_DOWNLOAD_DIR` and your Downloads directory by default, under its own
    name, and answers with the path. An existing file is never overwritten: a
    number is added to the name instead. Files up to 100 MiB.
