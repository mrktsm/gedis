package replication

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestBacklogOffsetsAndPartialHistory(t *testing.T) {
	t.Parallel()

	backlog, err := NewBacklog(8)
	if err != nil {
		t.Fatalf("NewBacklog() error = %v", err)
	}
	if start, end := backlog.Append([]byte("abc")); start != 1 || end != 3 {
		t.Fatalf("Append(abc) offsets = %d..%d, want 1..3", start, end)
	}
	if start, end := backlog.Append([]byte("def")); start != 4 || end != 6 {
		t.Fatalf("Append(def) offsets = %d..%d, want 4..6", start, end)
	}

	for offset, want := range map[int64]string{
		0: "abcdef",
		1: "bcdef",
		3: "def",
		6: "",
	} {
		got, ok := backlog.After(offset)
		if !ok || string(got) != want {
			t.Errorf("After(%d) = %q, %t; want %q, true", offset, got, ok, want)
		}
	}
	if backlog.Offset() != 6 || backlog.FirstOffset() != 1 || backlog.HistoryLength() != 6 {
		t.Fatalf("backlog metadata = offset %d, first %d, history %d", backlog.Offset(), backlog.FirstOffset(), backlog.HistoryLength())
	}
}

func TestBacklogTrimsOldestBytes(t *testing.T) {
	t.Parallel()

	backlog, _ := NewBacklog(5)
	backlog.Append([]byte("abc"))
	backlog.Append([]byte("defg"))
	if backlog.Offset() != 7 || backlog.FirstOffset() != 3 || backlog.HistoryLength() != 5 {
		t.Fatalf("backlog metadata = offset %d, first %d, history %d", backlog.Offset(), backlog.FirstOffset(), backlog.HistoryLength())
	}
	if _, ok := backlog.After(1); ok {
		t.Fatal("After(trimmed offset) succeeded")
	}
	if got, ok := backlog.After(2); !ok || string(got) != "cdefg" {
		t.Fatalf("After(2) = %q, %t; want cdefg, true", got, ok)
	}

	backlog.Append([]byte("123456789"))
	if got, ok := backlog.After(11); !ok || string(got) != "56789" {
		t.Fatalf("After(11) = %q, %t; want 56789, true", got, ok)
	}
	if backlog.Offset() != 16 || backlog.FirstOffset() != 12 {
		t.Fatalf("large append metadata = offset %d, first %d", backlog.Offset(), backlog.FirstOffset())
	}
}

func TestBacklogReturnsIndependentBytes(t *testing.T) {
	t.Parallel()

	backlog, _ := NewBacklog(8)
	input := []byte("value")
	backlog.Append(input)
	input[0] = 'X'
	first, _ := backlog.After(0)
	first[0] = 'Y'
	second, _ := backlog.After(0)
	if string(second) != "value" {
		t.Fatalf("backlog shares mutable data: %q", second)
	}
}

func TestBacklogConcurrentAppendsAreContiguous(t *testing.T) {
	t.Parallel()

	backlog, _ := NewBacklog(128)
	const writers = 100
	var waitGroup sync.WaitGroup
	waitGroup.Add(writers)
	for value := byte(0); value < writers; value++ {
		go func() {
			defer waitGroup.Done()
			backlog.Append([]byte{value})
		}()
	}
	waitGroup.Wait()

	stream, ok := backlog.After(0)
	if !ok || len(stream) != writers || backlog.Offset() != writers {
		t.Fatalf("concurrent backlog = len %d, offset %d, ok %t", len(stream), backlog.Offset(), ok)
	}
	counts := make([]int, writers)
	for _, value := range stream {
		counts[int(value)]++
	}
	for value, count := range counts {
		if count != 1 {
			t.Errorf("byte %d count = %d, want 1", value, count)
		}
	}
}

func TestNewBacklogRejectsInvalidCapacity(t *testing.T) {
	t.Parallel()

	if _, err := NewBacklog(0); !errors.Is(err, ErrInvalidBacklogSize) {
		t.Fatalf("NewBacklog(0) error = %v, want ErrInvalidBacklogSize", err)
	}
}

func TestReplicationIDUsesRedisLengthHex(t *testing.T) {
	t.Parallel()

	id, err := newID(bytes.NewReader(bytes.Repeat([]byte{0xab}, replicationIDBytes)))
	if err != nil {
		t.Fatalf("newID() error = %v", err)
	}
	want := "abababababababababababababababababababab"
	if id != want {
		t.Fatalf("newID() = %q, want %q", id, want)
	}
}
