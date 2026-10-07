---
search:
  boost: 0.3
---

# ADR-026: The OpenAPI specification is vendored per supported release, as reference

Mattermost's OpenAPI specification for each supported release is vendored under openapi/: mattermost-latest.json and mattermost-esr.json. Mattermost publishes no document per release, so tools/openapi-spec builds each one as Mattermost's own build does, by concatenating the sources under api/v4/source at the release's tag in the order api/Makefile lists, and records the release in info.x-mattermost-release. The release is read from the image tag in docker/<stack>/compose.yml and stated nowhere else. Where two source files define one path, their operations are merged and the merge is reported, so nothing the specification documents is lost.

The specification is reference, not code. It is the only document of every parameter an endpoint takes and of the server version an operation or parameter first appeared in, which Client4 does not say, so it is what a tool's use of an endpoint is checked against, and one input to finding what differs between releases. What a request sends and an answer holds is the server's own code (ADR-024), and what the server does is the live suite's (ADR-004).

`task openapi:refresh` vendors both documents and needs the network; `task openapi:verify` fails when a vendored document is not the release its stack runs, needs nothing, and runs in `task quality:verify`. When a stack's image tag changes, refresh in the same change.

A document built from the sources at the tag is exactly what that release's documentation says; one fetched from the documentation site describes whatever is newest.

## Not chosen

- **Fetch the specification when it is needed**: Every check would need the network, and the documentation site publishes only the newest release.
- **Repair the specification where it is wrong**: Nothing generates code from it, so a defect matters only where a check reads it, and is fixed there.
