---
search:
  boost: 0.3
---

# ADR-002: Every workflow is a task, and every tool's version is pinned in one place

Every workflow in this repository is a task in Taskfile.yml, run as `task <name>`, and CI runs the same tasks a developer does rather than copies of their steps. The Go toolchain is pinned by the toolchain directive in go.mod, and Go fetches it when the installed one is older. Tools that are not Go module dependencies are pinned in .github/tool-versions.env, which the Taskfile loads and CI reads: golangci-lint, govulncheck, GoReleaser, Task itself, the MCP Registry's publisher, Komac, the mcpb tool and the Node that runs it among them. CI installs a tool that publishes binaries from its release, held to the release's checksums, rather than building it: .github/actions/setup-task puts Task on the PATH in seconds, where `go install` took a minute a job. `TestEveryToolVersionIsPinnedInOnePlace` refuses a version a workflow or the Taskfile states itself (ADR-015). The documentation site's Python tooling is a uv project in docs/, locked by docs/uv.lock. A contributor installs Go, Task, Docker and git; lefthook and uv are recommended.

Add a workflow as a task and have CI call the task. Pin a new tool in .github/tool-versions.env, never in a workflow alone. Write logic a task needs in Go under tools/, one directory per command, when it has a purpose no existing tool serves; the reason for each is in its package comment. Keep a task's commands to what Task's built-in shell runs on every platform; a task that needs bash says so in its description.

A version stated in two places can disagree, and the disagreement shows as a check that passes locally and fails in CI. Task is cross-platform and declarative, and Go tools under tools/ run anywhere the module builds.

## Not chosen

- **Make**: Not installed on Windows by default, and its recipes run in whichever shell the platform has.
- **Shell scripts for the tooling**: They do not run the same on Windows; tools/ in Go does.
