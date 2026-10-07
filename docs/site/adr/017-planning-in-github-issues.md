---
search:
  boost: 0.3
---

# ADR-017: Planning lives in GitHub issues, and an issue closes when its fix reaches main

Work that outlives one branch or one session is tracked in a GitHub issue: features, efforts of several steps, known bugs, technical debt and open design questions. The issues are the project's plan; local notes and conversation context are not. A large effort is one issue with a checklist or sub-issues. Start a session by reading the open issues that bear on the task. An agent asks before opening an issue, and fixes a problem it meets mid-task on the branch in hand.

An issue closes when its fix reaches main, not when the fix merges to next. A fix merged to next leaves its issue open with the label "staged on next" and one comment naming the pull request. The closing keyword goes in the body of the commit that fixes the issue, one keyword per issue (`Closes #1, closes #2`). GitHub acts on a keyword only when its commit reaches the default branch, main, and main moves only when next is promoted onto it (ADR-011), so the keyword closes the issue at that promotion. The same push runs the release workflow, which cuts a release when the promoted commits call for one (ADR-013): a fix a user can observe is typed to release, so the promotion that closes its issue also releases it, and an issue settled by a change that cuts no release, such as documentation, closes with nothing new to install.

A session ends and its context goes with it; issues are shared, searchable and reviewable. To a user, a closed issue reads as fixed in what main publishes, which is false while the fix waits on next.

## Not chosen

- **Plans in local files**: Nobody else sees them, and they drift from one session to the next.
- **Close on merge**: A reporter reads the issue, not the branch, and opens a duplicate when the bug is still in the release they run.
