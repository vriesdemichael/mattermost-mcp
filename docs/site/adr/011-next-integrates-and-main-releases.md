---
search:
  boost: 0.3
---

# ADR-011: Every change integrates on next, linearly, and only main releases

`next` is a permanent branch, and every change integrates there through a pull request merged by rebase: features, fixes and Dependabot's updates alike. The repository allows no merge commit and no squash, and the ruleset on next and main requires a pull request, a passing `CI Complete` and linear history, and refuses a force push. main moves only when next is promoted onto it, and only main releases (ADR-013). CI runs in full on both branches, live suite included, and its Release Flow job refuses every pull request into main.

A promotion is a fast-forward push of next to main, `git push origin origin/next:main`, made by an administrator after `task release:promote:check` confirms that main is an ancestor of next and that `CI Complete` passed on next's tip. The push bypasses the ruleset's pull request requirement, so the release workflow refuses to release a commit on which `CI Complete` has not succeeded. main takes no commit that next lacks, so the push is always a fast-forward and neither branch is rewritten.

Branch from next and open every pull request against it; retarget one opened against main. Bring a branch up to date by rebasing it onto next (`task pr:rebase`), never by merging next into it. A pull request branch is yours to rewrite as often as it helps, by amending, rebasing and force-pushing with lease; next and main are never rewritten. Do not add a release trigger to next or tag from it. When adding a branch to CI's triggers, add it to both the pull request and the push list.

GitHub closes an issue from a commit's closing keyword only on the default branch, main, so the keywords in commits on next take effect when next is promoted (ADR-017).

Every releasing commit on main cuts a version, so a change needs somewhere to wait: on next, a run of fixes and features becomes one release, and a set of breaking changes becomes one release with one migration note. A linear history reads, bisects and reverts one commit at a time, and the release takes its version and notes from the commits themselves.

## Not chosen

- **Release every merge from main**: A version per change is a rate nobody can follow, and a breaking change cannot wait for the others it belongs with.
- **Promote by pull request**: main allows only rebase merges, which replay every commit with a new SHA while next keeps the originals, leaving two copies of one history.
- **Squash merges**: One commit per pull request loses the types that decide the release, and a pull request that fixes one thing and adds another releases as one of them.
