---
search:
  boost: 0.3
---

# ADR-009: Tests are sorted by what they need, and none of them skips

A unit test needs nothing beyond the machine it runs on. `task test:unit` runs every package under cmd/, internal/ and tools/; the pre-commit hook runs that task, and CI runs it on Linux, Windows and macOS, and under the race detector on Linux. A test that needs more carries a build tag and has a task of its own: `live` for a real Mattermost, in tests/live (`task test:live`); `browser` for the browsers on the machine, in internal/login (`task test:browser`), which CI runs on Linux, Windows and macOS with each runner's Chrome, Edge and Firefox and Playwright's Chromium, so `mm-mcp login` is proven where it ships (ADR-019); and, when the MCP Apps views arrive, `views` for a browser showing them (ADR-022). Without a tag, `go test ./...` needs no Mattermost and no browser. `task test:live:compile` builds the live suite without running it, so a change that breaks it fails in seconds rather than in the live job.

Tests run on every operating system. A test decides by the capability it needs and asserts on every system rather than skipping one by name. A test that cannot reach its dependency fails and says what is missing: the live suite exits before running anything when no Mattermost answers, naming the address it tried and the task that starts one.

Put a test that needs an outside dependency behind a tag and a task, so the fast suite stays fast and needs no setup. Do not call t.Skip on a missing dependency, and do not write a test that runs on one operating system only unless the code it tests is built for that system alone.

A skipped test reads as a passing one: a run that skipped everything reports success having proven nothing, and a skip on one system is coverage that never ran there. mm-mcp ships for all three systems.

## Not chosen

- **Skip when a dependency is missing**: The run stays green on a machine that cannot run the test, so nobody learns that it did not.
- **Live and unit tests in one suite**: Every commit would wait for a Mattermost, and a contributor without Docker could not run the tests at all.
