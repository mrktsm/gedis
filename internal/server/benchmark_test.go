package server

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

var benchmarkResponse resp.Value

func BenchmarkEngineCommands(b *testing.B) {
	b.Run("GET/hit", func(b *testing.B) {
		engine := NewEngine()
		engine.Execute([][]byte{[]byte("SET"), []byte("key"), bytes.Repeat([]byte{'x'}, 64)})
		command := [][]byte{[]byte("GET"), []byte("key")}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkResponse = engine.Execute(command).Response
		}
	})

	b.Run("SET/overwrite-64B", func(b *testing.B) {
		engine := NewEngine()
		command := [][]byte{[]byte("SET"), []byte("key"), bytes.Repeat([]byte{'x'}, 64)}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkResponse = engine.Execute(command).Response
		}
	})

	b.Run("INCR/hot-key", func(b *testing.B) {
		engine := NewEngine()
		command := [][]byte{[]byte("INCR"), []byte("counter")}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkResponse = engine.Execute(command).Response
		}
	})

	b.Run("INCR/parallel-hot-key", func(b *testing.B) {
		engine := NewEngine()
		command := [][]byte{[]byte("INCR"), []byte("counter")}
		b.ReportAllocs()
		b.RunParallel(func(parallel *testing.PB) {
			var response resp.Value
			for parallel.Next() {
				response = engine.Execute(command).Response
			}
			_ = response
		})
	})

	b.Run("ZRANK/10k-members", func(b *testing.B) {
		engine := benchmarkSortedSet(b, 10_000)
		command := [][]byte{[]byte("ZRANK"), []byte("leaders"), []byte("member-005000")}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkResponse = engine.Execute(command).Response
		}
	})

	b.Run("ZRANGE/100-of-10k", func(b *testing.B) {
		engine := benchmarkSortedSet(b, 10_000)
		command := [][]byte{[]byte("ZRANGE"), []byte("leaders"), []byte("0"), []byte("99"), []byte("WITHSCORES")}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			benchmarkResponse = engine.Execute(command).Response
		}
	})
}

func BenchmarkTCPCommands(b *testing.B) {
	b.Run("PING/round-trip", func(b *testing.B) {
		connection := benchmarkServerConnection(b)
		writer := resp.NewWriter(connection)
		reader := resp.NewReader(connection)
		command := [][]byte{[]byte("PING")}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if err := writer.WriteCommand(command...); err != nil {
				b.Fatalf("write PING: %v", err)
			}
			response, err := reader.ReadValue()
			if err != nil {
				b.Fatalf("read PING: %v", err)
			}
			benchmarkResponse = response
		}
	})

	b.Run("PING/pipeline-16", func(b *testing.B) {
		connection := benchmarkServerConnection(b)
		buffered := bufio.NewWriterSize(connection, 16*1024)
		writer := resp.NewWriter(buffered)
		reader := resp.NewReader(connection)
		command := [][]byte{[]byte("PING")}
		b.ReportAllocs()
		b.ResetTimer()
		for completed := 0; completed < b.N; {
			batch := min(16, b.N-completed)
			for range batch {
				if err := writer.WriteCommand(command...); err != nil {
					b.Fatalf("write pipelined PING: %v", err)
				}
			}
			if err := buffered.Flush(); err != nil {
				b.Fatalf("flush PING pipeline: %v", err)
			}
			for range batch {
				response, err := reader.ReadValue()
				if err != nil {
					b.Fatalf("read pipelined PING: %v", err)
				}
				benchmarkResponse = response
			}
			completed += batch
		}
	})

	b.Run("SET/round-trip-64B", func(b *testing.B) {
		connection := benchmarkServerConnection(b)
		writer := resp.NewWriter(connection)
		reader := resp.NewReader(connection)
		command := [][]byte{[]byte("SET"), []byte("key"), bytes.Repeat([]byte{'x'}, 64)}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if err := writer.WriteCommand(command...); err != nil {
				b.Fatalf("write SET: %v", err)
			}
			response, err := reader.ReadValue()
			if err != nil {
				b.Fatalf("read SET: %v", err)
			}
			benchmarkResponse = response
		}
	})
}

func benchmarkSortedSet(b *testing.B, members int) *Engine {
	b.Helper()
	engine := NewEngine()
	for index := range members {
		member := []byte("member-" + leftPad(index, 6))
		command := [][]byte{[]byte("ZADD"), []byte("leaders"), []byte(strconv.Itoa(index)), member}
		if response := engine.Execute(command).Response; response.Kind() == resp.KindError {
			b.Fatalf("populate sorted set: %s", response.Bytes())
		}
	}
	return engine
}

func leftPad(value, width int) string {
	text := strconv.Itoa(value)
	if len(text) >= width {
		return text
	}
	return string(bytes.Repeat([]byte{'0'}, width-len(text))) + text
}

func benchmarkServerConnection(b *testing.B) net.Conn {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	server := New(DefaultConfig(), NewEngine())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	b.Cleanup(func() {
		_ = connection.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			b.Errorf("shutdown: %v", err)
		}
		if err := <-done; err != nil {
			b.Errorf("serve: %v", err)
		}
	})
	return connection
}
