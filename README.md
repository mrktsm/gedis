## Gedis - OSS Redis Implementation in Go

[![CI](https://github.com/mrktsm/gedis/actions/workflows/ci.yml/badge.svg)](https://github.com/mrktsm/gedis/actions/workflows/ci.yml)
![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go Version](https://img.shields.io/badge/Go-1.24.6-00ADD8?logo=go)

<img src="assets/redis-go-logo.png" alt="Redis-Go Logo" width="220" align="right"/>

A Redis-compatible in-memory database written from scratch in Go to explore
streaming protocol parsing, concurrent command execution, persistence, and
replication.

Gedis is undergoing a sequential rebuild. The current server implements a
tested RESP2 networking foundation; command, storage, persistence, and
replication milestones are tracked explicitly below.

<br clear="left"/>

## Features

- Binary-safe, bounded RESP2 parsing and encoding
- Pipelined commands over persistent TCP connections
- Concurrent clients with ordered per-connection execution
- `PING`, `ECHO`, and `QUIT`
- Configurable protocol limits and graceful shutdown
- Unit, black-box TCP, race-detector, and fuzz coverage

## Quick Start

```bash
# Start Gedis on the Redis default port
go run ./cmd/gedis

# Connect with an unmodified Redis client
redis-cli PING
redis-cli ECHO "hello from Gedis"
```

## Architecture

- **Server**: TCP lifecycle, concurrent clients, ordered dispatch, and shutdown
- **Protocol**: Independent streaming RESP2 parser and encoder
- **Command engine**: Transport-independent validation and response semantics

The sequential rebuild is specified in the [architecture](docs/architecture.md),
[roadmap](docs/roadmap.md), and [engineering references](docs/references.md).
