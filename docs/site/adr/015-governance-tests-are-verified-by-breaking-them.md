---
search:
  boost: 0.3
---

# ADR-015: Governance tests are verified by breaking them

A governance test asserts an invariant over everything of one kind the repository holds, rather than a behaviour of one function, so that one added later is held to it without anyone remembering to. Each lives in a file named governance_test.go beside what it guards, and runs with the unit suite. Three rules govern them. Break a guard before trusting it. Prefer making a contradiction unrepresentable over testing for it: when two declarations answer the same question, derive one from the other. And keep the set listed here, which `TestTheGovernanceRecordListsExactlyTheGovernanceTests` holds in both directions. The set:

- `TestEveryToolDeclaresItsHintsAndTitle`: every MCP tool states a title and all four hints.
- `TestNoToolIsOpenWorld`: no MCP tool is annotated open-world.
- `TestAReadOnlyServerListsOnlyReadOnlyTools`: without writes allowed, no tool that writes is offered.
- `TestAllowingWritesAddsExactlyTheToolsThatWrite`: allowing writes adds exactly the tools not annotated read-only.
- `TestEveryToolIsCalledByALiveTest`: every MCP tool is called by name in tests/live.
- `TestTheToolsPageDocumentsEveryToolAndOnlyThose`: the tools page documents exactly the tools the server has.
- `TestTheRepositorysRecordsLoad`: every decision record parses, and no two share a number.
- `TestTheRecordIndexIsCurrent`: the record index is what the records generate.
- `TestEveryADRMentionHasARecord`: nothing in the repository names a record that does not exist.
- `TestNoRecordNamesAMattermostVersion`: no record restates which Mattermost releases are supported.
- `TestTheGovernanceRecordListsExactlyTheGovernanceTests`: this list and the governance tests agree.
- `TestEveryActionIsPinnedToACommit`: every workflow action is pinned to a release's commit, with the release named beside it.
- `TestEveryVariableTheSourceNamesIsListed`: every environment variable the shipped code names is in config.EnvironmentVariables, which the seal empties.
- `TestTheConfigurationPageNamesEveryVariableAndOnlyThose`: the configuration page documents exactly the variables mm-mcp reads.
- `TestEveryTestPackageIsSealed`: every package under cmd/ and internal/ with tests seals its process.

Break a governance test before adding it, and before trusting one you did not write: record what breaks it, and that you saw it fail. Add it to the list above in the same change. A guard that scans the tree also fails when it finds too little to scan, so a scan that has stopped matching cannot report perfect compliance. Do not write a test that compares a value to something derived from it.

A guard that has stopped guarding still runs, still passes and still occupies the slot. A tautological one reads correctly, because its only fault is that both sides come from the same place, and that is invisible until the sabotage is run.

## Not chosen

- **Keep the list in AGENTS.md**: Nothing verifies AGENTS.md. A list is worth having only if something checks it.
- **Trust review to catch a tautological guard**: It reads exactly like a real one; only running the sabotage tells them apart.
