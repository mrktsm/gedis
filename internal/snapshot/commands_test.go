package snapshot

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/store"
)

func TestCommandsEncodeStringsSortedSetsAndAbsoluteExpiry(t *testing.T) {
	t.Parallel()

	entries := []store.SnapshotEntry{
		{
			Key:  "leaders",
			Kind: store.KindSortedSet,
			SortedSet: []store.ZItem{
				{Member: "negative", Score: math.Inf(-1)},
				{Member: "alpha", Score: 10.5},
				{Member: "positive", Score: math.Inf(1)},
			},
			ExpiresAt: time.UnixMilli(105000),
		},
		{
			Key:         "message",
			Kind:        store.KindString,
			StringValue: []byte("value"),
			ExpiresAt:   time.UnixMilli(110000),
		},
	}
	want := [][][]byte{
		{
			[]byte("ZADD"), []byte("leaders"),
			[]byte("-inf"), []byte("negative"),
			[]byte("10.5"), []byte("alpha"),
			[]byte("inf"), []byte("positive"),
		},
		{[]byte("PEXPIREAT"), []byte("leaders"), []byte("105000")},
		{[]byte("SET"), []byte("message"), []byte("value"), []byte("PXAT"), []byte("110000")},
	}
	got := Commands(entries)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Commands() = %q, want %q", got, want)
	}

	got[2][2][0] = 'X'
	if string(entries[1].StringValue) != "value" {
		t.Fatalf("Commands() shares snapshot value storage: %q", entries[1].StringValue)
	}
}

func TestCommandsSkipImpossibleEmptySortedSet(t *testing.T) {
	t.Parallel()

	entries := []store.SnapshotEntry{{Key: "empty", Kind: store.KindSortedSet}}
	if commands := Commands(entries); commands == nil || len(commands) != 0 {
		t.Fatalf("Commands(empty zset) = %#v, want non-nil empty slice", commands)
	}
}
