# Agent Instructions — mm-mcp

mm-mcp is an MCP server for Mattermost, in Go, on the official MCP SDK and
Mattermost's own Client4. The rules for working here are in the decision records
under `docs/site/adr/`; this file holds the mechanics, the pitfalls, and the rules
from CONTRIBUTING.md an agent must not lose
([ADR-003](docs/site/adr/003-ai-agents-as-first-class-developers.md)).

## Rules that are easy to lose

- **Branch from `next`, open pull requests against `next`, rebase, never merge.**
  Commit subjects are Conventional Commits; the type decides the release
  ([ADR-011](docs/site/adr/011-next-integrates-and-main-releases.md),
  [ADR-013](docs/site/adr/013-releases-are-cut-from-conventional-commits-on-main.md)).
- **Close issues from the commit that fixes them**, with a keyword in its body,
  one per issue: `Closes #5, closes #6`. It takes effect when the commit
  reaches main ([ADR-017](docs/site/adr/017-planning-in-github-issues.md)).
- **Every list pages by cursor**: `limit` and `cursor` in, `next_cursor` out,
  read to its end ([ADR-032](docs/site/adr/032-every-list-pages-by-an-opaque-cursor.md)).
- **Every tool is called by a live test**, through an
  `mcp.CallToolParams{Name: "..."}` literal
  ([ADR-004](docs/site/adr/004-live-tests-against-a-real-mattermost.md)).
- **No fake Mattermost in unit tests.** No httptest.Server answering a
  Mattermost route, no fake of internal/mattermost
  ([ADR-005](docs/site/adr/005-unit-tests-do-not-simulate-mattermost.md)).
- **Read-only by default; every write asks**
  ([ADR-021](docs/site/adr/021-read-only-by-default-and-every-write-asks.md)).
- **No credential in a flag, an argument, a log line, an error or a tool result**
  ([ADR-019](docs/site/adr/019-credentials-are-supplied-not-acquired.md)).
- **Temporary files go in `.tmp/`**; tests use t.TempDir()
  ([ADR-018](docs/site/adr/018-agents-write-temporary-files-to-tmp.md)).
- **Ask before opening a GitHub issue**; the issues are the plan
  ([ADR-017](docs/site/adr/017-planning-in-github-issues.md)).

## Commands

```bash
task test:unit
task test:live
task test:live STACK=esr
task test:live RELEASE=11.9.2
task quality:verify
task quality:format
task docs:validate
task docs:adr-index
task openapi:refresh
```

`test:live` starts this checkout's Mattermost if needed. `quality:verify` is every
static gate, golangci-lint included. Run `docs:adr-index` after adding or
removing a record, and `openapi:refresh` after a stack's image tag changes.

## Layout

| Path | Holds |
|---|---|
| `cmd/mm-mcp` | the binary's entry point, nothing else |
| `internal/cli` | the command line: `serve`, `version` |
| `internal/config` | configuration from the environment, and the list of every variable read |
| `internal/server` | the MCP server, the tool catalogue (`AllSpecs`), and the tools |
| `internal/mattermost` | the wrapper around Client4 every tool goes through |
| `internal/network` | the one HTTP transport, with the unit-test network block |
| `internal/teststack` | how the live-test instances are named, found and bootstrapped |
| `internal/testsupport` | the unit-test seal |
| `internal/repository` | repository-wide governance tests |
| `tests/live` | the live suite (build tag `live`) |
| `tools/` | one Go command per directory, each with its reason in its package comment |
| `internal/apisurface` | reads the vendored specifications and route tables: matching, parameters, release differences |
| `openapi/` | the vendored specification and route table of each supported release, as reference |

## Fixing a bug

In this order, every time.

1. **Reproduce it with a live test**, against the local stack. A unit test is
   enough only when no request is involved: configuration parsing, argument
   validation, formatting.
2. **Look for the same bug next door, and reproduce that too.** Bugs come from an
   assumption about Mattermost, and an assumption is rarely applied once.
3. **Fix it.**
4. **Confirm the unit and live suites are green**, on both stacks when the call
   might differ between releases.

Then break each new test by reverting its fix and watch it fail.

## Adding a tool

1. Add the call to `internal/mattermost`, using Client4, returning model types or
   a `*mattermost.Error` ([ADR-024](docs/site/adr/024-mattermosts-client-at-the-newest-release.md)).
2. Add the tool in `internal/server/tools_<area>.go` with `toolSpec`, typed input
   and output structs, a title, the annotations from `readOnly`, and a
   description written for the model that reads it. Reach Mattermost only
   through the `ClientFor` it is given
   ([ADR-020](docs/site/adr/020-stdio-and-streamable-http-single-tenant-first.md)).
   A tool that writes takes `writes` instead, and wraps its handler in `asking`
   with a question naming what it writes, where, and as whom
   ([ADR-021](docs/site/adr/021-read-only-by-default-and-every-write-asks.md)).
   A tool that answers with a list embeds `pageArgs` and `pageInfo` and pages
   with `openCursor` ([ADR-032](docs/site/adr/032-every-list-pages-by-an-opaque-cursor.md)).
3. Declare its `Uses`: every operation it calls, by operationId in
   `openapi/mattermost-latest.json`, and for every parameter and body field of
   each, `SetBy(arg)`, `Fixed(value, reason)` or `Omitted(reason)`
   ([ADR-028](docs/site/adr/028-every-parameter-of-an-operation-a-tool-calls-is-accounted-for.md)).
   When `TestEveryDifferenceBetweenSupportedReleasesIsHandled` names a
   difference on the ESR, handle it in the call, say how in `Releases`, and list
   the operation on `docs/site/mattermost-releases.md`
   ([ADR-027](docs/site/adr/027-a-difference-between-supported-releases-is-found-from-mattermosts-own-files.md)).
4. Add its spec to `AllSpecs`.
5. Write the live test in `tests/live/`, calling the tool through `callTool`,
   which fails on a request to an operation the tool does not declare, and
   seeding what it needs with helpers that own their fixtures
   ([ADR-008](docs/site/adr/008-a-live-test-seeds-and-owns-its-fixtures.md)).
6. Document it on `docs/site/tools.md`.

## Gotchas

- **The Mattermost module is a pseudo-version** at the commit of the newest
  release's tag, not a published version; Dependabot leaves it alone. Move it
  with `go get github.com/mattermost/mattermost/server/public@<commit>` in the
  change that moves docker/latest.
- **Every test package seals itself** with
  `func TestMain(m *testing.M) { testsupport.SealedMain(m) }`. A new package
  with tests needs it; `TestEveryTestPackageIsSealed` says so.
- **A test passes configuration through a Getenv**, `testsupport.Env(...)`, never
  t.Setenv, which also rules out t.Parallel.
- **A new environment variable** goes in `config.EnvironmentVariables` and on
  `docs/site/configuration.md` in the same change.
- **Mattermost deactivates rather than deletes** a user by default, so a live
  fixture deactivates its user in cleanup, and a username is never reused: draw
  it from `uniqueName`.
- **Search is indexed asynchronously.** A live test that searches for what it
  just posted polls with a deadline; it never sleeps a fixed time.
- **The instance's port is in `.tmp/stack-<stack>.env`**, not fixed, in a linked
  worktree. Run the live suite through the task, or set MM_LIVE_URL.
- **A local image updater** such as Watchtower must not touch the test stack;
  the compose files label their containers to opt out.
- **Line endings are LF.** `git add --renormalize .` fixes a file a tool wrote
  with CRLF.

## Governance tests

Files named `governance_test.go` hold tests that assert an invariant over
everything of one kind. The full list is in
[ADR-015](docs/site/adr/015-governance-tests-are-verified-by-breaking-them.md),
which a test keeps in step with the code. Break a new one before trusting it.
