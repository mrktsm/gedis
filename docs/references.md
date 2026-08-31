# Engineering references

Gedis is implemented independently, but its behavior and architecture are
grounded in established specifications and open-source systems. These sources
are references, not vendored or copied implementations.

## Specifications

- [Redis serialization protocol](https://redis.io/docs/latest/develop/reference/protocol-spec/)
  defines RESP framing, request/response types, binary safety, and pipelining.
- [Redis command documentation](https://redis.io/docs/latest/commands/)
  is the behavioral source of truth for the supported command subset.
- [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
  motivates canonical RESP commands in the AOF, fsync policies, and rewrite.
- [Redis replication](https://redis.io/docs/latest/operate/oss_and_stack/management/replication/)
  motivates replication IDs, offsets, backlog-based partial synchronization,
  and full synchronization when history is unavailable.

## Implementations studied

- [redis/redis](https://github.com/redis/redis) is the compatibility reference.
  Gedis follows its externally documented semantics without copying internals.
- [tidwall/redcon](https://github.com/tidwall/redcon) demonstrates a compact Go
  boundary between a streaming RESP server and application command handling.
  Gedis deliberately implements its own protocol layer instead of importing it.
- [alicebob/miniredis](https://github.com/alicebob/miniredis) demonstrates the
  value of deterministic, command-focused compatibility tests in pure Go.
- [EchoVault/SugarDB](https://github.com/EchoVault/SugarDB) is a Go example of
  separating an embeddable keyspace, RESP service, persistence, and replication.
- [microsoft/Garnet](https://github.com/microsoft/Garnet) uses a narrow storage
  interface beneath a broad RESP API and documents compatibility explicitly.
- [dragonflydb/dragonfly](https://github.com/dragonflydb/dragonfly) demonstrates
  how an alternative implementation can preserve Redis APIs while clearly
  documenting different concurrency and performance architecture.

## Reference policy

When behavior is ambiguous, add a test against a real Redis instance and record
the supported result in the compatibility matrix. Avoid copying source code.
Record any adapted algorithm, format, or non-obvious design in this file with a
link and the applicable license before merging it.
