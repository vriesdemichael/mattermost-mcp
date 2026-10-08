# Tools

Every tool declares a title and the four MCP annotations: whether it only reads,
whether it may destroy something, whether repeating it changes nothing more, and
that it works in a closed domain, your one Mattermost server
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

Each tool also accounts for every parameter of the Mattermost endpoints it
calls: which argument sets it, or why it is fixed or left out
([ADR-028](adr/028-every-parameter-of-an-operation-a-tool-calls-is-accounted-for.md)).

Every list pages the same way: give `limit`, and pass the `next_cursor` an
answer ends with back as `cursor`, with the same other arguments, for the next
page, until an answer has none
([ADR-032](adr/032-every-list-pages-by-an-opaque-cursor.md)).

Every post comes back in one shape: its address, which opens it in
Mattermost, its author's username and name, its channel and team by name, its
files with their ids, reactions with who reacted, whether it is pinned or
marked as written with AI, its thread, and when it was edited. An answer whose
posts are all in one channel names the channel and team once, rather than on
every post. A list cuts a message longer than 4,000 characters short and says
its whole length; `read_post` reads it whole. Times are in UTC. A tool that takes a name, of a channel, a team or a person,
matches it in any case, whole or in part; an ambiguous name is refused with every
candidate, and an unknown one with the closest names
([ADR-030](adr/030-names-are-matched-leniently-and-a-refusal-names-the-next-step.md)).
Every `channel_id` and `team_id` takes a name as well as an id, a channel's
with `~` or `#` before it or not, and every `post_id` and `root_id` takes
the address the post opens at.

## People

`get_me`: Who am I
:   The Mattermost user the server acts as: id, username, name, nickname,
    position, roles, the timezone set in Mattermost, and whether it is a bot.
    Call it to check the connection
    and whose access the other tools use.

`get_users`: Get users
:   People by username, user id or email address, any mix of them in one call,
    with whether each is a bot or deactivated. An email address finds someone
    only when the server shows you addresses. A name nobody has is listed
    with the closest usernames beside the people found, and refused only when
    nobody is found.

`search_users`: Search users
:   Users whose username, name, nickname or email address contains a term,
    optionally only within a team or a channel. Deactivated users are left out.
    Mattermost finds at most 1000 for one term.

`get_status`: Get status
:   Whether people are around: online, away, do not disturb or offline, when
    they were last active, and the status message they set.

## Teams and channels

`get_user_teams`: List the user's teams
:   The teams you belong to.

`get_team_info`: Get team
:   A team by id or name. An open team you are not in is found by the exact name
    in its address.

`get_user_channels`: List the user's channels
:   The channels you belong to, direct and group messages included, most
    recently active first, each with its team and how many messages and
    mentions are unread. `team_id` keeps one team's, `unread_only` those with
    something unread.

`get_channel_info`: Get channel
:   A channel by id or name, among your own channels and the public channels of
    your teams, saying whether you belong to it and whether it is archived.

`search_channels`: Search channels
:   Channels by part of their name: your own, and public ones you have not
    joined. Mattermost finds at most 50 public channels in a team for one term.

`list_team_channels`: List a team's channels
:   A team's public channels, by name.

`list_archived_channels`: List archived channels
:   A team's archived channels. An archived channel can be read but not posted
    in.

`get_channel_stats`: Get channel stats
:   How many people belong to a channel, how many are guests, and how many posts
    are pinned and files shared in it.

## Reading

`read_channel`: Read channel
:   A channel's messages, 30 a page by default and at most 200, oldest first
    within a page: the newest, and the pages after go further back. `before`
    starts back from a post; `after` and `since`, from a post or a time, read
    forward. `since` takes a time with an offset, or a time or a day in your
    own timezone. `collapse_threads` leaves the replies out and shows each thread by
    the post that started it, as Mattermost shows a channel with collapsed reply
    threads.

`read_unread`: Read unread posts
:   Catches up on a channel from where you stopped reading: a few posts you have
    read, then the ones you have not, oldest first, with the first unread one
    named. A channel you never opened is all unread, and reads newest first,
    the pages after going further back. It marks nothing read: what you have
    read is yours to record
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

Posts written in the same millisecond stay on one page, so a page of a channel
can hold a few more posts than its limit: Mattermost reads on from a post by
its time alone, and a page split inside a millisecond would lose posts.

`read_post`: Read post
:   A post and the thread it is in, oldest first, 100 posts a page by default.
    Give any post in the thread; `include_thread` false reads the post alone.

`list_threads`: List threads
:   The threads you follow, as Mattermost's threads view lists them, most
    recently replied to first: the post that started each, who took part, and
    how many replies and mentions you have not read.

`list_pinned_posts`: List pinned posts
:   The posts pinned to a channel.

`list_saved`: List saved posts
:   The posts you saved to come back to.

`search_posts`: Search posts
:   Messages you can read, across every team or one, by words, and by who wrote
    them, where and when through `from`, `in`, `before`, `after` and `on`, so no
    search syntax is needed. `@username` finds where someone was mentioned.
    20 posts a page by default, at most 100. Mattermost's search finds the 100
    most recent matches at most, and the answer says `capped` when it reached
    them: `before` reaches older ones. `in` takes a channel you belong to, the
    only ones Mattermost searches, and `from` a username, refused with the
    closest when nobody has it. The days `before`, `after` and `on` name are
    your own, in the timezone you set in Mattermost, as in its search box.

`list_mentions`: List mentions
:   The posts that mention you, most recent first, as Mattermost's Recent
    Mentions finds them: by `@username`, and by your first name, the other
    words and `@channel`, `@all` and `@here` when your notification settings
    say those mention you. Only a post that mentions you as Mattermost would
    notify you is kept, and none of your own. The answer names the words it
    looked for. `from`, `in`, `before`, `after` and `on` narrow it, and like
    `search_posts` it reaches the 100 most recent matches at most. The
    database search Team Edition uses skips "all" and "here" as too common to
    index, so `@all` and `@here` are found only on a server that searches with
    Elasticsearch, as they are in Mattermost's own Recent Mentions.

## Files

`read_file`: Read file
:   A file attached to a post, as content the model can read
    ([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)):
    text in windows of numbered lines, chosen with `start_line` and
    `line_count`; Word, PowerPoint and Excel files as their text; zip and tar
    archives as a listing; images as images, turned upright and scaled down
    when large; small audio, video and PDF files as themselves, for a client
    that can read them. Anything else is described by its type and size, with
    the post's link. Files over 64 MiB are described without being read.

`search_files`: Search files
:   Files attached to posts you can read, by name, by type with `ext:pdf`, and by
    `from`, `in`, `before`, `after` and `on`, each with the post and channel it
    is in. Like `search_posts`, it finds the 100 most recent matches at most.

## Writing

Tools that change Mattermost are offered only when `MM_MCP_ALLOW_WRITES` is
true. What a tool changes decides whether it asks you first
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

### What others see: asked every time

Each call asks you through your MCP client: it shows what will change, where,
and under whose name, and acts only when you tick the box and accept. The
question is an MCP elicitation, a form the client shows, so these tools need a
client that shows one, such as Claude Code. A client that declares it cannot
gets an error that says so, and nothing is written; `save_draft`, which does
not ask, still puts a message in your message box for you to send. Some
clients declare that they can and never show the form, and the call waits
until the client gives up on it; Claude Desktop has been reported to.

Every message is checked before you are asked
([ADR-031](adr/031-a-message-is-checked-before-anyone-is-asked-to-post-it.md)):
one longer than the server takes is refused with its length and the limit, an
@mention of someone nobody is is refused with the closest usernames, and the
question says how many people `@here`, `@channel` and `@all` reach, who of
those mentioned is deactivated, and, on a licensed server with user groups,
how many a group's mention notifies. Mentions are found as Mattermost finds them, so
code mentions nobody and `@here.` ending a sentence is `@here`. What a post
does, and every file it carries, comes before the message in the question.

What a write acts on is what you were asked about: a reply posted, a file
rewritten or a draft typed on while you read the question stops the write. Posts and edits are marked as written with AI,
as Mattermost shows it, unless `MM_MCP_MARK_AI_GENERATED` is false.

`create_post`: Create post
:   Posts a message in a channel, or with `root_id` replies in the thread of any
    post in it. The question shows the message as it will be sent, the channel
    and, for a reply, the start of the thread. Posting takes down the typing
    indicator `typing` put up there. Returns the post.

    `files` attaches up to ten files: text the model writes, given a `name` and
    `content`, or, when the server runs on your machine over stdio, a file by
    its full `path`. The question lists each file with its size, type and the
    path it was read from, and the box you tick names every file read from
    your disk by its path. The files are uploaded only once you accept, and a
    file changed after you were asked is refused. A file where credentials are
    kept is never attached: anything under `.ssh`, `.gnupg`, `.aws`, `.kube`
    and the like, an MCP client's configuration, a `.env` file, a key store,
    and any file holding a private key or the token mm-mcp runs with. Attach
    such a file in Mattermost yourself if you mean to. `dm` and
    `group_message` take `files` too.

`dm`: Send direct message
:   Sends a direct message to one person, by username or email address, or to
    yourself when none is given. This is how to message people: "message Alice and Bob" is two direct
    messages.

`group_message`: Send group message
:   Sends one message to two to seven people together, in the group conversation
    Mattermost keeps for exactly them. Only when you ask for a group
    conversation.

`update_post`: Update post
:   Replaces the text of one of your own posts. The question shows the old text
    and the new. An edit notifies nobody, so only its length is checked.
    Another person's post is refused, even with an administrator's credential.

`delete_post`: Delete post
:   Deletes one of your own posts. Deleting the post that starts a thread
    deletes its replies, other people's included, and the question says how
    many.

`add_reaction`: Add reaction
:   Reacts to a post with an emoji, by name, such as `thumbsup`, or as itself,
    such as 👍. An unknown name is refused with the closest ones. A reaction
    joins one others gave under another name of the same emoji, so
    `thumbsup` beside a `+1` is the one 👍. Reacting again with the same
    emoji changes nothing.

`remove_reaction`: Remove reaction
:   Takes back one of your reactions.

`pin_post`: Pin post
:   Pins a post to its channel for everyone, or unpins it with `pinned` false.

`delete_draft`: Delete draft
:   Deletes your draft in a channel or thread. Nobody else sees a draft, but it
    may hold words you have not sent, so the question shows it and where it is
    first.

`join_channel`: Join channel
:   Joins a public channel of one of your teams. Everyone in it sees that you
    joined. A private channel is joined only by being added.

`leave_channel`: Leave channel
:   Leaves a channel you belong to. Everyone in it sees that you left, and the
    question says when the channel is private, where only being added brings
    you back.

`add_channel_members`: Add channel members
:   Adds up to twenty people, by username, user id or email address, to a
    channel you belong to. Each is notified, and the question names each.

`create_channel`: Create channel
:   Creates a public or private channel in one of your teams, with you in it,
    its address made from its name as Mattermost's own app makes it, and its
    purpose and header when given.

`set_status`: Set status
:   Sets your status as everyone sees it beside your name: online, away, do not
    disturb until a time, or offline, and a status message with an emoji, until
    a time or until changed; or clears the message. A time without an offset
    is read in your timezone.

### Yours alone, or gone in seconds: not asked

`typing`: Show typing
:   Shows you typing in a channel or thread while a message is written, kept up
    until `create_post` posts there, `stop` is sent, or a minute passes. It
    is optional, for a long message that takes a while to write.

`follow_thread`: Follow thread
:   Follows a thread, so its replies notify you and it appears in
    `list_threads`, or stops following it with `following` false.

`save_post`: Save post
:   Saves a post among your saved posts, or removes it with `saved` false.

`set_post_reminder`: Set post reminder
:   Has Mattermost remind you of a post at a time, as its "Remind me" does. A
    time without an offset is read in the timezone you set in Mattermost.

`save_draft`: Save draft
:   Puts a message in your message box in Mattermost as a draft, in a channel or
    a thread, for you to change and send yourself. The message is checked as a
    post's is, and its notes say what its mentions will do. A different draft
    already there is never replaced, and a draft is refused when your drafts do
    not sync, since you would never see it.

`list_drafts`: List drafts
:   Your drafts, in channels and threads, with where each is.

`mark_channel_read`: Mark channel read
:   Marks a channel read, as opening it in Mattermost does, clearing its unread
    count and mentions, only when you ask for it. Reading a channel for you
    never marks it read
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

## On your machine

Offered only when the server runs on your own machine, started by your client
over stdio, whether writes are allowed or not: these tools write to your disk,
not to Mattermost
([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)).

`save_file`: Save file
:   Saves a file attached to a post into your download directory, set with
    `MM_MCP_DOWNLOAD_DIR` and your Downloads directory by default, under its own
    name, and answers with the path. An existing file is never overwritten: a
    number is added to the name instead, and saving the same file again
    answers with the copy already saved. The file is marked as downloaded from
    the internet, as a browser marks it: on Windows, SmartScreen checks a
    program and Office opens a document in Protected View; on macOS,
    Gatekeeper checks it. Files up to 100 MiB.

## When something does not work

`diagnose`: Diagnose mm-mcp
:   The checks `mm-mcp doctor` makes, from inside the running server, so with
    the configuration your MCP client actually started it with: the address,
    which credential it acts with, a login `mm-mcp login` stored and whether
    `MM_TOKEN` hides it, the proxy, whether the server answers as a supported
    Mattermost, whose credential it is and whether that user belongs to a team.
    It adds what only the client shows: its name and protocol version, and
    whether it can show the question mm-mcp asks before each write. With
    `ask_test_question`, it shows you a test question and reports how it was
    answered: an answer within a few seconds came from the client itself,
    which then answers every question before a write without showing it. Each
    check says what it found, and a failed one what to do. It never returns
    the credential ([Configuration](configuration.md#command-line)).

## Not offered

- **Scheduled posts.** Mattermost's scheduled posts need a licence, and Team
  Edition, which mm-mcp is tested against, refuses them
  ([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).
- **Marking what the model read as read, or anything unread.** What you have
  read is yours to record, and a model reading a channel is not you reading
  it; `mark_channel_read` marks a channel read when you ask
  ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).
