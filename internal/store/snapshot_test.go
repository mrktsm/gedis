package store

import (
	"reflect"
	"testing"
	"time"
)

func TestSnapshotIsDeterministicAndIndependent(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("z-string", []byte("value"), SetOptions{
		ExpiresAt: clock.Now().Add(time.Hour),
	})
	_, _ = keyspace.ZAdd("a-zset", []ZUpdate{
		{Member: "bravo", Score: 10},
		{Member: "alpha", Score: 10},
		{Member: "charlie", Score: 20},
	}, ZAddOptions{})

	want := []SnapshotEntry{
		{
			Key:  "a-zset",
			Kind: KindSortedSet,
			SortedSet: []ZItem{
				{Member: "alpha", Score: 10},
				{Member: "bravo", Score: 10},
				{Member: "charlie", Score: 20},
			},
		},
		{
			Key:         "z-string",
			Kind:        KindString,
			StringValue: []byte("value"),
			ExpiresAt:   clock.Now().Add(time.Hour),
		},
	}
	snapshot := keyspace.Snapshot()
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("Snapshot() = %#v, want %#v", snapshot, want)
	}

	snapshot[1].StringValue[0] = 'X'
	snapshot[0].SortedSet[0].Member = "changed"
	value, _, _ := keyspace.Get("z-string")
	items, _ := keyspace.ZRangeByRank("a-zset", 0, -1, false)
	if string(value) != "value" || items[0].Member != "alpha" {
		t.Fatalf("mutating snapshot changed keyspace: value %q, items %#v", value, items)
	}

	_, _ = keyspace.Set("z-string", []byte("new"), SetOptions{})
	if string(snapshot[1].StringValue) != "Xalue" {
		t.Fatalf("keyspace mutation changed snapshot: %q", snapshot[1].StringValue)
	}
}

func TestSnapshotExcludesExpiredKeys(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("expired", []byte("gone"), SetOptions{
		ExpiresAt: clock.Now().Add(time.Second),
	})
	_, _ = keyspace.Set("live", []byte("kept"), SetOptions{})
	clock.Advance(time.Second)

	snapshot := keyspace.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Key != "live" {
		t.Fatalf("Snapshot() = %#v, want only live key", snapshot)
	}
	if _, exists := keyspace.entries["expired"]; exists {
		t.Fatal("Snapshot() did not remove expired key")
	}
}

func TestSnapshotOfEmptyKeyspaceIsNonNil(t *testing.T) {
	t.Parallel()

	if snapshot := New().Snapshot(); snapshot == nil || len(snapshot) != 0 {
		t.Fatalf("Snapshot() = %#v, want non-nil empty slice", snapshot)
	}
}
