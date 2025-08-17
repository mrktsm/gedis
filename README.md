# Redis in Go

![Build Status](https://img.shields.io/badge/build-passing-brightgreen)
![License](https://img.shields.io/badge/license-Apache%202.0-blue)

<img src="assets/redis-go-logo.png" alt="Redis-Go Logo" width="160" align="left"/>

A lightweight Redis implementation written in Go, featuring core Redis functionality including key-value storage and sorted sets. This project demonstrates modern Go programming practices with concurrent client handling, Redis-compatible protocol parsing, and efficient in-memory data structures.

Built from the ground up to understand Redis internals, this server supports essential Redis commands and provides a solid foundation for learning distributed systems concepts.

<br clear="left"/>

## Features

- Basic Redis protocol support
- Key-value operations (GET, SET, DEL)
- Sorted Sets (ZADD, ZRANGE, ZREM)
- TCP server implementation
- Concurrent client handling

## Quick Start

```bash
# Start the server
go run cmd/server/main.go

# Run the client
go run cmd/client/main.go
```

## Architecture

- **Server**: TCP server handling Redis protocol commands
- **Storage**: In-memory data structures with thread-safe operations
- **Protocol**: Redis-compatible command parsing and response formatting

Built with Go 1.24.6
