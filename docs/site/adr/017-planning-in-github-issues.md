---
search:
  boost: 0.3
---

# ADR-017: Planning lives in GitHub issues, and an issue closes when its fix ships

Work that outlives one branch or one session is tracked in a GitHub issue: features, efforts of several steps, known bugs, technical debt and open design questions. The issues are the project's plan; local notes and conversation context are not. A large effort is one issue with a checklist or sub-issues. Start a session by reading the open issues that bear on the task. An agent asks before opening an issue, and fixes a problem it meets mid-task on the branch in hand.

An issue closes when the release carrying its fix is published, not when the fix merges to next. A fix merged to next leaves its issue open with the label "staged on next" and one comment naming the pull request. The closing keyword goes in the body of the commit that fixes the issue, one keyword per issue (`Closes #1, closes #2`), because main takes rebase merges and GitHub closes nothing from a branch that is not the default, so the keyword closes the issue when next reaches main.

A session ends and its context goes with it; issues are shared, searchable and reviewable. To a user, a closed issue reads as fixed in a version they can install, which is false while the fix waits on next.

## Not chosen

- **Plans in local files**: Nobody else sees them, and they drift from one session to the next.
- **Close on merge**: A reporter reads the issue, not the branch, and opens a duplicate when the bug is still in the release they run.
