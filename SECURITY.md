# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Use GitHub's [private vulnerability
reporting](https://github.com/vriesdemichael/mm-mcp/security/advisories/new)
on the Security tab. It keeps the report private until a fix is published, and
gives us somewhere to work on the fix and publish an advisory when it lands.

Please include:

- the version (`mm-mcp --version`) and how you run it: stdio or HTTP, which MCP
  client
- the tool involved, if any
- what an attacker can do with it, and what access they need first
- a reproduction if you have one

You do not need a proof-of-concept exploit. A clear description of the weakness
is enough.

## What to expect

This project is maintained by one person, so response times reflect that rather
than a staffed security team:

| | |
|---|---|
| Acknowledgement | within 5 working days |
| Initial assessment | within 10 working days |
| Fix or documented mitigation | depends on severity; you will be told which |

You will be credited in the advisory and release notes unless you ask not to be.
If a report turns out to be a non-issue you will get an explanation, not silence.

Please give a reasonable window to ship a fix before disclosing publicly. If you
hear nothing within the acknowledgement window, escalating publicly is fair.

## Supported versions

| Version | Supported |
|---|---|
| Latest release | Yes |
| Anything older | No — upgrade to the latest release |

There are no maintenance branches. A security fix ships in the next release.

## Scope

**In scope**

- The `mm-mcp` binary, its archives, the Linux packages and the `.mcpb` bundles
- Credential handling: how a token is read, sent, and kept out of logs, errors
  and tool results
- The write safeguards: a tool that writes being offered while writes are off,
  or acting without the person's confirmation
- Text from Mattermost being interpreted as anything but text, by a tool result
  or by a view
- The HTTP transport, including reaching it from beyond the machine
- The release pipeline and the artifacts it publishes

**Out of scope**

- The local test stacks under `docker/`. They are disposable instances with
  well-known credentials, listening on the loopback address. Their weaknesses
  are deliberate.
- Vulnerabilities in Mattermost itself. Report those to
  [Mattermost](https://mattermost.com/security-vulnerability-report/).
- An agent being talked into misusing a tool by text it read, when the tool
  behaved as documented: that is prompt injection, which the write confirmations
  exist to contain. A way around a confirmation is in scope.

## Verifying a release

Every archive, Linux package, bundle and the checksum manifest on a GitHub
release is signed with a keyless Sigstore bundle by the release workflow on
`main`, and each has a build provenance attestation. Every binary has an SPDX
SBOM, checked against the binary's own build information before the release is
published and attested against the archive and packages that hold it. With the GitHub CLI:

```bash
gh attestation verify mm-mcp_0.1.0_linux_amd64.tar.gz --repo vriesdemichael/mm-mcp
```

Or with cosign, against the signing identity:

```bash
cosign verify-blob --bundle sha256sums.txt.sigstore.json --certificate-identity 'https://github.com/vriesdemichael/mm-mcp/.github/workflows/release.yml@refs/heads/main' --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' sha256sums.txt
```

## Telemetry

mm-mcp sends no telemetry and makes no network calls other than to the
Mattermost server you configure.
