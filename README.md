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
sorted-set commands, and the foundations of append-only persistence.

<br clear="left"/>

## Features

- Binary-safe, bounded RESP2 parsing and encoding
- Pipelined commands over persistent TCP connections
- Concurrent clients with ordered per-connection execution
- Redis-compatible string commands, conditional `SET`, counters, and TTLs
- Sorted sets backed by a span-indexed skip list
- Canonical RESP append-only logging with fsync, replay, and atomic compaction
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
```

When AOF is enabled, the default path is `data/appendonly.aof`. Gedis refuses a
truncated tail by default; pass `-aof-repair-truncated` to keep the valid command
prefix and truncate only the incomplete final command.

`BGREWRITEAOF` runs asynchronously and replaces the log atomically. Unlike
Redis's fork/copy-on-write implementation, the current Gedis rewrite pauses
writes while it serializes the snapshot; reads continue after snapshot capture.

## Architecture

- **Server**: TCP lifecycle, concurrent clients, ordered dispatch, and shutdown
- **Protocol**: Independent streaming RESP2 parser and encoder
- **Command engine**: Transport-independent validation and response semantics
- **Keyspace**: Typed values, lazy/active expiration, and atomic mutations
- **Persistence**: Ordered canonical mutations, startup recovery, and fsync
  policies

The sequential rebuild is specified in the [architecture](docs/architecture.md),
[roadmap](docs/roadmap.md), [conformance log](docs/conformance.md), and
[engineering references](docs/references.md).
