# Redis conformance log

This document records the evidence behind Gedis compatibility claims. It keeps
three things separate: behavior observed from Redis, automated Gedis fixtures,
and features that are not implemented yet.

## Baseline and method

- Compatibility baseline: Redis Open Source 7.2.7.
- Client used for black-box probes: `redis-cli 7.2.7`.
- Probe date: 2026-08-31.
- Protocol baseline: RESP2, using unmodified `redis-cli` over TCP.
- Implementation policy: reproduce documented and externally observed behavior
  with original Go code; do not translate or copy Redis source.

Manual probes were run against an isolated local Redis process. Non-obvious
results were then captured in deterministic tests under `internal/server` or
`internal/store`. The regular suite does not require Redis to be installed.

## Verified behavior

| Area | Redis 7.2.7 observation | Gedis regression evidence |
| --- | --- | --- |
| RESP2 | Fragmented frames, pipelined arrays, binary-safe bulk strings, malformed and truncated input | `internal/resp/reader_test.go`, `internal/resp/writer_test.go` |
| TCP | Multiple pipelined commands return ordered responses on one connection; `QUIT` closes after `+OK` | `internal/server/server_test.go` |
| `SET` | `NX`, `XX`, and `GET` return the previous value or null according to whether the condition applied | `internal/server/strings_test.go` |
| Expiration | Missing TTL is `-2`, persistent TTL is `-1`, `PTTL` preserves milliseconds, and `TTL` rounds 1500 ms to 2 seconds | `internal/server/strings_test.go`, `internal/store/expiration_test.go` |
| Integer strings | Missing counters begin at zero; invalid values and overflow return Redis-style integer errors | `internal/server/strings_test.go`, `internal/store/keyspace_test.go` |
| `ZADD` | Duplicate members in one command are applied in input order; `CH` counts each effective add/update; incompatible options use Redis error text | `internal/server/sorted_sets_test.go`, `internal/store/sorted_set_test.go` |
| `ZRANGE` | Rank and score ranges, reverse ordering, exclusive bounds, `LIMIT`, and `WITHSCORES` match the probed subset | `internal/server/sorted_sets_test.go` |
| Wrong types | String operations on sorted sets and sorted-set operations on strings return `WRONGTYPE` | `internal/server/strings_test.go`, `internal/server/sorted_sets_test.go` |
| AOF format | Mutations are canonical RESP arrays; relative TTLs are persisted as absolute millisecond deadlines | `internal/aof/log_test.go`, `internal/server/persistence_test.go` |

The manual command probes covered `SET`, `GET`, `DEL`, `EXISTS`, `INCR`,
`INCRBY`, `MGET`, `MSET`, `TYPE`, `EXPIRE`, `PEXPIRE`, `TTL`, `PTTL`,
`PERSIST`, `ZADD`, `ZREM`, `ZSCORE`, `ZCARD`, `ZINCRBY`, `ZRANGE`, `ZRANK`,
and `ZREVRANK`, including success, missing-key, wrong-type, option-conflict,
and malformed-number cases used by the fixtures above.

## Supported subset

Gedis intentionally does not claim full Redis compatibility. The currently
supported data types are strings and sorted sets. Transactions, Lua/functions,
streams, pub/sub, ACLs, cluster mode, eviction policies, RESP3, and the broader
Redis command surface are outside the present subset.

AOF encoding, replay, truncation detection, fsync policies, and engine write
ordering are implemented as libraries. Server startup recovery, atomic rewrite,
and replication are still roadmap work and must not be presented as shipped
features until their integration and restart tests land.

## Adding compatibility evidence

For each new command or option:

1. Link the official command specification and a pinned Redis source file when
   source-level design affects the implementation.
2. Probe ambiguous success, error, missing-key, and wrong-type cases against the
   baseline Redis version.
3. Add deterministic fixtures that do not require a running Redis instance.
4. Add a `redis-cli` end-to-end check when networking or wire output changes.
5. Update this log and the supported subset without overstating compatibility.
