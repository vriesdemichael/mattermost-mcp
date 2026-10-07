---
search:
  boost: 0.3
---

# ADR-031: A message is checked before anyone is asked to post it, and says it was written with AI

Every tool that takes a message the model wrote checks it before the person is asked: `create_post`, `dm`, `group_message`, `update_post` and `save_draft`. A message longer than the server takes, by the `MaxPostSize` it tells its clients, is refused with its length and the limit, so the model shortens it, splits it into replies, or attaches the full text as a file. Every @mention outside code is resolved against the server's users: one nobody has is refused with the closest usernames, and a deactivated user is named in the question, since they will not be notified. `@channel`, `@all` and `@here` are spelled out in the question with how many people they reach. The question shows the message exactly as it will be posted.

Every post and edit the model writes carries Mattermost's own `ai_generated_by` property, set to the user it was written for, which Mattermost's web app shows as AI-generated beside the post's time. Mattermost accepts the property only for the poster or a bot, so it says who the post is from and that a model wrote it, nothing more. An edit keeps the post's other properties. The question says the post will be marked. `MM_MCP_MARK_AI_GENERATED=false` turns the marker off; it is on unless turned off. A draft is not marked: the person sends it, after changing it as they like.

The server's own instructions tell the model how Mattermost renders a message, in a few lines every session reads once, rather than every tool's description repeating it.

A colleague reads a post as the person's own words, so what reaches them should be what the person agreed to, and say plainly when a model wrote it. A mention that names nobody notifies nobody and reads as a mistake; a mention of everyone notifies everyone, which the person should know before they tick the box. A message Mattermost would refuse is better refused before the person spends attention on it.

## Not chosen

- **Split an overlong message into replies on the tool's own initiative**: The person would confirm one post and get several; the model can split it, and each part is then asked about as what it is.
- **Leave the AI marker to the model**: It would be set inconsistently, and a post's provenance is not the model's to decide.
- **Mark drafts**: The person writes the last version of a draft and sends it themselves.
- **Formatting guidance in every tool's description**: It would cost every tool listing in every session for what one paragraph of the server's instructions says.
