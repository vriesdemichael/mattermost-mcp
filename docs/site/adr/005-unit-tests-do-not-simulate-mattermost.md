---
search:
  boost: 0.3
---

# ADR-005: Unit tests do not simulate Mattermost

A unit test does not stand a fake Mattermost in front of the code under test. Anything whose outcome depends on how the server behaves, such as which route it serves, what it accepts, what it returns, which status it answers with and what an omitted field means, is proven in the live suite (ADR-004) or it is not proven. Unit tests keep what needs no server: configuration and argument parsing, validation, formatting, logic over data held in memory, the reading of an error already in hand, the annotations and schemas a tool declares, and assertions that the code refuses input before any request is made.

A stand-in stays only where it claims nothing about Mattermost: a round tripper whose subject is the request itself, such as which host it went to; a fault below the API, such as a refused connection; or one that fails the test when it is called, proving no request was made. An httptest.Server that answers a Mattermost route with a Mattermost-shaped body, or a fake of internal/mattermost that returns Mattermost data, is what this record rules out.

When a fix needs a test, write it live first, and reach for a unit test only when no request is involved. If an existing stand-in has to be edited to let a correct change land, it was asserting a wrong belief: delete it and cover the behaviour live.

A mock encodes its author's belief about the API, so when that belief is the defect the mock agrees with the code and the test passes. A mock correct today and wrong after a Mattermost upgrade fails no differently from a right one; only a real server tells them apart.

## Not chosen

- **Keep mocks, held to a stricter standard**: It asks a reviewer to spot an assumption that looks correct by construction, and leaves the drift between releases untouched.
- **Mock through Client4's own types**: Real types make a fake look authoritative; the answer it gives is still its author's belief.
