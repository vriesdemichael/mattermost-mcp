---
search:
  boost: 0.3
---

# ADR-008: A live test seeds and owns its fixtures, and names them at random

Every live test seeds the state it needs and owns it. The helpers in tests/live/harness_test.go, such as `seedUser` and `seedBot`, create a user, a bot, and in time a team and channels of the test's own through an administrator's Client4, and register their removal with t.Cleanup: a user is deactivated and a bot disabled, because permanent deletion is off by default. No test reads another's fixtures or depends on the order tests run in, so every live test calls t.Parallel and the suite runs as many at once as LIVE_PARALLEL in Taskfile.yml says. A test that asserts on what the whole instance shares, such as a search, looks for its own fixtures by their unique names or content.

A fixture's name is the prefix `lt-`, the kind of fixture, and a random part from `uniqueName`, which draws from crypto/rand: never a timestamp, a timestamp with a counter beside it, or a timestamp cut short. The prefix traces a fixture left behind to the suite, and `task stack:reset` deletes the instance's data when leftovers build up.

When a test needs a new kind of state, add a helper that creates it and registers its removal. Do not seed shared state once for the suite. Mattermost indexes search asynchronously, so a test that searches for what it just posted waits with a bounded poll that fails with what it saw, never with a fixed sleep.

Shared state makes a failure depend on what ran before it, which is the expensive kind to debug, and it stops tests running at the same time. Clock-derived names collide: the clock is coarser than the suite is parallel, and a counter restarts with the process, so a run collides with what a crashed run left behind.

## Not chosen

- **Clean the instance before every run**: It depends on a teardown that a crash is what prevents, and does nothing about two tests colliding inside one run.
- **One seeded team and channel set for the whole suite**: Every test that writes changes what the others read.
