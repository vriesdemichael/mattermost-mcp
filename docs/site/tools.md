---
hide:
  - navigation
---

# Tools

<p class="mm-lead">Everything your agent can do in Mattermost with mm-mcp. Ask in
your own words; it picks the tool. Writing is off until you allow it.</p>

<div class="mm-stats">
<a class="mm-stat" href="#every-tool"><strong>45</strong> tools</a>
<span class="mm-stat mm-stat--read"><strong>24</strong> only read</span>
<span class="mm-stat mm-stat--asks"><strong>14</strong> ask you first</span>
<span class="mm-stat mm-stat--yours"><strong>6</strong> yours alone, not asked</span>
<span class="mm-stat mm-stat--disk"><strong>1</strong> saves to your disk</span>
</div>

<div class="grid cards mm-jobs" markdown>

-   :material-inbox-arrow-down:{ .lg .middle } **Catch up**

    ---

    Pick up where you left off, in every channel, without marking anything read.

    “What did I miss since Friday?”
    { .mm-example }

    [`read_unread`](#read_unread){ .mm-chip } [`get_user_channels`](#get_user_channels){ .mm-chip } [`list_mentions`](#list_mentions){ .mm-chip } [`list_threads`](#list_threads){ .mm-chip }

-   :material-text-search:{ .lg .middle } **Find it again**

    ---

    Messages, files, people and channels, searched the way you would say it: by
    words, by who, where and when.

    “What did we decide about the release date?”
    { .mm-example }

    [`search_posts`](#search_posts){ .mm-chip } [`search_files`](#search_files){ .mm-chip } [`search_users`](#search_users){ .mm-chip } [`search_channels`](#search_channels){ .mm-chip }

-   :material-forum-outline:{ .lg .middle } **Read the whole story**

    ---

    A channel page by page, a thread from its first post, and what was pinned or
    saved.

    “Summarise the thread about last night's outage.”
    { .mm-example }

    [`read_channel`](#read_channel){ .mm-chip } [`read_post`](#read_post){ .mm-chip } [`list_pinned_posts`](#list_pinned_posts){ .mm-chip } [`list_saved`](#list_saved){ .mm-chip }

-   :material-file-document-outline:{ .lg .middle } **Open the attachments**

    ---

    Word, Excel and PowerPoint as their text, PDFs, images and archives, read for
    the model, or saved to your disk.

    “What's in the spreadsheet Sam shared?”
    { .mm-example }

    [`read_file`](#read_file){ .mm-chip } [`save_file`](#save_file){ .mm-chip .mm-chip--disk }

-   :material-send-check-outline:{ .lg .middle } **Post, reply and react**

    ---

    Messages, replies, direct and group messages, edits and reactions, each
    shown to you before it goes out.

    “Tell Sam in the thread that the notes are ready.”
    { .mm-example }

    [`create_post`](#create_post){ .mm-chip .mm-chip--asks } [`dm`](#dm){ .mm-chip .mm-chip--asks } [`group_message`](#group_message){ .mm-chip .mm-chip--asks } [`update_post`](#update_post){ .mm-chip .mm-chip--asks } [`delete_post`](#delete_post){ .mm-chip .mm-chip--asks } [`add_reaction`](#add_reaction){ .mm-chip .mm-chip--asks } [`remove_reaction`](#remove_reaction){ .mm-chip .mm-chip--asks } [`pin_post`](#pin_post){ .mm-chip .mm-chip--asks }

-   :material-account-group-outline:{ .lg .middle } **Channels and people**

    ---

    Who is who and who is around, which channels exist and who is in them; join,
    leave, create, add people, and set your status.

    “Is Sam online, and who else is in ~release-planning?”
    { .mm-example }

    [`get_me`](#get_me){ .mm-chip } [`get_users`](#get_users){ .mm-chip } [`get_status`](#get_status){ .mm-chip } [`get_user_teams`](#get_user_teams){ .mm-chip } [`get_team_info`](#get_team_info){ .mm-chip } [`get_channel_info`](#get_channel_info){ .mm-chip } [`list_team_channels`](#list_team_channels){ .mm-chip } [`list_archived_channels`](#list_archived_channels){ .mm-chip } [`get_channel_stats`](#get_channel_stats){ .mm-chip } [`join_channel`](#join_channel){ .mm-chip .mm-chip--asks } [`leave_channel`](#leave_channel){ .mm-chip .mm-chip--asks } [`add_channel_members`](#add_channel_members){ .mm-chip .mm-chip--asks } [`create_channel`](#create_channel){ .mm-chip .mm-chip--asks } [`set_status`](#set_status){ .mm-chip .mm-chip--asks }

-   :material-bookmark-outline:{ .lg .middle } **Your own things**

    ---

    A draft in your message box to send yourself, reminders, saved posts and
    followed threads. Nobody else sees them, so nothing is asked.

    “Draft a reply I can send myself, and remind me at nine tomorrow.”
    { .mm-example }

    [`save_draft`](#save_draft){ .mm-chip .mm-chip--yours } [`list_drafts`](#list_drafts){ .mm-chip } [`delete_draft`](#delete_draft){ .mm-chip .mm-chip--asks } [`set_post_reminder`](#set_post_reminder){ .mm-chip .mm-chip--yours } [`save_post`](#save_post){ .mm-chip .mm-chip--yours } [`follow_thread`](#follow_thread){ .mm-chip .mm-chip--yours } [`mark_channel_read`](#mark_channel_read){ .mm-chip .mm-chip--yours } [`typing`](#typing){ .mm-chip .mm-chip--yours }

-   :material-stethoscope:{ .lg .middle } **Check the setup**

    ---

    When something fails: the connection, the login, and whether your app shows
    mm-mcp's questions.

    “Why can't you post?”
    { .mm-example }

    [`diagnose`](#diagnose){ .mm-chip }

</div>

## Every tool

Grouped as above. A badge says when a tool does more than read:
<span class="mm-badge mm-badge--asks">asks you first</span> for what others see,
<span class="mm-badge mm-badge--yours">yours alone</span> for what only you see
or is gone in seconds, and <span class="mm-badge mm-badge--disk">your disk</span>.
The tools that write are offered only once you allow writes, with
`MM_MCP_ALLOW_WRITES` ([Configuration](configuration.md)).

### Catch up

`read_unread`: Read unread posts { #read_unread }
:   Catches up on a channel from where you stopped reading: a few posts you have
    read, then the ones you have not, oldest first, with the first unread one
    named. A channel you never opened is all unread, and reads newest first,
    the pages after going further back. It marks nothing read: what you have
    read is yours to record.

`get_user_channels`: List the user's channels { #get_user_channels }
:   The channels you belong to, direct and group messages included, most
    recently active first, each with its team and how many messages and
    mentions are unread. `team_id` keeps one team's, `unread_only` those with
    something unread.

`list_mentions`: List mentions { #list_mentions }
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

`list_threads`: List threads { #list_threads }
:   The threads you follow, as Mattermost's threads view lists them, most
    recently replied to first: the post that started each, who took part, and
    how many replies and mentions you have not read.

### Find it again

`search_posts`: Search posts { #search_posts }
:   Messages you can read, across every team or one, by words, and by who wrote
    them, where and when through `from`, `in`, `before`, `after` and `on`, so no
    search syntax is needed. `@username` finds where someone was mentioned.
    20 posts a page by default, at most 100. Mattermost's search finds the 100
    most recent matches at most, and the answer says `capped` when it reached
    them: `before` reaches older ones. `in` takes a channel you belong to, the
    only ones Mattermost searches, and `from` a username, refused with the
    closest when nobody has it. The days `before`, `after` and `on` name are
    your own, in the timezone you set in Mattermost, as in its search box.

`search_files`: Search files { #search_files }
:   Files attached to posts you can read, by name, by type with `ext:pdf`, and by
    `from`, `in`, `before`, `after` and `on`, each with the post and channel it
    is in. Like `search_posts`, it finds the 100 most recent matches at most.

`search_users`: Search users { #search_users }
:   Users whose username, name, nickname or email address contains a term,
    optionally only within a team or a channel. Deactivated users are left out.
    Mattermost finds at most 1000 for one term.

`search_channels`: Search channels { #search_channels }
:   Channels by part of their name: your own, and public ones you have not
    joined. Mattermost finds at most 50 public channels in a team for one term.

### Read the whole story

`read_channel`: Read channel { #read_channel }
:   A channel's messages, 30 a page by default and at most 200, oldest first
    within a page: the newest, and the pages after go further back. `before`
    starts back from a post; `after` and `since`, from a post or a time, read
    forward. `since` takes a time with an offset, or a time or a day in your
    own timezone. `collapse_threads` leaves the replies out and shows each
    thread by the post that started it, as Mattermost shows a channel with
    collapsed reply threads.

    A page here or of `read_unread` can hold a few more posts than its limit:
    posts written in the same millisecond stay on one page, since Mattermost
    reads on from a post by its time alone, and a page split inside a
    millisecond would lose posts.

`read_post`: Read post { #read_post }
:   A post and the thread it is in, oldest first, 100 posts a page by default.
    Give any post in the thread; `include_thread` false reads the post alone.

`list_pinned_posts`: List pinned posts { #list_pinned_posts }
:   The posts pinned to a channel.

`list_saved`: List saved posts { #list_saved }
:   The posts you saved to come back to.

### Open the attachments

`read_file`: Read file { #read_file }
:   A file attached to a post, as content the model can read: text in windows of
    numbered lines, chosen with `start_line` and `line_count`; Word, PowerPoint
    and Excel files as their text; zip and tar archives as a listing; images as
    images, turned upright and scaled down when large; small audio, video and
    PDF files as themselves, for a client that can read them. Anything else is
    described by its type and size, with the post's link. Files over 64 MiB are
    described without being read.

`save_file`: Save file { #save_file .mm-disk }
:   Saves a file attached to a post into your download directory, set with
    `MM_MCP_DOWNLOAD_DIR` and your Downloads directory by default, under its own
    name, and answers with the path. An existing file is never overwritten: a
    number is added to the name instead, and saving the same file again
    answers with the copy already saved. The file is marked as downloaded from
    the internet, as a browser marks it: on Windows, SmartScreen checks a
    program and Office opens a document in Protected View; on macOS,
    Gatekeeper checks it. Files up to 100 MiB. Offered only when mm-mcp runs on
    your machine, whether writes are allowed or not
    ([below](#on-your-machine)).

### Post, reply and react

Each of these asks you first, showing exactly what will happen
([below](#how-a-write-is-asked)).

`create_post`: Create post { #create_post .mm-asks }
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
    kept is never attached ([Security](security.md#credentials)); attach such
    a file in Mattermost yourself if you mean to. `dm` and `group_message`
    take `files` too.

`dm`: Send direct message { #dm .mm-asks }
:   Sends a direct message to one person, by username or email address, or to
    yourself when none is given. This is how to message people: "message Alice
    and Bob" is two direct messages.

`group_message`: Send group message { #group_message .mm-asks }
:   Sends one message to two to seven people together, in the group conversation
    Mattermost keeps for exactly them. Only when you ask for a group
    conversation.

`update_post`: Update post { #update_post .mm-asks }
:   Replaces the text of one of your own posts. The question shows the old text
    and the new. An edit notifies nobody, so only its length is checked.
    Another person's post is refused, even with an administrator's credential.

`delete_post`: Delete post { #delete_post .mm-asks }
:   Deletes one of your own posts. Deleting the post that starts a thread
    deletes its replies, other people's included, and the question says how
    many.

`add_reaction`: Add reaction { #add_reaction .mm-asks }
:   Reacts to a post with an emoji, by name, such as `thumbsup`, or as itself,
    such as 👍. An unknown name is refused with the closest ones. A reaction
    joins one others gave under another name of the same emoji, so
    `thumbsup` beside a `+1` is the one 👍. Reacting again with the same
    emoji changes nothing.

`remove_reaction`: Remove reaction { #remove_reaction .mm-asks }
:   Takes back one of your reactions.

`pin_post`: Pin post { #pin_post .mm-asks }
:   Pins a post to its channel for everyone, or unpins it with `pinned` false.

### Channels and people

`get_me`: Who am I { #get_me }
:   The Mattermost user the server acts as: id, username, name, nickname,
    position, roles, the timezone set in Mattermost, and whether it is a bot.
    Call it to check the connection and whose access the other tools use.

`get_users`: Get users { #get_users }
:   People by username, user id or email address, any mix of them in one call,
    with whether each is a bot or deactivated. An email address finds someone
    only when the server shows you addresses. A name nobody has is listed
    with the closest usernames beside the people found, and refused only when
    nobody is found.

`get_status`: Get status { #get_status }
:   Whether people are around: online, away, do not disturb or offline, when
    they were last active, and the status message they set.

`get_user_teams`: List the user's teams { #get_user_teams }
:   The teams you belong to.

`get_team_info`: Get team { #get_team_info }
:   A team by id or name. An open team you are not in is found by the exact name
    in its address.

`get_channel_info`: Get channel { #get_channel_info }
:   A channel by id or name, among your own channels and the public channels of
    your teams, saying whether you belong to it and whether it is archived.

`list_team_channels`: List a team's channels { #list_team_channels }
:   A team's public channels, by name.

`list_archived_channels`: List archived channels { #list_archived_channels }
:   A team's archived channels. An archived channel can be read but not posted
    in.

`get_channel_stats`: Get channel stats { #get_channel_stats }
:   How many people belong to a channel, how many are guests, and how many posts
    are pinned and files shared in it.

`join_channel`: Join channel { #join_channel .mm-asks }
:   Joins a public channel of one of your teams. Everyone in it sees that you
    joined. A private channel is joined only by being added.

`leave_channel`: Leave channel { #leave_channel .mm-asks }
:   Leaves a channel you belong to. Everyone in it sees that you left, and the
    question says when the channel is private, where only being added brings
    you back.

`add_channel_members`: Add channel members { #add_channel_members .mm-asks }
:   Adds up to twenty people, by username, user id or email address, to a
    channel you belong to. Each is notified, and the question names each.

`create_channel`: Create channel { #create_channel .mm-asks }
:   Creates a public or private channel in one of your teams, with you in it,
    its address made from its name as Mattermost's own app makes it, and its
    purpose and header when given.

`set_status`: Set status { #set_status .mm-asks }
:   Sets your status as everyone sees it beside your name: online, away, do not
    disturb until a time, or offline, and a status message with an emoji, until
    a time or until changed; or clears the message. A time without an offset
    is read in your timezone.

### Your own things

`save_draft`: Save draft { #save_draft .mm-yours }
:   Puts a message in your message box in Mattermost as a draft, in a channel or
    a thread, for you to change and send yourself. The message is checked as a
    post's is, and its notes say what its mentions will do. A different draft
    already there is never replaced, and a draft is refused when your drafts do
    not sync, since you would never see it. Where your app does not show
    mm-mcp's question, this is still a way to post only what you send.

`list_drafts`: List drafts { #list_drafts }
:   Your drafts, in channels and threads, with where each is.

`delete_draft`: Delete draft { #delete_draft .mm-asks }
:   Deletes your draft in a channel or thread. Nobody else sees a draft, but it
    may hold words you have not sent, so the question shows it and where it is
    first.

`set_post_reminder`: Set post reminder { #set_post_reminder .mm-yours }
:   Has Mattermost remind you of a post at a time, as its "Remind me" does. A
    time without an offset is read in the timezone you set in Mattermost.

`save_post`: Save post { #save_post .mm-yours }
:   Saves a post among your saved posts, or removes it with `saved` false.

`follow_thread`: Follow thread { #follow_thread .mm-yours }
:   Follows a thread, so its replies notify you and it appears in
    `list_threads`, or stops following it with `following` false.

`mark_channel_read`: Mark channel read { #mark_channel_read .mm-yours }
:   Marks a channel read, as opening it in Mattermost does, clearing its unread
    count and mentions, only when you ask for it. Reading a channel for you
    never marks it read.

`typing`: Show typing { #typing .mm-yours }
:   Shows you typing in a channel or thread while a message is written, kept up
    until `create_post` posts there, `stop` is sent, or a minute passes. It
    is optional, for a long message that takes a while to write.

### Check the setup

`diagnose`: Diagnose mm-mcp { #diagnose }
:   The checks `mm-mcp doctor` makes, from inside the running server, so with
    the configuration your MCP client actually started it with: the address,
    which credential it acts with, a login `mm-mcp login` stored and whether
    `MM_TOKEN` hides it, the proxy, whether the server answers as a supported
    Mattermost, whose credential it is and whether that user belongs to a team.
    It adds what only the client shows: its name and protocol version, and
    whether it can show the question mm-mcp asks before each write, or that
    mm-mcp leaves the asking to it. With `ask_test_question`, it shows you a
    test question and reports how it was answered: an answer within a few
    seconds came from the client itself, which then answers every question
    before a write without showing it. Each check says what it found, and a
    failed one what to do. It never returns the credential
    ([Configuration](configuration.md#command-line)).

## Good to know

### Works the same everywhere

- **Names work where ids do.** Every `channel_id` and `team_id` takes a name as
  well as an id, a channel's with `~` or `#` before it or not, and every
  `post_id` and `root_id` takes the address the post opens at. A name of a
  channel, a team or a person matches in any case, whole or in part; an
  ambiguous one is refused with every candidate, and an unknown one with the
  closest names
  ([ADR-030](adr/030-names-are-matched-leniently-and-a-refusal-names-the-next-step.md)).
- **Every list pages the same way.** Give `limit`, and pass the `next_cursor` an
  answer ends with back as `cursor`, with the same other arguments, for the next
  page, until an answer has none
  ([ADR-032](adr/032-every-list-pages-by-an-opaque-cursor.md)).
- **Every post comes back in one shape:** its address, which opens it in
  Mattermost, its author's username and name, its channel and team by name, its
  files with their ids, reactions with who reacted, whether it is pinned or
  marked as written with AI, its thread, and when it was edited. An answer whose
  posts are all in one channel names the channel and team once.
- **A list cuts a long message short.** A message longer than 4,000 characters
  is cut, with its whole length said; `read_post` reads it whole.
- **Times are your own.** A time in an answer is in the timezone you set in
  Mattermost, with its offset, such as `2026-10-09T09:30:00+02:00`, and a time
  you give without an offset is read in it too. With no timezone set, UTC.

### How a write is asked

Each tool that changes what others see asks you through your MCP client: it
shows what will change, where, and under whose name, and acts only when you
tick the box and accept
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)). The question
is an MCP elicitation, a form the client shows, so these tools need a client
that shows one, such as Claude Code. A client that declares it cannot gets an
error that says so, and nothing is written; `save_draft`, which does not ask,
still puts a message in your message box for you to send. Some clients declare
that they can and never show the form: the call waits until the client gives up
on it, as Claude Desktop has been reported to, or is declined at once, as in the
Claude desktop app's Code tab.

`MM_MCP_ASK_BEFORE_WRITES` decides who asks
([Configuration](configuration.md),
[ADR-033](adr/033-who-asks-before-a-write-is-a-setting.md)). Everything here is
how mm-mcp asks, its default. Set to `false`, mm-mcp asks nothing, and your MCP
client's own approval of each tool call, if it asks, is the only check;
`MM_MCP_FORCE_HUMAN_IN_THE_LOOP_IN_CLAUDE_CODE` then has Claude Code ask on
every call. The checks on a message below hold either way.

Every message is checked before you are asked
([ADR-031](adr/031-a-message-is-checked-before-anyone-is-asked-to-post-it.md)):
one longer than the server takes is refused with its length and the limit, an
@mention of someone nobody is is refused with the closest usernames, and the
question says how many people `@here`, `@channel` and `@all` reach, who of
those mentioned is deactivated, and, on a licensed server with user groups, how
many a group's mention notifies. Mentions are found as Mattermost finds them, so
code mentions nobody and `@here.` ending a sentence is `@here`. What a post
does, and every file it carries, comes before the message in the question.

What a write acts on is what you were asked about: a reply posted, a file
rewritten or a draft typed on while you read the question stops the write.
Posts and edits are marked as written with AI, as Mattermost shows it, unless
`MM_MCP_MARK_AI_GENERATED` is false.

### On your machine

`save_file`, and attaching a file by its `path`, are offered only when mm-mcp
runs on your own machine, started by your client over stdio, whether writes are
allowed or not: they reach your disk, not Mattermost
([ADR-029](adr/029-files-reach-the-model-as-content-and-the-disk-only-locally.md)).

### Not offered

- **Scheduled posts.** Mattermost's scheduled posts need a licence, and Team
  Edition, which mm-mcp is tested against, refuses them
  ([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).
- **Marking what the model read as read, or anything unread.** What you have
  read is yours to record, and a model reading a channel is not you reading
  it; `mark_channel_read` marks a channel read when you ask
  ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

### How the tools are built

Every tool declares a title and the four MCP annotations: whether it only reads,
whether it may destroy something, whether repeating it changes nothing more, and
that it works in a closed domain, your one Mattermost server
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)). A client
may use them to decide what to ask you about.

Each tool also accounts for every parameter of the Mattermost endpoints it
calls: which argument sets it, or why it is fixed or left out
([ADR-028](adr/028-every-parameter-of-an-operation-a-tool-calls-is-accounted-for.md)),
and a live test calls every tool against a real Mattermost
([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).
