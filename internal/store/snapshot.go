package store

import (
	"sort"
	"time"
)

// SnapshotEntry is an immutable copy of one live key at a point in time.
// Exactly one of StringValue or SortedSet is populated according to Kind.
type SnapshotEntry struct {
	Key         string
	Kind        Kind
	StringValue []byte
	SortedSet   []ZItem
	ExpiresAt   time.Time
}

// Snapshot returns every live key in deterministic key order. Sorted-set
// members use their canonical score/member order. The returned data does not
// share mutable storage with the keyspace.
func (k *Keyspace) Snapshot() []SnapshotEntry {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	keys := make([]string, 0, len(k.entries))
	for key, current := range k.entries {
		if current.expired(now) {
			delete(k.entries, key)
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	snapshot := make([]SnapshotEntry, 0, len(keys))
	for _, key := range keys {
		current := k.entries[key]
		copied := SnapshotEntry{
			Key:       key,
			Kind:      current.kind,
			ExpiresAt: current.expiresAt,
		}
		switch current.kind {
		case KindString:
			copied.StringValue = cloneBytes(current.stringData)
		case KindSortedSet:
			copied.SortedSet = make([]ZItem, 0, len(current.sortedSet.scores))
			for node := current.sortedSet.index.header.levels[0].forward; node != nil; node = node.levels[0].forward {
				copied.SortedSet = append(copied.SortedSet, ZItem{
					Member: node.member,
					Score:  node.score,
				})
			}
		}
		snapshot = append(snapshot, copied)
	}
	return snapshot
}
