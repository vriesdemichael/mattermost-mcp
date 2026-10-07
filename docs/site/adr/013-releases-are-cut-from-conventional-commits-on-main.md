---
search:
  boost: 0.3
---

# ADR-013: Releases are cut from Conventional Commits on main

A push to main runs .github/workflows/release.yml, which reads the commits since the last release tag and cuts a release when one of them calls for it. From 1.0.0 a breaking change cuts a major, `feat` a minor, and `fix`, `perf` and `revert` a patch. Before 1.0.0, while the major version is 0, a breaking change cuts a minor and everything else that releases cuts a patch, and the first release is v0.1.0. Every other type cuts no release, and its commits ship with the next one that does. That reading lives in tools/release, which decides the version and writes the notes from the same parse, so the two cannot disagree about what broke.

The version a binary reports is injected when it is built, with `-ldflags -X` into internal/version; a build that was not given one reports "dev". The bundles and server.json carry 0.0.0 in the repository and are stamped by the workflow, so a version is written in one place, the tag. The workflow writes the notes from the commits, under a hand-written introduction when docs/release-notes/<version>.md exists, and tags the commit. GoReleaser, configured in .goreleaser.yaml, then builds every platform's binary, archives it alone, scans it into an SPDX SBOM, packages the Linux binaries as .deb and .rpm, writes the checksum manifest, signs all of it with keyless Sigstore bundles, and creates the GitHub release as a draft with those notes. What GoReleaser has no notion of is this project's own, and runs before the draft is made public: tools/sbom-check holds every SBOM to the build information the Go linker wrote into its binary and refuses the release on any difference, tools/mcpb packs the .mcpb bundles from GoReleaser's binaries, and the workflow signs and uploads them and attests the build provenance of every archive, package and bundle. Then the release is made public, each archive and package is attested with its binary's SBOM, server.json is published to the MCP Registry, and the docs are published (ADR-016). A check that fails leaves a draft nobody can install. CI runs the same build on every pull request through `task release:snapshot`, with the SBOMs and their check, so a release builds nothing a pull request has not. A repeated run is safe: a tag already on the commit is kept and an existing release is updated, and a version that another run tagged on a different commit stops this one.

Releases run from main only, so the signing identity is one workflow on one branch, and only the jobs that sign, attest or publish may mint an OIDC token.

Type a commit by what a user sees. A change no user can observe, to tooling, CI, tests or documentation, is `ci`, `chore`, `test`, `refactor` or `docs`, and cuts no release. Mark a change breaking when it would break a configuration that works today: a tool renamed or removed, an argument renamed or made required, a result's shape changed, an environment variable renamed, or a call that succeeded now refused.

## Not chosen

- **Release only by a manual run**: Release timing comes to depend on someone remembering.
- **Let GoReleaser decide the version and write the notes**: It reads the version from a tag someone has pushed and the notes from its own changelog rules; here the commits decide both, in one parse (tools/release).
- **Build, archive and sign by hand in the workflow**: Every platform, archive format, checksum and signature is configuration GoReleaser already has, and a script repeating it is one more thing to keep right.
