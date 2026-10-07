---
search:
  boost: 0.3
---

# ADR-006: A unit test inherits nothing and reaches nothing beyond the machine

Every package under cmd/ and internal/ that has tests seals its process in TestMain with `testsupport.SealedMain`, which empties every variable mm-mcp reads, listed in `config.EnvironmentVariables`, and sets `MM_MCP_BLOCK_EXTERNAL_NETWORK` to 1. While that is set, `network.NewSafeTransport`, the transport every HTTP client in mm-mcp is built on, refuses any host but localhost, 127.0.0.1 and ::1, and the error names the host it refused. `TestEveryTestPackageIsSealed` fails a package that has tests and no seal, and `TestEveryVariableTheSourceNamesIsListed` fails when the code names a variable the list, and so the seal, leaves out.

Build every HTTP client on `network.NewSafeTransport`. Read configuration through a function that takes a Getenv, as `config.FromEnv` and `cli.Run` do, and have a test pass the values it wants with `testsupport.Env` rather than publish them to the process; t.Setenv is for a test whose subject is the environment, and rules out t.Parallel. Add a variable mm-mcp reads to `config.EnvironmentVariables` and to the configuration page in the same change.

Unsealed, the machine decides what a test sees: a developer with `MM_URL` and `MM_TOKEN` in their shell runs different inputs than a clean runner, and a green run on one is no evidence about the other. An unintended network call makes a suite slow and flaky, fails on an isolated runner, and lets code under test act on a real server with a real token.

## Not chosen

- **Clear the environment test by test**: Every test has to remember to, the one that forgets reads the developer's token, and t.Setenv makes a test sequential.
- **Block the network with a mocking library**: It covers the clients it is told about, not one added later.
