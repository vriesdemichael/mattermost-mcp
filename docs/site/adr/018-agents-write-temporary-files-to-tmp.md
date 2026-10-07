---
search:
  boost: 0.3
---

# ADR-018: Agents and tasks write temporary files to .tmp

An agent writes its temporary files, such as downloads, response dumps, scratch output and intermediate artifacts, to `.tmp/` at the repository root and nowhere else: not a system temp directory, not the desktop, not a tracked source directory. The tasks write their intermediate files there too: coverage data, the strict docs build and the smoke-test environment. Create `.tmp/` if it is missing, clear out what you no longer need, and keep `.tmp/.gitkeep`. A test is different: it works in a directory it created, t.TempDir().

.gitignore ignores everything in `.tmp/` except `.gitkeep`, so nothing written there is committed by accident. A directory inside the repository is one an agent's sandbox lets it write, and one a person can find.
