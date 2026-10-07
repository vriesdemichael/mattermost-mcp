---
search:
  boost: 0.3
---

# ADR-025: The supported window runs from the oldest supported Extended Support Release to the newest release

mm-mcp supports every Mattermost release from the oldest Extended Support Release that Mattermost still supports to the newest release. When Mattermost names a new Extended Support Release, the previous one stays in the window until Mattermost ends its support, so an organisation part way through an upgrade is not cut off. The two pinned ends are the image tags in docker/esr/compose.yml, the current Extended Support Release, and docker/latest/compose.yml, the newest release; they are the one place each is recorded, and the supported releases page states the window for readers. No record names a release, which `TestNoRecordNamesAMattermostVersion` holds.

The live suite runs against both pinned ends on every pull request (ADR-007). Every other release in the window, the previous Extended Support Release while it is supported and each minor release between the ends, runs through `task test:live RELEASE=<x.y.z>`, and CI runs the whole window once a week. A release in between is expected to behave like one of its neighbours until a run shows otherwise. A difference between the ends is found from each release's own files before any test runs (ADR-027).

mm-mcp is built against the newest release (ADR-024). Where an older release in the window differs in a call a tool makes, the difference is handled in that call and nowhere else: the call reads the server's release, which Mattermost reports in the X-Version-Id header of every answer, once per server, and either asks the older release in the way it understands or refuses before sending, naming the release that has the capability. A write is checked before it is sent, because Mattermost ignores a field it does not know and answers success. A field an older release does not send is left out of what a tool returns rather than reported as empty or false. The tool list is the same on every release. Each difference is listed on the supported releases page with the release it arrived in, and a live test whose behaviour differs by release asserts each side, with the boundary stated in the test.

Dependabot proposes updates to both images, and a person merges them (ADR-012): the newest release takes any update, and the Extended Support Release takes patch releases only. When Mattermost names a new Extended Support Release, move docker/esr to it by hand, in a change that also refreshes the vendored specifications (ADR-026) and updates the supported releases page.

The Extended Support Release is what organisations that move slowly run, and they are the people a work instance belongs to; the newest release is where API changes appear first. Two ends on every pull request is what CI can afford; the weekly run catches a difference in between before a user does.

## Not chosen

- **Drop the previous Extended Support Release as soon as a new one is named**: Organisations upgrade on their own schedule, and Mattermost supports both for months.
- **The newest release only**: The people this serves at work mostly run an Extended Support Release, and would find a difference first.
- **Hide a tool the connected release cannot serve**: A tool that is missing fails without saying why; one that refuses names the release that can.
