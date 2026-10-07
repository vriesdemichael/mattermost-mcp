---
search:
  boost: 0.3
---

# ADR-022: MCP Apps views, shown through one tool

mm-mcp adopts MCP Apps. In a client that renders them, the model can put what it found in front of the person as a view instead of describing it; a client that renders none gets text. The first views are a thread, read as Mattermost lays it out with names, avatars, Markdown, reactions and attachments; a digest of unread messages and mentions across channels; and a composer that shows a draft before it is posted.

Views are kinds of one tool, `show`, which the model calls once it has what the person asked about; the data tools carry no view, so an agent that reads twenty threads does not leave twenty views in its turn. What a view draws travels in the tool result beside a short text for the model, avatars included, which mm-mcp fetches with its own credential, because the client's webview has none. Text from Mattermost is never parsed as HTML: the page builds elements through the DOM, renders Markdown with a parser that produces elements, and opens a link only through the host. The view page is plain JavaScript and CSS embedded in the binary with go:embed, with no build step, and takes the host's theme and Mattermost's own wording.

The composer drafts; posting goes through the write tool, so the confirmation of ADR-021 applies whether the person posts from the view or the model posts on its own. What a person does in a view goes through the model's tools, never around them.

Add a view as a kind of `show`, never as UI metadata on a data tool. Build elements, never HTML from strings; the browser tests that arrive with the first view hold it. Count from Mattermost's totals, never from what a view lists, and say when a view lists fewer.

A person takes in a thread faster laid out as Mattermost lays it out than from a model's account of it. Data carried in the result lets a stored conversation render without the server running.

## Not chosen

- **A view on each data tool**: The model reads far more than the person needs to see.
- **A component library rendered on the server**: A thread and a composer need control over layout and Markdown rendering that a library constrains.
- **React bundled with a build step**: Node in every build, and a large bundle in every view, for pages a few hundred lines of plain JavaScript draw.
