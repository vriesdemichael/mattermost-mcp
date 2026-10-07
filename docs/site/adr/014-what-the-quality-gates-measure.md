---
search:
  boost: 0.3
---

# ADR-014: What the quality gates measure, and why each exists

Each gate answers a question no other one does:

- Patch coverage, over the unit and live profiles merged, blocks new untested code under cmd/ and internal/.
- Global coverage over the same tree catches erosion: a deleted test, or a refactor that drops covered paths, neither of which shows as an uncovered changed line.
- Tool reach, `TestEveryToolIsCalledByALiveTest`, requires every MCP tool to be called by a live test (ADR-004). It is binary per tool and says nothing about the arguments within one.
- The governance tests hold invariants over everything of one kind (ADR-015).
- gofmt, golangci-lint, govulncheck, line endings, the decision records, and the vendored specifications' releases are checked statically.

Coverage is a measurement, recomputed on every run into .tmp/ and published by CI to Codecov and as workflow artifacts, and never committed. tools/coverage merges the profiles and applies both thresholds without rerunning anything, and names the uncovered changed lines when the patch falls short. The thresholds live in .github/coverage-thresholds.env, which Taskfile.yml loads and the workflow reads, and the workflow takes a missing key as an error rather than a default. This record says what they mean, not what they are. tools/ is outside the coverage gate; its commands are tested anyway where they compute something, such as the release version and the coverage itself, because a bug there makes a release or a gate wrong quietly.

Every gate that needs no Mattermost runs in both places: in `task quality:verify` or the unit suite, which the git hooks run, and in a CI job other than the live one. The exception is govulncheck, `task quality:vulncheck`, which CI runs and `task quality:verify` does not: it fetches the vulnerability database, and its answer moves with the database rather than with the change, so in a hook it would refuse a push for a finding published that morning in a dependency the change never touched. Run it yourself when a change moves a dependency. CI reports the unit suite, the live suite, the coverage gates and Codecov as separate jobs, because they fail for unrelated reasons. Deliberately not measured: mutation testing, flake rate, dependency freshness and startup time.

Before adding a gate, say which question it answers that the others do not. Add one that needs no Mattermost to `task quality:verify` and as a step in a CI job outside the live one. Put a threshold in .github/coverage-thresholds.env, and do not lower one to make a change pass. When patch coverage fails, read the lines it names; if they cannot be reached, the code is wrong rather than the gate, so extract the decision into something a test can reach. Write a test to prove a guarantee, never only to move a number.

A gate that runs in one place only is checked by nobody, and one that runs only in a hook is advisory, because nothing stops a branch that skipped the hook. A threshold stated twice can disagree silently, and the failure is someone who believes a gate passed.

## Not chosen

- **Run the live gates in the pre-push hook**: They need Docker, and CI runs them on every pull request.
- **Codecov's status checks as the gate**: Its processing is asynchronous and has dropped uploads silently; the gate is computed in the workflow, and Codecov keeps history.
