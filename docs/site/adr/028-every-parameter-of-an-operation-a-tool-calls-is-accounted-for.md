---
search:
  boost: 0.3
---

# ADR-028: Every parameter of an operation a tool calls is accounted for, and the live suite proves the tool calls what it declares

A tool declares, in its spec's Uses, every Mattermost operation it calls, by its operationId in the newest release's specification, and for each one says what it does with every parameter the operation has: the path, query and header parameters, and every field of its JSON body, written body.<field>. A parameter is set by one of the tool's arguments (`SetBy`), always sent as a fixed value with a reason (`Fixed`), or never sent with a reason (`Omitted`). `TestEveryParameterOfACalledOperationIsAccountedFor` fails on a parameter left unsaid or one the operation does not have; `TestEveryToolArgumentSetsAParameterItCalls` fails on an argument that sets nothing the tool declares, and on a declared argument the tool does not take; `TestEveryToolDeclaresTheOperationsItCalls` fails on an operation the newest specification lacks or its router does not serve. The live suite records every request a tool sends while it is called, matches each to its operation, and fails a call that reaches an operation its tool does not declare, and a suite that finishes without having seen a declared operation called.

When adding a tool or a parameter, read the operation in openapi/mattermost-latest.json, decide each parameter, and write the decision with its reason. An omission is a decision, and the reason is what a reviewer reads: say what a user would lose and why that is right, not that it is unneeded. When a release adds a parameter, the check fails the change that adopts the release, and the parameter is decided then.

A tool that quietly ignores a parameter does less than the person asking expects, and nobody can tell from the outside. Declaring each one turns "does this tool do everything the endpoint can" into a list a reviewer reads and a check keeps complete. A declaration nobody verifies drifts from the code, so the live suite checks it against the requests the tool really sends.

## Not chosen

- **Expose every parameter**: Many are for administrators, paging internals or the web app's own needs, and every argument is one more thing the model must decide; an omission with a reason is the better tool.
- **Declare only the parameters a tool sets**: A parameter added by a new release, or one nobody considered, would go unnoticed.
- **Derive the operations from the code instead of declaring them**: Client4 builds its URLs at run time, so only the live suite can see which operations a tool reaches; it checks the declaration rather than replacing it.
