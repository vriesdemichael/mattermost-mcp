# Contributing

Thanks for considering a contribution.

This project validates its tools against a **real Mattermost** rather than
against mocks. The instance is Mattermost Team Edition in Docker, free and
unlicensed, so you can run the full suite locally with no account and no
secrets — the same suite that runs on your pull request.

## Prerequisites

| Tool | Why |
|---|---|
| **Go** | building and testing; go.mod pins the toolchain, and Go fetches it |
| **[Task](https://taskfile.dev)** | every workflow in this repo is a `task` target |
| **Docker** | runs the local Mattermost for the live suite |
| **git** | |

Recommended, not required: [lefthook](https://lefthook.dev) runs the unit tests
before each commit and the fast gates before each push, and
[uv](https://docs.astral.sh/uv/) builds the documentation site. Node is needed
only to validate an `.mcpb` bundle locally.

Install the Task CI runs, pinned as `GO_TASK_VERSION` in
`.github/tool-versions.env`:

```bash
go install github.com/go-task/task/v3/cmd/task@v3.48.0
```

```bash
lefthook install
```

## First run

```bash
git clone https://github.com/vriesdemichael/mm-mcp
cd mm-mcp
task test:unit
```

The unit suite needs no network and no Mattermost. If it passes, you have a
working environment. `task` lists every task.

## Running the live suite

This is the gate that matters, and the one most changes need.

```bash
task test:live
```

It starts this checkout's Mattermost first when it is not running. A fresh
instance is ready in about fifteen seconds once the image is pulled, and the
suite takes seconds. `task test:live STACK=esr` runs it against the Extended
Support Release, and `task test:live RELEASE=11.9.2` against any release.

Each checkout, every linked worktree included, has its own instances, so
parallel work never shares one; see [docker/README.md](docker/README.md).

## Making a change

**Branch from `next`.** Every change targets `next`; `main` moves only when a
maintainer promotes `next` onto it, and only `main` releases
([ADR-011](docs/site/adr/011-next-integrates-and-main-releases.md)).

**Use [Conventional Commits](https://www.conventionalcommits.org/).** The commit
type decides the version of the release your change ships in:

| Type | From 1.0.0 | Before 1.0.0 |
|---|---|---|
| `feat` | minor | patch |
| `fix`, `perf`, `revert` | patch | patch |
| any type with `!`, or a `BREAKING CHANGE:` footer | major | minor |
| `ci`, `chore`, `docs`, `style`, `refactor`, `test`, `build` | **no release** | **no release** |

A breaking change is one that breaks a configuration that works today: a tool
renamed or removed, an argument renamed or made required, a result's shape
changed, an environment variable renamed.

**Close issues from the commit that fixes them.** Put a closing keyword in the
commit's body, one per issue: `Closes #5, closes #6`. GitHub acts on it when the
commit reaches `main`, so the issue closes when `next` is promoted, with the
release that promotion cuts if its commits call for one
([ADR-017](docs/site/adr/017-planning-in-github-issues.md)).

**Keep history linear.** Rebase onto `next`; never merge `next` into your branch.

```bash
task pr:rebase
```

**Every tool has a live test.** A tool no live test calls fails the unit suite
(`TestEveryToolIsCalledByALiveTest`). Call it with an
`mcp.CallToolParams{Name: "..."}` literal, so the check can read it, and assert
on what it returned.

**A tool that writes is gated, and asks when others see the change.** Annotate
it as not read-only. When what it changes is seen by others, confirm each call
with the person; when it is the user's alone or gone in seconds, such as
following a thread or showing them typing, say why it does not ask
([ADR-021](docs/site/adr/021-read-only-by-default-and-every-write-asks.md)).

## Before opening a pull request

```bash
task quality:verify
task test:unit
task docs:validate
```

and the live tests that cover what you changed. `task quality:format` fixes
formatting.

### What the git hooks do

- **pre-commit** runs the unit suite.
- **pre-push** runs `task quality:verify` — formatting, line endings, the
  decision records, the vendored specifications, golangci-lint — builds the live
  suite without running it, and builds the docs.

The live suite and the coverage gate run in neither: they need Docker, and CI
runs them on every pull request. Run `task quality:coverage` when you want the
full gate locally; when patch coverage fails, add tests and re-run
`task quality:coverage:gates`, which re-checks the profiles already in `.tmp/`
in seconds. Do not bypass hooks with `--no-verify`.

### Line endings

`.gitattributes` pins every file to LF on every platform. If a tool writes CRLF
anyway, `task quality:line-endings:verify` fails; fix it with:

```bash
git add --renormalize .
```

## What CI checks

| Job | What it does |
|---|---|
| Release Flow | refuses every pull request into `main` and points it at `next` |
| Unit Tests | formatting, line endings, the decision records, the vendored specifications, golangci-lint, govulncheck, the unit suite with coverage and under the race detector, and that the live suite compiles |
| Unit Tests (windows-latest), Unit Tests (macos-latest) | the unit suite on Windows and macOS |
| Live Tests | starts Mattermost and runs the live suite, on the newest release and the Extended Support Release |
| Coverage Gates | global and patch coverage over the unit and live profiles merged |
| Codecov | publishes coverage history and the README badge |
| Docs Site | builds the documentation strictly |
| Release Artifacts | builds every platform's archive, SBOM and `.mcpb` bundle |
| CI Complete | passes only when every job above did; the one status branch protection requires |

A weekly workflow also runs the live suite against every Mattermost release
between the two ends. Live tests run on pull requests from forks; if they fail
on yours, the failure is real.

## Where the deeper detail lives

- [`AGENTS.md`](AGENTS.md) — the mechanics and gotchas of working in this
  repository. Written for AI agents, but the content applies to anyone.
- [`docs/site/adr/`](docs/site/adr/index.md) — the decision records. If you want
  to know *why* something works the way it does, it is there.
- [`docker/README.md`](docker/README.md) — the local Mattermost instances.

## Reporting bugs

Open an issue and pick **Bug report**. Anything that is not a bug — a feature, a
question, an idea worth arguing about — goes in a blank issue. Discussions are
not enabled, so issues are the venue for design conversation too.

For security vulnerabilities, see [`SECURITY.md`](SECURITY.md) — please do not
open a public issue.

## Opening a pull request

The template asks for three things, and deliberately not for a checklist: CI
runs the gates and knows whether they passed. It asks for what no gate can work
out: what the change does, **why**, and whether it is a *decision* that needs a
record.

## Writing a decision record

A record states a rule as it holds now. It is `NNN-slug.md` in `docs/site/adr/`,
opens with the search front matter the others carry and `# ADR-NNN: Title`,
gives the rule first and then why it holds, in prose, and may end with a
`## Not chosen` list of `- **Alternative**: why not`.

- When the rule changes, change the record. How it came to change is in git.
- When a rule no longer holds, delete the record and every mention of its
  number; `TestEveryADRMentionHasARecord` fails on one left behind. A number is
  never used again.
- Say each thing once. A `Not chosen` entry gives a reason the body does not.
- Keep it short. Every sentence states a rule, a reason it holds, or what to do.
- After adding or deleting a record, run `task docs:adr-index`.

## Code of conduct

Participation is covered by the [Code of Conduct](CODE_OF_CONDUCT.md). In short:
argue about the code as much as you like, not about the person.
