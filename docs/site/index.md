---
title: An MCP server for Mattermost
hide:
  - navigation
  - toc
---

<div class="mm-hero" markdown>

<div markdown>

# Your Mattermost, within your agent's reach

<p class="mm-hero__lead">mm-mcp is an MCP server for Mattermost. It lets an AI
agent read and search your Mattermost, and, when you allow it, post and reply,
as you or as a bot. Each post others will see waits for your yes.</p>

[Install mm-mcp](installation.md){ .md-button .md-button--primary }
[Log in](login.md){ .md-button }

</div>

<div class="mm-chat" role="img" aria-label="A conversation: you ask your agent what you missed in a channel, it reads the unread posts through mm-mcp and sums them up, and before it replies for you, mm-mcp asks whether to post.">
<div class="mm-chat__bar"><span class="mm-chat__dot"></span>Your agent, with mm-mcp</div>
<div class="mm-chat__body">
<div class="mm-msg mm-msg--you">What did I miss in ~release-planning?</div>
<div class="mm-tool">read_unread ~release-planning</div>
<div class="mm-msg mm-msg--agent">Two threads since Friday. The release moved to Thursday, and Sam asks whether the 2.4 notes are ready.</div>
<div class="mm-msg mm-msg--you">Tell Sam they are.</div>
<div class="mm-tool">create_post ~release-planning</div>
<div class="mm-msg mm-msg--agent mm-ask">Post in ~release-planning as @you?<br><em>“The 2.4 notes are ready, Sam.”</em>
<div class="mm-ask__buttons"><span>Post</span><span>Cancel</span></div>
</div>
</div>
</div>

</div>

## What it is for

<div class="grid cards" markdown>

-   :material-text-search:{ .lg .middle } **Reading and searching**

    ---

    Channels, threads, unread messages and mentions, and people and channels
    looked up by the names you use.

-   :material-send-check-outline:{ .lg .middle } **Posting and replying**

    ---

    Posts, replies, edits, reactions and pins, joining and creating channels,
    and your status, once you allow writes, each confirmed by you before it is
    sent.

-   :material-bookmark-outline:{ .lg .middle } **Your own things**

    ---

    Following threads, saved posts, reminders, drafts, the typing indicator and
    marking a channel read, without a question each time.

-   :material-paperclip:{ .lg .middle } **Files and attachments**

    ---

    Read as text or images the model can use, uploaded, and saved to your
    machine.

</div>

Planned, not yet built: **views in the chat**, so that in a client that renders
MCP Apps a thread appears as Mattermost lays it out and a draft appears before
it is posted
([ADR-022](adr/022-mcp-apps-views-through-one-show-tool.md)).

## How it behaves

<div class="grid cards" markdown>

-   :material-shield-check-outline:{ .lg .middle } **Read-only unless you say otherwise**

    ---

    The tools that post or change anything are not even offered until
    `MM_MCP_ALLOW_WRITES` is true. Then each change others see asks you first;
    following a thread, saving a post, a reminder, a draft only you see and the
    typing indicator do not
    ([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).
    `MM_MCP_ASK_BEFORE_WRITES=false` leaves the asking to your MCP client's own
    approval, for a client that does not show mm-mcp's question
    ([ADR-033](adr/033-who-asks-before-a-write-is-a-setting.md)).

-   :material-account-key-outline:{ .lg .middle } **One server, one identity**

    ---

    It acts as whoever owns the token it is given: a personal access token, a
    bot's token, or the session of your own login through
    [`mm-mcp login`](login.md), single sign-on included
    ([ADR-019](adr/019-credentials-are-supplied-not-acquired.md)).

-   :material-lan-connect:{ .lg .middle } **stdio or HTTP**

    ---

    A desktop or IDE client starts it as a local process; over Streamable HTTP
    it serves a loopback address for now
    ([ADR-020](adr/020-stdio-and-streamable-http-single-tenant-first.md)).

-   :material-test-tube:{ .lg .middle } **Tested against a real Mattermost**

    ---

    Every tool is exercised against Team Edition, on the current Extended
    Support Release and the newest release
    ([ADR-004](adr/004-live-tests-against-a-real-mattermost.md)).

</div>

## Next to Mattermost's own MCP server

Mattermost publishes an MCP server of its own, inside its Agents plugin. mm-mcp
differs on purpose:

- **Any edition, no plugin.** mm-mcp talks to Mattermost's REST API as a client
  does, so it runs against Team Edition as well; the other's write tools need an
  Enterprise licence, and it runs inside a plugin on the server.
- **As you.** It acts with your own token or a bot's, and every post says it was
  written with AI.
- **Asks before every write others see**, through your MCP client, showing what
  will be posted, where and as whom.
- **Tools shaped for an agent** rather than one per endpoint: a post comes with
  its author, channel, team, files and reactions, and names are found as a
  person writes them ([ADR-030](adr/030-names-are-matched-leniently-and-a-refusal-names-the-next-step.md)).

!!! note "Young"
    mm-mcp is young: its [tools](tools.md) read, search, post and handle files,
    and are tested against a real Mattermost on every change. The
    [issues](https://github.com/vriesdemichael/mm-mcp/issues) are the plan.

mm-mcp is an independent project. It is not affiliated with, endorsed by or
supported by Mattermost, Inc.; Mattermost is a trademark of Mattermost, Inc.
