#!/bin/sh
set -eu

gedis_bench_count=${GEDIS_BENCH_COUNT:-5}
gedis_bench_time=${GEDIS_BENCH_TIME:-1s}

go version
go env GOOS GOARCH
uname -a
exec go test \
  -run '^$' \
  -bench '^Benchmark(Engine|TCP)Commands$' \
  -benchmem \
  -benchtime "$gedis_bench_time" \
  -count "$gedis_bench_count" \
  ./internal/server
