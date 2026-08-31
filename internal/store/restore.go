package store

import (
	"container/heap"
	"fmt"
	"math"
)

// Restore validates and builds replacement state before atomically swapping it
// into the live keyspace. Expired entries are omitted rather than resurrected.
func (k *Keyspace) Restore(snapshot []SnapshotEntry) error {
	now := k.clock.Now()
	replacement := make(map[string]entry, len(snapshot))
	seen := make(map[string]struct{}, len(snapshot))
	expirations := make(expirationHeap, 0, len(snapshot))
	var generation uint64

	for _, item := range snapshot {
		if _, duplicate := seen[item.Key]; duplicate {
			return fmt.Errorf("%w: duplicate key %q", ErrInvalidSnapshot, item.Key)
		}
		seen[item.Key] = struct{}{}
		if !item.ExpiresAt.IsZero() && !now.Before(item.ExpiresAt) {
			continue
		}

		generation++
		current := entry{
			kind:       item.Kind,
			expiresAt:  item.ExpiresAt,
			generation: generation,
		}
		switch item.Kind {
		case KindString:
			current.stringData = cloneBytes(item.StringValue)
		case KindSortedSet:
			if len(item.SortedSet) == 0 {
				return fmt.Errorf("%w: sorted set %q is empty", ErrInvalidSnapshot, item.Key)
			}
			current.sortedSet = newSortedSet()
			for _, member := range item.SortedSet {
				if math.IsNaN(member.Score) {
					return fmt.Errorf("%w: sorted set %q contains NaN", ErrInvalidSnapshot, item.Key)
				}
				if _, duplicate := current.sortedSet.scores[member.Member]; duplicate {
					return fmt.Errorf("%w: sorted set %q repeats member %q", ErrInvalidSnapshot, item.Key, member.Member)
				}
				current.sortedSet.scores[member.Member] = member.Score
				current.sortedSet.index.insert(member.Score, member.Member)
			}
		default:
			return fmt.Errorf("%w: key %q has kind %d", ErrInvalidSnapshot, item.Key, item.Kind)
		}
		replacement[item.Key] = current
		if !current.expiresAt.IsZero() {
			heap.Push(&expirations, expirationItem{
				key:        item.Key,
				expiresAt:  current.expiresAt,
				generation: current.generation,
			})
		}
	}

	k.mutex.Lock()
	k.entries = replacement
	k.expirations = expirations
	k.nextGeneration = generation
	k.mutex.Unlock()
	return nil
}
