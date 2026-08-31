package store

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestZAddScoreCardinalityAndOrdering(t *testing.T) {
	t.Parallel()

	keyspace := New()
	result, err := keyspace.ZAdd("leaders", []ZUpdate{
		{Member: "charlie", Score: 20},
		{Member: "bravo", Score: 10},
		{Member: "alpha", Score: 10},
	}, ZAddOptions{})
	if err != nil {
		t.Fatalf("ZAdd() error = %v", err)
	}
	if result.Added != 3 || result.Updated != 0 {
		t.Fatalf("ZAdd() = %#v, want 3 added", result)
	}

	result, err = keyspace.ZAdd("leaders", []ZUpdate{{Member: "charlie", Score: 5}}, ZAddOptions{})
	if err != nil {
		t.Fatalf("ZAdd(update) error = %v", err)
	}
	if result.Added != 0 || result.Updated != 1 {
		t.Fatalf("ZAdd(update) = %#v, want 1 updated", result)
	}

	items, err := keyspace.ZRangeByRank("leaders", 0, -1, false)
	if err != nil {
		t.Fatalf("ZRangeByRank() error = %v", err)
	}
	want := []ZItem{{Member: "charlie", Score: 5}, {Member: "alpha", Score: 10}, {Member: "bravo", Score: 10}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("ZRangeByRank() = %#v, want %#v", items, want)
	}
	if score, exists, _ := keyspace.ZScore("leaders", "charlie"); !exists || score != 5 {
		t.Fatalf("ZScore() = %v, %v, want 5, true", score, exists)
	}
	if size, _ := keyspace.ZCard("leaders"); size != 3 {
		t.Fatalf("ZCard() = %d, want 3", size)
	}
}

func TestZAddOptionsAndDuplicateAccounting(t *testing.T) {
	t.Parallel()

	keyspace := New()
	result, err := keyspace.ZAdd("key", []ZUpdate{
		{Member: "member", Score: 1},
		{Member: "member", Score: 2},
	}, ZAddOptions{})
	if err != nil {
		t.Fatalf("ZAdd(duplicate) error = %v", err)
	}
	if result.Added != 1 || result.Updated != 1 {
		t.Fatalf("ZAdd(duplicate) = %#v, want 1 added and 1 updated", result)
	}

	result, _ = keyspace.ZAdd("key", []ZUpdate{{Member: "member", Score: 3}}, ZAddOptions{Condition: ZAddIfAbsent})
	if result.Applied {
		t.Fatalf("ZAdd(NX existing) = %#v, want not applied", result)
	}
	result, _ = keyspace.ZAdd("key", []ZUpdate{{Member: "missing", Score: 3}}, ZAddOptions{Condition: ZAddIfPresent})
	if result.Applied {
		t.Fatalf("ZAdd(XX missing) = %#v, want not applied", result)
	}
	result, _ = keyspace.ZAdd("key", []ZUpdate{{Member: "member", Score: 1}}, ZAddOptions{Comparison: ZAddIfGreater})
	if result.Applied {
		t.Fatalf("ZAdd(GT lower) = %#v, want not applied", result)
	}
	result, _ = keyspace.ZAdd("key", []ZUpdate{{Member: "member", Score: 1}}, ZAddOptions{Comparison: ZAddIfLess})
	if !result.Applied || result.Updated != 1 {
		t.Fatalf("ZAdd(LT lower) = %#v, want one update", result)
	}
}

func TestZIncrement(t *testing.T) {
	t.Parallel()

	keyspace := New()
	if score, applied, err := keyspace.ZIncrBy("key", "member", 2.5); err != nil || !applied || score != 2.5 {
		t.Fatalf("ZIncrBy(new) = %v, %v, %v, want 2.5, true, nil", score, applied, err)
	}
	if score, applied, err := keyspace.ZIncrBy("key", "member", 1.5); err != nil || !applied || score != 4 {
		t.Fatalf("ZIncrBy(existing) = %v, %v, %v, want 4, true, nil", score, applied, err)
	}
	_, _, _ = keyspace.ZIncrBy("infinity", "member", math.Inf(1))
	if _, _, err := keyspace.ZIncrBy("infinity", "member", math.Inf(-1)); !errors.Is(err, ErrNotFloat) {
		t.Fatalf("ZIncrBy(resulting NaN) error = %v, want ErrNotFloat", err)
	}
}

func TestZRemoveDeletesEmptyKey(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.ZAdd("key", []ZUpdate{{Member: "one", Score: 1}, {Member: "two", Score: 2}}, ZAddOptions{})
	if removed, err := keyspace.ZRem("key", "one", "missing"); err != nil || removed != 1 {
		t.Fatalf("ZRem() = %d, %v, want 1, nil", removed, err)
	}
	if removed, err := keyspace.ZRem("key", "two"); err != nil || removed != 1 {
		t.Fatalf("ZRem(last) = %d, %v, want 1, nil", removed, err)
	}
	if _, exists := keyspace.Kind("key"); exists {
		t.Fatal("empty sorted-set key still exists")
	}
}

func TestZRankAndReverseRanges(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.ZAdd("key", []ZUpdate{
		{Member: "one", Score: 1},
		{Member: "two", Score: 2},
		{Member: "three", Score: 3},
		{Member: "four", Score: 4},
	}, ZAddOptions{})

	if rank, exists, _ := keyspace.ZRank("key", "two", false); !exists || rank != 1 {
		t.Fatalf("ZRank(two) = %d, %v, want 1, true", rank, exists)
	}
	if rank, exists, _ := keyspace.ZRank("key", "two", true); !exists || rank != 2 {
		t.Fatalf("ZRevRank(two) = %d, %v, want 2, true", rank, exists)
	}
	items, _ := keyspace.ZRangeByRank("key", 1, -2, true)
	want := []ZItem{{Member: "three", Score: 3}, {Member: "two", Score: 2}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("ZRangeByRank(reverse) = %#v, want %#v", items, want)
	}
}

func TestZRangeByScoreBoundariesAndLimit(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.ZAdd("key", []ZUpdate{
		{Member: "one", Score: 1},
		{Member: "two-a", Score: 2},
		{Member: "two-b", Score: 2},
		{Member: "three", Score: 3},
		{Member: "four", Score: 4},
	}, ZAddOptions{})

	items, err := keyspace.ZRangeByScore(
		"key",
		ScoreBoundary{Value: 1, Exclusive: true},
		ScoreBoundary{Value: 4},
		false,
		1,
		2,
	)
	if err != nil {
		t.Fatalf("ZRangeByScore() error = %v", err)
	}
	want := []ZItem{{Member: "two-b", Score: 2}, {Member: "three", Score: 3}}
	if !reflect.DeepEqual(items, want) {
		t.Fatalf("ZRangeByScore() = %#v, want %#v", items, want)
	}

	reverse, _ := keyspace.ZRangeByScore(
		"key",
		ScoreBoundary{Value: 2},
		ScoreBoundary{Value: math.Inf(1)},
		true,
		0,
		-1,
	)
	wantReverse := []ZItem{{Member: "four", Score: 4}, {Member: "three", Score: 3}, {Member: "two-b", Score: 2}, {Member: "two-a", Score: 2}}
	if !reflect.DeepEqual(reverse, wantReverse) {
		t.Fatalf("ZRangeByScore(reverse) = %#v, want %#v", reverse, wantReverse)
	}
}

func TestZSetWrongTypeAndExpiration(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("string", []byte("value"), SetOptions{})
	if _, err := keyspace.ZAdd("string", []ZUpdate{{Member: "member", Score: 1}}, ZAddOptions{}); !errors.Is(err, ErrWrongType) {
		t.Fatalf("ZAdd(string) error = %v, want ErrWrongType", err)
	}

	_, _ = keyspace.ZAdd("expiring", []ZUpdate{{Member: "member", Score: 1}}, ZAddOptions{})
	_, _ = keyspace.Expire("expiring", time.Second)
	_, _ = keyspace.ZAdd("expiring", []ZUpdate{{Member: "member", Score: 2}}, ZAddOptions{})
	clock.Advance(time.Second)
	if size, err := keyspace.ZCard("expiring"); err != nil || size != 0 {
		t.Fatalf("ZCard(expired) = %d, %v, want 0, nil", size, err)
	}
}
