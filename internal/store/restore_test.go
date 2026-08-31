package store

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestRestoreAtomicallyReplacesMixedState(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("old", []byte("removed"), SetOptions{})
	snapshot := []SnapshotEntry{
		{
			Key:         "message",
			Kind:        KindString,
			StringValue: []byte("value"),
			ExpiresAt:   clock.Now().Add(time.Second),
		},
		{
			Key:  "leaders",
			Kind: KindSortedSet,
			SortedSet: []ZItem{
				{Member: "bravo", Score: 20},
				{Member: "alpha", Score: 10},
			},
		},
	}
	if err := keyspace.Restore(snapshot); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if keyspace.Exists("old") != 0 {
		t.Fatal("Restore() retained old key")
	}
	value, exists, err := keyspace.Get("message")
	if err != nil || !exists || string(value) != "value" || keyspace.PTTL("message") != 1000 {
		t.Fatalf("restored string = %q, %t, %v, TTL %d", value, exists, err, keyspace.PTTL("message"))
	}
	items, err := keyspace.ZRangeByRank("leaders", 0, -1, false)
	if err != nil || len(items) != 2 || items[0].Member != "alpha" || items[1].Member != "bravo" {
		t.Fatalf("restored sorted set = %#v, %v", items, err)
	}

	snapshot[0].StringValue[0] = 'X'
	snapshot[1].SortedSet[0].Member = "changed"
	value, _, _ = keyspace.Get("message")
	items, _ = keyspace.ZRangeByRank("leaders", 0, -1, false)
	if string(value) != "value" || items[1].Member != "bravo" {
		t.Fatalf("Restore() shares snapshot storage: %q, %#v", value, items)
	}
}

func TestRestoreOmitsExpiredEntries(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	err := keyspace.Restore([]SnapshotEntry{
		{Key: "expired", Kind: KindString, StringValue: []byte("gone"), ExpiresAt: clock.Now()},
		{Key: "live", Kind: KindString, StringValue: []byte("value")},
	})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if keyspace.Exists("expired") != 0 || keyspace.Exists("live") != 1 {
		t.Fatalf("restored key existence = expired %d, live %d", keyspace.Exists("expired"), keyspace.Exists("live"))
	}
}

func TestRestoreRejectsInvalidSnapshotWithoutChangingState(t *testing.T) {
	t.Parallel()

	tests := [][]SnapshotEntry{
		{
			{Key: "duplicate", Kind: KindString},
			{Key: "duplicate", Kind: KindString},
		},
		{{Key: "unknown", Kind: Kind(99)}},
		{{Key: "empty", Kind: KindSortedSet}},
		{{Key: "nan", Kind: KindSortedSet, SortedSet: []ZItem{{Member: "member", Score: math.NaN()}}}},
		{{Key: "members", Kind: KindSortedSet, SortedSet: []ZItem{{Member: "same", Score: 1}, {Member: "same", Score: 2}}}},
	}
	for _, snapshot := range tests {
		keyspace := New()
		_, _ = keyspace.Set("existing", []byte("preserved"), SetOptions{})
		if err := keyspace.Restore(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
			t.Fatalf("Restore(%#v) error = %v, want ErrInvalidSnapshot", snapshot, err)
		}
		value, exists, err := keyspace.Get("existing")
		if err != nil || !exists || string(value) != "preserved" {
			t.Fatalf("state after rejected Restore = %q, %t, %v", value, exists, err)
		}
	}
}

func TestRestoreEmptySnapshotClearsKeyspace(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.Set("key", []byte("value"), SetOptions{})
	if err := keyspace.Restore(nil); err != nil {
		t.Fatalf("Restore(nil) error = %v", err)
	}
	if snapshot := keyspace.Snapshot(); snapshot == nil || len(snapshot) != 0 {
		t.Fatalf("Snapshot(after Restore nil) = %#v", snapshot)
	}
}
