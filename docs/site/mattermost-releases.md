# Supported Mattermost releases

mm-mcp supports every Mattermost release from the oldest Extended Support
Release that Mattermost still supports to the newest release
([ADR-025](adr/025-supported-mattermost-releases.md)). When Mattermost names a new
Extended Support Release, the previous one stays supported until Mattermost ends
its support.

| | Release | Tested with |
|---|---|---|
| Extended Support Release | 11.7 | 11.7.11, Team Edition |
| Newest release | 11.11 | 11.11.1, Team Edition |

The live suite runs against both ends on every change, and against the newest
patch of every minor release between them once a week. An older release may
work, but nothing checks it.

The tests run on the free Team Edition
([ADR-007](adr/007-the-live-instance-is-team-edition-in-docker.md)). mm-mcp works
with the licensed editions too, which serve the same API, but offers no tools
for features only they have, such as scheduled posts, guest accounts and LDAP
groups. The login is the exception: it logs in through single sign-on, SAML,
Entra ID or OpenID Connect, which are licensed features; that is tested by hand
([For administrators](administrators.md#what-is-tested)).

## Differences between releases

Each operation a tool calls is compared between the two ends, from each
release's own route table and specification
([ADR-027](adr/027-a-difference-between-supported-releases-is-found-from-mattermosts-own-files.md)).

| Operation | Tool | On the Extended Support Release |
|---|---|---|
| `CreatePost` | `create_post`, `dm`, `group_message` | No `silent` parameter, which the tool never sends. Nothing differs in use. |
| `DeleteDraft` | `delete_draft` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
| `DeleteDraftForThread` | `delete_draft` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
| `GetDrafts` | `save_draft`, `list_drafts`, `delete_draft` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
| `UpsertDraft` | `save_draft` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
| `GetTeamByName` | `get_team_info` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
| `SearchPostsInAllTeams` | `search_posts` | Served, as 11.7's router shows, but missing from its specification. Nothing differs in use. |
