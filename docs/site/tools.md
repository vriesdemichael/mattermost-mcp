# Tools

Every tool declares a title and the four MCP annotations: whether it only reads,
whether it may destroy something, whether repeating it changes nothing more, and
that it works in a closed domain, your one Mattermost server
([ADR-021](adr/021-read-only-by-default-and-every-write-asks.md)).

## Reading

`get_me`: Who am I
:   The Mattermost user the server acts as: id, username, name, nickname,
    position, roles, and whether it is a bot. Call it to check the connection and
    whose access the other tools use.

## Writing

None yet. Tools that write are offered only when `MM_MCP_ALLOW_WRITES` is true,
and each one asks you before it acts.
