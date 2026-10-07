---
search:
  boost: 0.3
---

# ADR-016: Versioned docs with MkDocs Material and mike

The documentation site is MkDocs with the Material theme, built from docs/site and published to GitHub Pages with mike. CI builds it strictly on every pull request (`task docs:validate`), and the release workflow publishes it with every release, so the site changes only when a release is cut.

The site is versioned by line. From 1.0.0 a line is a major version; before it, while a minor version may break, a line is a minor version, v0.3. A release deploys to its line, replacing that line's previous build, with its full version as the entry's title and as an alias, so a link naming a release resolves to the newest build of its line. The `latest` alias follows the newest line and is the default. `task docs:deploy-version` decides the line, and the release workflow runs it.

Build, serve and deploy through the docs tasks. The docs tooling is the uv project in docs/, locked by docs/uv.lock (ADR-002). A page that documents a list the code holds, such as the environment variables, is checked against the code by a governance test (ADR-015).

MkDocs Material is maintained and asks little upkeep of a repository that is otherwise Go, and mike gives versioned docs that follow release tags. Within a line, a newer release's docs describe everything a reader of an older one can use, so one build per line loses nothing a reader needs.

## Not chosen

- **README-only documentation**: Does not scale to navigable, versioned docs.
- **A build per release**: The version selector grows past anything a reader can pick from.
