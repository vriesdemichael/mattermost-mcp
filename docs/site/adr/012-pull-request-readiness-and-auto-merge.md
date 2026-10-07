---
search:
  boost: 0.3
---

# ADR-012: Pull request readiness, and what may merge itself

A pull request is opened when it is complete and reviewable: no partial implementation, no TODO, FIXME or debugging left in, `task quality:verify`, `task test:unit` and `task docs:validate` passing, and the live tests that cover what it changes run against the local stack. CI runs the rest.

A pull request into next that changes anything a user or an MCP client meets waits for a maintainer to merge it: tools, their names, descriptions, arguments, annotations and results; views; configuration, environment variables and the command line; error messages; what the packages install; the docs pages, the README and the decision records. A pull request that mixes the two kinds is of this kind. Any other pull request into next, to tests, CI, the local stack or internal code with no visible effect, may merge itself by rebase auto-merge once `CI Complete` passes. Resolve its review comments before turning auto-merge on.

Dependabot's minor and patch updates merge themselves through .github/workflows/dependabot-automerge.yml once CI passes. A major update, an update proposed against a branch other than next, and any update of a Mattermost image wait for a person: a Mattermost image names a release mm-mcp claims to support (ADR-025), and a green suite is the evidence that a release is adoptable, not the decision to adopt it.

A maintainer reviews what users see. Everything else lands on next, which releases nothing, and is looked at as a whole when next is promoted.

## Not chosen

- **Every pull request waits for a maintainer**: Dependency updates and test changes would queue behind a single person, and stale dependencies are their own risk.
- **Every pull request merges itself once green**: A tool's description is what a model reads to decide what to call; no gate judges whether it reads well.
