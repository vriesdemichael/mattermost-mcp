---
search:
  boost: 0.3
---

# ADR-010: Conventional Commits, and git hooks run by lefthook

Every commit subject is a Conventional Commit, `type(scope): description`, with `!` or a `BREAKING CHANGE:` footer marking a breaking change. The type decides whether a commit reaching main cuts a release, and how large (ADR-013), and the release notes are built from the subjects, so a wrong type ships a wrong version. No hook checks a subject; a reviewer does.

lefthook runs the git hooks configured in lefthook.yml: the unit tests before a commit, and before a push `task quality:verify` and the docs build. They are recommended rather than required, because CI runs the same gates on every pull request and CI is what refuses a change; a hook only tells you sooner. Install them with `lefthook install`. Do not skip a hook. A new gate that needs no Mattermost goes into `task quality:verify`, which the pre-push hook and CI both run (ADR-014).

## Not chosen

- **pre-commit, the Python framework**: A Python toolchain to install for a Go project, with versions pinned in its own configuration.
- **A commit-msg hook that validates subjects**: It runs only where it is installed, so it cannot be relied on, and a reviewer reads the subject anyway.
