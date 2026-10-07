# The live-test instances

Mattermost Team Edition with PostgreSQL, for the live suite
([ADR-007](../docs/site/adr/007-the-live-instance-is-team-edition-in-docker.md)).
`latest/` runs the newest release, `esr/` the Extended Support Release
([ADR-025](../docs/site/adr/025-supported-mattermost-releases.md)).

```bash
task stack:up
task stack:up STACK=esr
task stack:up RELEASE=11.9.2
```

`task stack:up` starts the containers through `tools/stack`, waits for
Mattermost to answer, creates the system administrator the live suite logs in as
(`sysadmin`, with the password in `internal/teststack`), and writes the
instance's address to `.tmp/stack-<stack>.env`, where the live suite reads it.
A fresh instance is ready in about fifteen seconds, a stopped one in about ten.

## One instance per checkout

Each checkout has its own instances, so worktrees never share one:

| Checkout | Project | Address |
|---|---|---|
| the main checkout | `mm-mcp-latest`, `mm-mcp-esr` | `localhost:8065`, `localhost:8066` |
| a linked worktree | `mm-mcp-<worktree>-<hash>-<stack>` | a port Docker assigns |
| any `RELEASE=` | `...-release-<x-y-z>` | a port Docker assigns |

Containers and volumes are labelled with the checkout that started them.
`task stack:up` first takes down other checkouts' stopped instances and removes
the data of any whose worktree no longer exists; it never touches a running
one, and starts at most `MM_STACK_MAX` (default 6) on one machine.

| Task | Does |
|---|---|
| `stack:up` | start, wait, bootstrap; safe to run again |
| `stack:down` | stop, keeping the data |
| `stack:reset` | delete the data and start again |
| `stack:status` | this checkout's instance, and every instance on the machine |
| `stack:logs` | the last 200 lines of Mattermost's log |
| `stack:prune` | take down stopped instances and remove deleted worktrees' data |

Every task takes `STACK=esr` or `RELEASE=x.y.z` to act on another instance.
Settings both stacks share are in `mattermost.env`. Each compose file names its
own image so Dependabot can propose each one's updates on its own terms.

These instances are disposable and deliberately weak: well-known credentials,
rate limiting off, listening on the loopback address. Do not expose them.
