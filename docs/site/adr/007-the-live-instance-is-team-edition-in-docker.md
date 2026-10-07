---
search:
  boost: 0.3
---

# ADR-007: Each checkout runs its own Mattermost Team Edition instances in Docker

The live suite runs against Mattermost Team Edition with PostgreSQL, started by Docker Compose from docker/latest for the newest release and docker/esr for the Extended Support Release (ADR-025). `task stack:up` starts one, `STACK=esr` choosing the other and `RELEASE=<x.y.z>` any other release from the latest stack's file, and tools/stack waits for it to answer and creates its system administrator. `task test:live` starts the instance before it runs, so a fresh clone needs only Docker.

Every checkout has its own instances, so worktrees never share one and one worktree's restart or reset never ends another's run. The main checkout's projects are mm-mcp-latest on port 8065 and mm-mcp-esr on 8066. A linked worktree's are named after it and a hash of its path, on ports Docker assigns; so is any release run beside them. Docker assigns a new port each time a stopped container starts, so `task stack:up` writes the instance's address to .tmp/stack-<stack>.env every time, and the live suite reads it from there; MM_LIVE_URL points it anywhere else. Containers and volumes carry the label dev.mm-mcp.worktree with the path of the checkout that started them. `task stack:up` first takes down other checkouts' stopped instances, which frees their networks, and removes the data of any whose worktree no longer exists; it never touches a running one, and refuses to start more than MM_STACK_MAX instances on one machine.

Team Edition is free and needs no licence, and this project pays for none, so every pull request, a fork's included, runs the live suite. A feature only the licensed editions have, such as guest accounts or LDAP groups, is not supported until it can be tested. The instance is configured through docker/mattermost.env, never by a test changing a server setting another test could observe. The administrator's credentials are well known on purpose, and the instance listens on the loopback address only.

Start and stop instances through the tasks, not with docker compose by hand, so names, ports, labels and the state file stay in step. When a test needs server configuration the stack does not have, change docker/mattermost.env for every test, and say why in the commit.

## Not chosen

- **One shared instance for every checkout**: A restart in one worktree ends another's live run, and a fixture one run removes is one another run is using.
- **A trial licence for Enterprise features**: It expires, it is a secret forks cannot have, and it buys features the people this serves mostly do not have.
- **The mattermost-preview image**: Simpler, but not what anyone deploys, and its tags do not track the Extended Support Release.
