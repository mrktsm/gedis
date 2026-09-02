## Gedis - OSS Redis Implementation in Go

[![CI](https://github.com/mrktsm/gedis/actions/workflows/ci.yml/badge.svg)](https://github.com/mrktsm/gedis/actions/workflows/ci.yml)
![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go Version](https://img.shields.io/badge/Go-1.24.6-00ADD8?logo=go)

<img src="assets/redis-go-logo.png" alt="Redis-Go Logo" width="220" align="right"/>

A Redis-compatible in-memory database written from scratch in Go to explore
streaming protocol parsing, concurrent command execution, persistence, and
replication.

Gedis is undergoing a sequential rebuild. The current server combines a tested
RESP2 networking layer with a typed concurrent keyspace, expiration, string and
sorted-set commands, append-only persistence, and primary/replica
synchronization.

<br clear="left"/>

## Features

- Binary-safe, bounded RESP2 parsing and encoding
- Pipelined commands over persistent TCP connections
- Concurrent clients with ordered per-connection execution
- Redis-compatible string commands, conditional `SET`, counters, and TTLs
- Sorted sets backed by a span-indexed skip list
- Canonical RESP append-only logging with fsync, replay, and atomic compaction
- Redis-style `INFO persistence` and `INFO replication` visibility
- Primary-side `PSYNC` full/partial streams with a bounded byte backlog
- Read-only replicas with full sync, live streaming, and durable restart catch-up
- Configurable protocol limits and graceful shutdown
- Unit, randomized model, black-box TCP, race-detector, and fuzz coverage

## Quick Start

```bash
# Start Gedis on the Redis default port
go run ./cmd/gedis

# Connect with an unmodified Redis client
redis-cli PING
redis-cli ECHO "hello from Gedis"

# Enable AOF recovery with Redis-style fsync choices
go run ./cmd/gedis -appendonly -appendfsync everysec

# Compact superseded history without stopping the process
redis-cli BGREWRITEAOF
redis-cli INFO persistence
```

When AOF is enabled, the default path is `data/appendonly.aof`. Gedis refuses a
truncated tail by default; pass `-aof-repair-truncated` to keep the valid command
prefix and truncate only the incomplete final command.

`BGREWRITEAOF` runs asynchronously and replaces the log atomically. Unlike
Redis's fork/copy-on-write implementation, the current Gedis rewrite pauses
writes while it serializes the snapshot; reads continue after snapshot capture.

## Primary and Replica

```bash
# terminal 1: writable primary
go run ./cmd/gedis -addr 127.0.0.1:6379 -appendonly

# terminal 2: read-only replica (waits for initial sync before serving)
go run ./cmd/gedis -addr 127.0.0.1:6380 \
  -replicaof 127.0.0.1:6379 \
  -appendonly -aof-path data/replica.aof

redis-cli -p 6379 SET message replicated
redis-cli -p 6380 GET message
redis-cli -p 6380 INFO replication
```

A disconnected replica requests partial resynchronization from the primary
backlog. With AOF enabled, a clean shutdown atomically saves its replication ID,
offset, primary address, and exact AOF size to `<aof-path>.replstate`; use
`-replica-state-path` to override that path. A restart resumes only when the
checkpoint's primary and AOF size match recovered state. Missing, corrupt, or
mismatched state safely forces a full synchronization. Volatile replicas without
AOF always start with a full synchronization.

## Architecture

- **Server**: TCP lifecycle, concurrent clients, ordered dispatch, and shutdown
- **Protocol**: Independent streaming RESP2 parser and encoder
- **Command engine**: Transport-independent validation and response semantics
- **Keyspace**: Typed values, lazy/active expiration, and atomic mutations
- **Persistence**: Ordered canonical mutations, startup recovery, and fsync
  policies
- **Replication**: Byte-offset backlog, atomic full sync, partial resync, and
  crash-safe resume checkpoints

The sequential rebuild is specified in the [architecture](docs/architecture.md),
[roadmap](docs/roadmap.md), [conformance log](docs/conformance.md), and
[engineering references](docs/references.md).
