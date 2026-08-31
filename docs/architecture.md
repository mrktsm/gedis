# Gedis architecture

## Goal

Gedis is a learning-oriented, Redis-compatible in-memory database written in
Go. Its purpose is to demonstrate the internals of a networked data system:
streaming protocol parsing, concurrent command execution, typed storage,
expiration, durability, and primary/replica synchronization.

Compatibility means that an unmodified Redis client can use the documented
subset of commands. It does not mean that Gedis implements every Redis command
or that it should be used as a production replacement for Redis.

## Design principles

1. **Compatibility is tested, not claimed.** Every supported command gets
   protocol-level tests for its replies, errors, and edge cases.
2. **Own the important machinery.** RESP parsing, command dispatch, storage,
   expiration, persistence, and replication are implemented in this repository.
3. **Keep boundaries explicit.** Networking must not know storage details, and
   storage must not know how RESP is encoded.
4. **Make correctness observable.** Race detection, fuzzing, recovery tests,
   replication tests, and reproducible benchmarks are part of the product.
5. **Prefer a deep subset over a shallow clone.** Unsupported behavior is
   documented in a compatibility matrix instead of being approximated.

## Target package layout

```text
cmd/gedis/            CLI entry point and process lifecycle
internal/config/      Flags, defaults, and configuration validation
internal/resp/        Streaming RESP2 decoder and response encoder
internal/server/      Listener, connections, dispatch, and command registry
internal/store/       Typed keyspace, strings, sorted sets, and expiration
internal/aof/         Append-only persistence, recovery, and rewrite
internal/replication/ Primary/replica synchronization and backlog
tests/integration/    Black-box protocol and multi-node tests
```

The `internal` boundary is intentional: the first supported product surface is
the Gedis server, not a prematurely stabilized embeddable library API.

## Request path

```text
TCP stream
  -> RESP2 decoder
  -> command lookup and argument validation
  -> engine execution against the typed keyspace
  -> mutation stream (AOF and connected replicas)
  -> RESP2 encoder
  -> TCP stream
```

Each connection is handled by one goroutine. Commands on a connection execute
in input order, which naturally supports Redis pipelining. The shared keyspace
owns synchronization for command atomicity; command handlers do not reach into
maps or locks directly.

## RESP2 boundary

The initial protocol supports arrays, bulk strings, simple strings, errors,
integers, and null bulk strings. Client commands are arrays of bulk strings.
The decoder is binary-safe, reads incrementally from a buffered stream, limits
aggregate and bulk sizes, and rejects malformed lengths and terminators.

RESP3, inline commands, Pub/Sub push mode, and cluster-bus traffic are outside
the first compatibility target.

## Storage and expiration

The keyspace stores a tagged value so one key cannot silently be both a string
and a sorted set. A single command observes expiration before accessing a key.
Expiration combines:

- lazy deletion when a key is accessed; and
- active deletion driven by a deadline heap in a background goroutine.

A replace operation receives a new expiration generation so stale heap entries
cannot delete a newer value.

## Durability

The append-only file stores successful mutating commands as canonical RESP2
arrays. Reusing the wire format makes the log inspectable and lets recovery use
the same command validation and execution path as live traffic.

The implemented policies are `always`, `everysec`, and `no`, matching Redis's
broad durability trade-off. Recovery runs before the listener opens and rejects
corruption. An incomplete final command can be truncated only through an
explicit operator flag.

`BGREWRITEAOF` snapshots live keys into a deterministic minimum command
sequence, fsyncs a same-directory temporary file, renames it over the old log,
fsyncs the directory entry, and resumes appending through the replacement file.
The job runs in a goroutine, but this implementation deliberately uses the
engine write gate rather than Redis's process fork and copy-on-write machinery:
writes pause during serialization, while reads continue after the snapshot copy
releases the keyspace lock.

## Replication

The implemented primary assigns a random 40-hex-character replication ID and a
monotonically increasing byte offset to its canonical mutation stream. It
retains a bounded byte backlog so a replica can request partial synchronization
by replication ID and next-byte offset on the normal server port. Successful
requests receive Redis-shaped `+CONTINUE`; unavailable history receives
`+FULLRESYNC` and a length-prefixed snapshot before live commands.

The full-sync payload is deliberately canonical RESP reconstruction commands,
not an RDB file. Its transfer framing follows Redis, but Redis replicas cannot
load the Gedis-specific payload. This boundary remains explicit until an RDB
codec exists. An unmodified `redis-cli` can negotiate the stream and inspect
incremental commands.

When the requested history is unavailable, the primary sends a point-in-time
snapshot followed by mutations that occurred while the snapshot was produced.
A Gedis replica completes `PING`, `REPLCONF`, and `PSYNC` negotiation before it
accepts client traffic. Full state is decoded into a temporary keyspace and
installed atomically; later commands pass through the same engine mutation gate
and AOF as local traffic while bypassing only the client `READONLY` policy. A
running replica retains its ID and offset across reconnects and requests only
missing backlog bytes.

Replication ID/offset metadata is not yet durable across a replica process
restart, which currently forces another full sync after local AOF recovery.
Automatic leader election, sharding, Redis Cluster, and consensus are separate
future projects and are not implied by primary/replica support.

## Correctness gates

Every milestone must keep these commands green:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Protocol parsing also receives native Go fuzz targets. Black-box tests connect
over TCP, and compatibility smoke tests use `redis-cli` when it is installed.
