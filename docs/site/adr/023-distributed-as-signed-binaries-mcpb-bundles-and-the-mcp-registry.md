---
search:
  boost: 0.3
---

# ADR-023: Distributed as signed binaries, .mcpb bundles, and through the MCP Registry

Each release publishes, on its GitHub release, a static mm-mcp binary for Linux, macOS and Windows on amd64 and arm64, each alone in an archive, with an SPDX SBOM; a .deb and an .rpm for each Linux architecture; a checksum manifest; and an .mcpb bundle per platform, which Claude Desktop installs with one click and which asks for the Mattermost address, the token and whether to allow writes. The archives, packages, bundles and the manifest are signed with keyless Sigstore bundles, and each binary's SBOM is attested against its archive and packages after tools/sbom-check has held it to the binary. The binaries are built reproducibly, with no symbol tables and an empty build id, and the Windows binary carries version information: a Go binary whose hash changes on every build, or that names nothing about itself, is what Defender's machine-learning heuristics and SmartScreen's reputation flag. server.json is published to the MCP Registry as `io.github.vriesdemichael/mm-mcp`, naming the bundles by their download address and checksum. No container image is published until there is a hosted HTTP deployment to run one (ADR-020).

The bundles and server.json carry the version 0.0.0 in the repository and are stamped by the release workflow (ADR-013), so a version is written in one place, the tag. CI builds the archives and the bundles on every pull request, and checks a bundle with the official mcpb tool.

Keep server.json's environment variables and the bundle's user configuration in step with the configuration page when a variable is added.

Homebrew, Scoop and WinGet are published once the release is public, never from the draft. tools/packages writes the formula for the vriesdemichael/homebrew-tap tap and the manifest for the vriesdemichael/scoop bucket from the release's checksum manifest, and winget-releaser opens the pull request to microsoft/winget-pkgs from a fork kept level with upstream. Each pushes with its own token, which the workflow gives only to the step that pushes, and a pull request writes the formula and the manifest from its snapshot so a release writes nothing a pull request has not. WinGet's first version is submitted by hand; the job publishes from the second on.

A static binary needs no runtime on the person's machine, and the bundle and the registry are where MCP clients look a server up. Signatures and attested SBOMs let an organisation that admits software through review check what it runs.

## Not chosen

- **One universal bundle**: A bundle holds one binary, and a person installs the one for their machine.
- **A container image now**: Nothing runs it yet, and an image is a second artifact to sign, scan and keep current.
- **GoReleaser's own Homebrew, Scoop and WinGet publishing**: It publishes in the run that drafts the release, before the SBOMs are checked, so a tap or a bucket could name a release that a later check refuses and nobody can download.
