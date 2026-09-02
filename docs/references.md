# Engineering references

Gedis is an independent Go implementation whose observable behavior and
architecture are grounded in specifications and production open-source
systems. These sources are references, not vendored or copied implementations.
Versioned links are used for source-level references so later upstream changes
cannot silently change the design record.

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

- [Redis 7.2.7 `networking.c`](https://github.com/redis/redis/blob/7.2.7/src/networking.c)
  is a source-level reference for request processing and ordered client output.
- [Redis 7.2.7 `t_zset.c`](https://github.com/redis/redis/blob/7.2.7/src/t_zset.c)
  is the source-level reference for the dictionary-plus-indexed-skip-list
  sorted-set design, score/member ordering, ranks, and range behavior.
- [Redis 7.2.7 `aof.c`](https://github.com/redis/redis/blob/7.2.7/src/aof.c)
  is the source-level reference for RESP command logging, fsync policies,
  absolute expiration propagation, replay, and atomic rewrite replacement.
- [Redis 7.2.7 `replication.c`](https://github.com/redis/redis/blob/7.2.7/src/replication.c)
  is the source-level reference for replication IDs, logical byte offsets,
  backlog history, and partial versus full synchronization.
- [Redis 7.2.7 `server.c`](https://github.com/redis/redis/blob/7.2.7/src/server.c)
  is the pinned source reference for `INFO` section framing and the replication,
  persistence, client, and statistics field layout.
- [tidwall/redcon v1.6.2](https://github.com/tidwall/redcon/tree/v1.6.2)
  demonstrates a compact Go boundary between a streaming RESP server and
  application command handling. Gedis implements its own protocol layer rather
  than importing Redcon.
- [alicebob/miniredis](https://github.com/alicebob/miniredis) demonstrates the
  value of deterministic, command-focused compatibility tests in pure Go.
- [EchoVault/SugarDB](https://github.com/EchoVault/SugarDB) is a Go example of
  separating an embeddable keyspace, RESP service, persistence, and replication.
- [microsoft/Garnet](https://github.com/microsoft/Garnet) uses a narrow storage
  interface beneath a broad RESP API and documents compatibility explicitly.
- [dragonflydb/dragonfly](https://github.com/dragonflydb/dragonfly) demonstrates
  how an alternative implementation can preserve Redis APIs while clearly
  documenting different concurrency and performance architecture.

## Build and container references

- [Docker multi-stage build documentation](https://docs.docker.com/build/building/multi-stage/)
  is the basis for separating the Go build/test environment from the minimal
  runtime image.
- [Docker Official Image for Go](https://hub.docker.com/_/golang) supplies the
  pinned `golang:1.24.13-alpine3.23` builder and documents dependency-first
  module caching.
- [Redis Docker Official Image packaging](https://github.com/redis/docker-library-redis)
  is the operational reference for a dedicated data directory and
  Redis-compatible container conventions.

## Reference policy

When behavior is ambiguous, probe the pinned Redis baseline, turn the observed
result into an automated fixture, and record it in the
[conformance log](conformance.md). Do not copy source code. Record any adapted
algorithm, on-disk format, or non-obvious design here with its versioned source
and applicable license before merging it.

The indexed skip list follows the data-structure design documented in Redis,
but its Go implementation, tests, and API were written for Gedis. The pinned
Redis 7.2.7 source is [BSD 3-Clause licensed](https://github.com/redis/redis/blob/7.2.7/COPYING);
Redcon and Miniredis are MIT licensed. No upstream source file is included in
this repository.
