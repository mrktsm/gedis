# Gedis implementation roadmap

Work is delivered in sequential, reviewable commits. A milestone is complete
only when its behavior is tested and its documentation describes the truth of
the repository at that commit.

## 1. Foundation

- Move executable behavior out of `cmd` and into focused internal packages.
- Introduce a configurable server with graceful shutdown.
- Add table-driven unit tests and GitHub Actions checks.
- Preserve existing behavior only where it agrees with the new contract.

**Exit:** packages have clear ownership and tests run on every push.

## 2. RESP2 and client compatibility

- Implement a bounded, binary-safe streaming RESP2 decoder.
- Implement all RESP2 response types required by the command subset.
- Support partial reads, multiple commands per read, and pipelining.
- Add `PING`, `ECHO`, and `COMMAND` discovery behavior needed by clients.

**Exit:** unmodified `redis-cli` can connect and pipeline commands.

## 3. Typed keyspace, strings, and expiration

- Implement `GET`, `SET`, `DEL`, `EXISTS`, `INCR`, `MGET`, and `MSET`.
- Support `SET` conditions and relative expiration options.
- Implement `EXPIRE`, `PEXPIRE`, `TTL`, `PTTL`, and `PERSIST`.
- Return Redis-compatible wrong-type, integer, and arity errors.

**Exit:** concurrent and fake-clock tests cover atomicity and expiration.

## 4. Sorted sets

- Replace command-specific globals with a keyspace-owned sorted-set value.
- Implement `ZADD`, `ZREM`, `ZSCORE`, `ZCARD`, `ZINCRBY`, and `ZRANGE`.
- Support rank ranges, `BYSCORE`, `REV`, `LIMIT`, and `WITHSCORES` in the
  documented subset.
- Preserve deterministic ordering for equal scores.

**Exit:** differential fixtures compare supported cases with Redis.

## 5. Append-only persistence

- Add disabled/always/everysec/no persistence policies.
- Replay valid commands at startup and detect truncated or corrupt tails.
- Add atomic AOF rewrite and recovery tests across process restarts.
- Expose persistence state through `INFO`.

**Exit:** acknowledged writes meet their documented durability policy.

## 6. Primary/replica synchronization

- Add replication IDs, byte offsets, and a bounded mutation backlog.
- Implement full synchronization and partial resynchronization.
- Make replicas reject writes while continuing to serve reads.
- Add disconnect, reconnect, and concurrent-write integration tests.

**Exit:** a restarted replica catches up without a full copy when its offset is
still present in the primary backlog.

## 7. Operational proof

- Add structured logs, `INFO`, connection metrics, and health checks.
- Add parser fuzzing and command benchmarks.
- Add Docker images and a Compose primary/replica demonstration.
- Publish a command compatibility table and reproducible benchmark method.

**Exit:** a new contributor can reproduce tests, a demo, and benchmark results
from the README without undocumented setup.

## Explicit non-goals for this roadmap

- Full Redis command coverage
- Redis Cluster or Sentinel compatibility
- Automatic failover or consensus
- Lua scripting, ACLs, Pub/Sub, Streams, or modules
- Claims of production readiness
