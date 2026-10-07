---
search:
  boost: 0.3
---

# ADR-027: A difference between supported releases is found from Mattermost's own files, before a test runs

Every operation a tool calls is compared between the oldest and the newest supported release, from what each release's own repository holds at its tag (ADR-026): the route table its router registers, which says whether the release serves the operation at all, and the specification, which says which parameters and body fields it documents and the release each one arrived in. `apisurface.Differences` names every way the oldest release differs, and `TestEveryDifferenceBetweenSupportedReleasesIsHandled` fails a tool that calls an operation which differs and does not say, in its Use's Releases, how it handles that; or that says so where nothing differs; or whose difference the supported releases page does not list by operationId. A change of either stack's release refreshes the vendored files in the same change, so a difference a new release brings fails the pull request that adopts it, before any live test runs.

When the check names a difference, handle it as ADR-025 says, in the call that differs: ask the older release in the way it understands, or refuse before sending and name the release that can. Write what the tool does in Releases, add the operation to the supported releases page, and assert both sides in a live test, on both stacks. A difference the files cannot see, such as the same parameter meaning something else, is found by the live suite on the older stack and handled the same way.

The route table is the release's own code, so a route an older release lacks is certain, whatever its documentation says. The specification is the only record of individual parameters and of the release each arrived in, and is read for that alone. Found from files, a difference is known when the release is adopted, not when a user reports it.

## Not chosen

- **The specification alone**: It documents operations the router does not serve and misses some it does, and the 11.7 specification defines one path twice.
- **The live suite on the older release alone**: It catches what a test exercises, on the release it runs against, after the change is written; a parameter no test sends is never compared.
- **Diff Client4 between the two tags**: Client4 is versioned with the newest server and has no method for many parameters; the router and the specification are what each release serves and documents.
