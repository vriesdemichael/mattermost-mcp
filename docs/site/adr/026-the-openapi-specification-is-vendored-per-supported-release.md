---
search:
  boost: 0.3
---

# ADR-026: Each supported release's specification and route table are vendored, as reference

For each supported release, two files are vendored under openapi/, built from a sparse clone of Mattermost's repository at the release's tag: the OpenAPI specification, mattermost-<stack>.json, and the route table, routes-<stack>.json. Mattermost publishes no specification per release, so tools/openapi-spec builds each one as Mattermost's own build does, by concatenating the sources under api/v4/source in the order api/Makefile lists, and records the release in info.x-mattermost-release. The route table is every endpoint the release's router registers in server/channels/api4, read from the source with Go's parser: what the server serves, whatever the specification says. The release is read from the image tag in docker/<stack>/compose.yml and stated nowhere else. Where two source files define one path, their operations are merged and the merge is reported, so nothing the specification documents is lost.

Both are reference, not code. The specification is the only document of every parameter an endpoint takes and of the server version an operation or parameter first appeared in, which Client4 does not say, so it is what a tool's use of an endpoint is checked against (ADR-028); with the route table it is how a difference between releases is found (ADR-027). What a request sends and an answer holds is the server's own code (ADR-024), and what the server does is the live suite's (ADR-004).

`task openapi:refresh` vendors all four files and needs the network; `task openapi:verify` fails when a vendored file is not the release its stack runs, needs nothing, and runs in `task quality:verify`. When a stack's image tag changes, refresh in the same change.

A document built from the sources at the tag is exactly what that release's documentation says; one fetched from the documentation site describes whatever is newest.

## Not chosen

- **Fetch the specification when it is needed**: Every check would need the network, and the documentation site publishes only the newest release.
- **Repair the specification where it is wrong**: Nothing generates code from it, so a defect matters only where a check reads it, and is fixed there.
