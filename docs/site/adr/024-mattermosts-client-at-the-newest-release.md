---
search:
  boost: 0.3
---

# ADR-024: Mattermost is reached through its own client, at the commit of the newest supported release

mm-mcp reaches Mattermost through internal/mattermost, which wraps model.Client4 from github.com/mattermost/mattermost/server/public. The module is required at a pseudo-version of the commit the newest supported release is tagged at, the release docker/latest/compose.yml runs (ADR-025), not at a published module version: those are versioned apart from server releases, and two server releases can name the same one while their code differs. When the newest release changes, the module moves to its tag's commit in the same change, with `go get github.com/mattermost/mattermost/server/public@<commit>`.

internal/mattermost adds to Client4 only what mm-mcp needs around it: the transport of ADR-006, a request timeout, the User-Agent, one error type that carries Mattermost's status, error id, message and request id, and, where a supported release differs from the newest, the handling of that difference (ADR-025). Tools call internal/mattermost, never Client4 directly, and never build a URL by hand where Client4 has the call; where it lacks a parameter the server accepts, the method uses Client4's raw request helpers and says why.

Add a method to internal/mattermost beside the tool that needs it, with the live test that proves it. A failure to reach Mattermost and an answer from it are different errors; keep them apart.

The client and the types are what the server's own API tests run, so the shape of a request and of an answer is the server's, not a belief about it. One client at one commit is what the newest release serves; a release that differs is handled where it differs.

## Not chosen

- **Track the module's published versions**: They do not follow server releases, so none of them names what a given release serves.
- **A client per supported release**: Many copies of hundreds of methods for differences that fit in a short list, and a release check is still needed to choose between them.
- **A client generated from the OpenAPI specification**: The specification is a hand-written description of the server; the client is the server's own code.
