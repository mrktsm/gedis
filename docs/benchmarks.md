# Reproducible benchmarks

## What is measured

The checked-in Go benchmarks intentionally report two distinct scopes:

- `BenchmarkEngineCommands` measures command validation and execution without
  RESP parsing, TCP, or persistence.
- `BenchmarkTCPCommands` measures an unpersisted Gedis server and client over
  the TCP loopback interface. It includes RESP encoding/decoding and one
  persistent connection. `PING/pipeline-16` sends and receives batches of 16;
  the round-trip cases wait for each reply.

These scopes must not be compared as though they measure the same work. The TCP
benchmarks are also not a maximum-throughput claim: they use one connection and
either one in-flight request or a fixed pipeline depth. Redis's benchmark guide
similarly recommends recording clients, pipeline depth, payload size,
persistence, and machine conditions before comparing results.

## Run it

Use an otherwise idle machine and keep power/CPU policy constant between runs:

```bash
./scripts/bench.sh
```

The script prints the Go, OS, architecture, and kernel metadata, then runs every
workload for a one-second target five times with allocation reporting. Override
only the duration or repeat count when needed:

```bash
GEDIS_BENCH_TIME=3s GEDIS_BENCH_COUNT=10 ./scripts/bench.sh
```

For change comparisons, capture the complete output before and after on the
same machine and analyze all samples with Go's `benchstat`; do not compare a
Gedis embedded benchmark with `redis-benchmark` network output.

## Recorded local baseline

Date: 2026-09-02. Revision: `9237a3b`. Environment: Go 1.24.6,
`darwin/arm64`, macOS 14.5 (23F79), Apple M1, 8 logical CPUs. AOF was disabled.
Values are the median of five one-second runs; the range shows the minimum and
maximum observed `ns/op` rather than hiding scheduler variance.

| Workload | Median ns/op | Observed range | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Engine `GET` hit, 64-byte value | 256.3 | 254.4–260.4 | 67 | 2 |
| Engine `SET` overwrite, 64-byte value | 427.8 | 419.9–429.4 | 168 | 6 |
| Engine `INCR`, hot key | 373.9 | 370.1–376.7 | 32 | 3 |
| Engine parallel `INCR`, one hot key | 527.3 | 520.7–583.0 | 32 | 4 |
| Engine `ZRANK`, 10,000 members | 367.1 | 344.1–395.8 | 5 | 1 |
| Engine `ZRANGE` first 100 with scores | 30,881 | 29,638–32,122 | 24,600 | 393 |
| TCP `PING`, one request per round trip | 31,814 | 31,102–36,786 | 256 | 15 |
| TCP `PING`, pipeline depth 16 | 7,400 | 5,557–8,595 | 263 | 15 |
| TCP `SET` round trip, 64-byte value | 62,806 | 48,682–101,068 | 784 | 29 |

This baseline is useful for detecting large regressions on comparable Apple M1
systems. It is not a cross-machine score or evidence that Gedis is faster than
Redis.
