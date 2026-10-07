---
search:
  boost: 0.3
---

# ADR-032: Every list pages by an opaque cursor, to its end

Every tool that answers with a list takes `limit` and `cursor`, and answers with `next_cursor`, which is absent on the last page. The model passes `next_cursor` back as `cursor`, with the same other arguments, for the next page; a different `limit` is fine. This is MCP's own way of paging its lists, so a model that has paged resources has paged these. A list's `limit` has a small default and a hard maximum, and a list bounded by its input, such as `get_users` with the names it is given, is the one exception. `TestEveryListPagesByCursor` holds every tool that answers with a list to this.

A cursor is opaque: base64 of the tool's name, a fingerprint of the arguments that chose the list, and where to continue. A cursor from another tool, or from the same tool with other arguments, is refused, so a model cannot continue the wrong list. Where to continue follows the list's Mattermost endpoint. A channel is read back or forward from a post, by `before` and `after`, which stays right while people post. A thread is read on from a post and its time. A list Mattermost gives whole, such as a database search's answer or the user's channels, is cut into pages at an offset. A list Mattermost gives a page at a time, ordered by a value two items can share, such as a time to the millisecond, is read whole and put in an order with a tiebreaker, because Mattermost settles such ties afresh for each request and its page boundaries can repeat one item and skip another. Every order a tool pages through breaks ties by id, so the same arguments give the same pages.

A list reaches its end. Where Mattermost caps a list, the tool says so in its description: at most 1000 users for one search term, 50 public channels in a team for one channel search term. Where the specification documents paging the router does not do, and the router pages by a parameter the specification leaves out, the tool sends what the router reads, declared as undocumented (ADR-028) and shown working by a live test past one of Mattermost's pages. Where Mattermost pages wrongly, as its saved posts take their page parameter as an offset in posts, the call says so and sends what works, and a live test counts the requests reading 205 items takes.

A model told "there may be more" with no way to fetch it stops at the first page or guesses; a list it can read to its end is one it can answer from. One way of paging across every tool is one thing to learn. A page boundary that skips an item is worse than no paging, since nothing tells the model an item is missing.

## Not chosen

- **Expose Mattermost's own paging per tool**: page numbers here, post ids there, and offsets that are page numbers; the model would learn each, and some of them skip items.
- **A truncated flag and a larger limit**: The model can ask for more only up to the maximum, and nothing reaches past it.
- **A transparent cursor, such as a page number**: A model would compute its own, for another list or past the end.
