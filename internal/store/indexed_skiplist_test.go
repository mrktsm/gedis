package store

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestIndexedSkipListOrdersByScoreThenMember(t *testing.T) {
	t.Parallel()

	list := newIndexedSkipList()
	for _, item := range []struct {
		score  float64
		member string
	}{
		{score: 2, member: "two"},
		{score: 1, member: "zulu"},
		{score: 1, member: "alpha"},
		{score: math.Inf(-1), member: "negative-infinity"},
		{score: math.Inf(1), member: "positive-infinity"},
	} {
		if !list.insert(item.score, item.member) {
			t.Fatalf("insert(%v, %q) = false", item.score, item.member)
		}
	}

	want := []string{"negative-infinity", "alpha", "zulu", "two", "positive-infinity"}
	assertSkipListOrder(t, list, want)
	if list.insert(1, "alpha") {
		t.Fatal("duplicate insert = true, want false")
	}
	if list.insert(math.NaN(), "nan") {
		t.Fatal("NaN insert = true, want false")
	}
}

func TestIndexedSkipListDeleteMaintainsRanksAndLinks(t *testing.T) {
	t.Parallel()

	list := newIndexedSkipList()
	for index, member := range []string{"one", "two", "three", "four", "five"} {
		list.insert(float64(index), member)
	}

	if !list.delete(0, "one") || !list.delete(2, "three") || !list.delete(4, "five") {
		t.Fatal("delete(existing) = false")
	}
	if list.delete(2, "three") {
		t.Fatal("delete(missing) = true")
	}
	assertSkipListOrder(t, list, []string{"two", "four"})
	if list.tail == nil || list.tail.member != "four" {
		t.Fatalf("tail = %#v, want four", list.tail)
	}
	if list.tail.backward == nil || list.tail.backward.member != "two" {
		t.Fatalf("tail.backward = %#v, want two", list.tail.backward)
	}
}

func TestIndexedSkipListRank(t *testing.T) {
	t.Parallel()

	list := newIndexedSkipList()
	for _, item := range []struct {
		score  float64
		member string
	}{
		{score: 10, member: "c"},
		{score: 10, member: "a"},
		{score: 20, member: "d"},
		{score: 10, member: "b"},
	} {
		list.insert(item.score, item.member)
	}

	for rank, member := range []string{"a", "b", "c", "d"} {
		node := list.nodeByRank(uint64(rank))
		if node == nil || node.member != member {
			t.Fatalf("nodeByRank(%d) = %#v, want %q", rank, node, member)
		}
		if got := list.rank(node.score, member); got != uint64(rank) {
			t.Fatalf("rank(%q) = %d, want %d", member, got, rank)
		}
	}
	if list.nodeByRank(4) != nil {
		t.Fatal("nodeByRank(out of range) != nil")
	}
	if got := list.rank(99, "missing"); got != invalidSkipListRank {
		t.Fatalf("rank(missing) = %d, want invalid rank", got)
	}
}

func TestIndexedSkipListScoreBoundaries(t *testing.T) {
	t.Parallel()

	list := newIndexedSkipList()
	for index, member := range []string{"zero", "one-a", "one-b", "two", "three"} {
		score := float64(index)
		if member == "one-b" {
			score = 1
		} else if index > 2 {
			score--
		}
		list.insert(score, member)
	}

	first := list.firstInScoreRange(scoreBoundary{value: 1, exclusive: true})
	if first == nil || first.member != "two" {
		t.Fatalf("first exclusive score > 1 = %#v, want two", first)
	}
	last := list.lastInScoreRange(scoreBoundary{value: 2})
	if last == nil || last.member != "two" {
		t.Fatalf("last inclusive score <= 2 = %#v, want two", last)
	}
	if got := list.firstInScoreRange(scoreBoundary{value: 4}); got != nil {
		t.Fatalf("first score >= 4 = %#v, want nil", got)
	}
}

func TestIndexedSkipListRandomizedAgainstSortedSlice(t *testing.T) {
	t.Parallel()

	type item struct {
		score  float64
		member string
	}

	random := rand.New(rand.NewSource(42))
	list := newIndexedSkipList()
	items := make(map[string]item)
	for operation := 0; operation < 5000; operation++ {
		member := "member-" + string(rune(random.Intn(400)))
		if old, exists := items[member]; exists && random.Intn(3) == 0 {
			if !list.delete(old.score, old.member) {
				t.Fatalf("operation %d: delete(%v, %q) = false", operation, old.score, old.member)
			}
			delete(items, member)
		} else if !exists {
			value := item{score: float64(random.Intn(40) - 20), member: member}
			if !list.insert(value.score, value.member) {
				t.Fatalf("operation %d: insert(%v, %q) = false", operation, value.score, value.member)
			}
			items[member] = value
		}

		ordered := make([]item, 0, len(items))
		for _, value := range items {
			ordered = append(ordered, value)
		}
		sort.Slice(ordered, func(first, second int) bool {
			return lessScoreMember(
				ordered[first].score,
				ordered[first].member,
				ordered[second].score,
				ordered[second].member,
			)
		})
		if uint64(len(ordered)) != list.length {
			t.Fatalf("operation %d: length = %d, want %d", operation, list.length, len(ordered))
		}
		for rank, want := range ordered {
			node := list.nodeByRank(uint64(rank))
			if node == nil || node.score != want.score || node.member != want.member {
				t.Fatalf("operation %d rank %d: node = %#v, want %#v", operation, rank, node, want)
			}
			if got := list.rank(want.score, want.member); got != uint64(rank) {
				t.Fatalf("operation %d: rank(%q) = %d, want %d", operation, want.member, got, rank)
			}
		}
	}
}

func assertSkipListOrder(t *testing.T, list *indexedSkipList, want []string) {
	t.Helper()

	if list.length != uint64(len(want)) {
		t.Fatalf("length = %d, want %d", list.length, len(want))
	}
	current := list.header.levels[0].forward
	for index, member := range want {
		if current == nil || current.member != member {
			t.Fatalf("member %d = %#v, want %q", index, current, member)
		}
		if got := list.nodeByRank(uint64(index)); got != current {
			t.Fatalf("nodeByRank(%d) = %#v, want current node %#v", index, got, current)
		}
		current = current.levels[0].forward
	}
	if current != nil {
		t.Fatalf("list has unexpected trailing node %#v", current)
	}
}
