package store

import (
	"container/heap"
	"context"
	"time"
)

const (
	TTLKeyMissing int64 = -2
	TTLNoExpiry   int64 = -1
)

type expirationItem struct {
	key        string
	expiresAt  time.Time
	generation uint64
}

type expirationHeap []expirationItem

func (h expirationHeap) Len() int {
	return len(h)
}

func (h expirationHeap) Less(first, second int) bool {
	if h[first].expiresAt.Equal(h[second].expiresAt) {
		return h[first].key < h[second].key
	}
	return h[first].expiresAt.Before(h[second].expiresAt)
}

func (h expirationHeap) Swap(first, second int) {
	h[first], h[second] = h[second], h[first]
}

func (h *expirationHeap) Push(value any) {
	*h = append(*h, value.(expirationItem))
}

func (h *expirationHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = expirationItem{}
	*h = old[:last]
	return value
}

// Expire gives an existing key a relative lifetime. A non-positive lifetime
// deletes the key immediately, matching Redis EXPIRE semantics.
func (k *Keyspace) Expire(key string, lifetime time.Duration) (bool, time.Time) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	return k.expireAtLocked(key, now, now.Add(lifetime))
}

func (k *Keyspace) ExpireAt(key string, expiresAt time.Time) (bool, time.Time) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	return k.expireAtLocked(key, now, expiresAt)
}

func (k *Keyspace) expireAtLocked(key string, now, expiresAt time.Time) (bool, time.Time) {
	current, exists := k.liveEntryLocked(key, now)
	if !exists {
		return false, time.Time{}
	}
	if !expiresAt.After(now) {
		delete(k.entries, key)
		return true, time.Time{}
	}
	current.expiresAt = expiresAt
	k.setEntryLocked(key, current)
	return true, expiresAt
}

func (k *Keyspace) Persist(key string) bool {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists || current.expiresAt.IsZero() {
		return false
	}
	current.expiresAt = time.Time{}
	k.setEntryLocked(key, current)
	return true
}

func (k *Keyspace) TTL(key string) int64 {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	current, exists := k.liveEntryLocked(key, now)
	if !exists {
		return TTLKeyMissing
	}
	if current.expiresAt.IsZero() {
		return TTLNoExpiry
	}
	remaining := current.expiresAt.Sub(now)
	return int64((remaining + 500*time.Millisecond) / time.Second)
}

func (k *Keyspace) PTTL(key string) int64 {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	current, exists := k.liveEntryLocked(key, now)
	if !exists {
		return TTLKeyMissing
	}
	if current.expiresAt.IsZero() {
		return TTLNoExpiry
	}
	return int64(current.expiresAt.Sub(now) / time.Millisecond)
}

// DeleteExpired removes due entries from the deadline heap. A limit of zero
// removes every currently due entry. Stale heap records are ignored using each
// entry's generation number.
func (k *Keyspace) DeleteExpired(limit int) int {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	deleted := 0
	for k.expirations.Len() > 0 && (limit <= 0 || deleted < limit) {
		next := k.expirations[0]
		if next.expiresAt.After(now) {
			break
		}
		heap.Pop(&k.expirations)

		current, exists := k.entries[next.key]
		if !exists || current.generation != next.generation || !current.expired(now) {
			continue
		}
		delete(k.entries, next.key)
		deleted++
	}
	return deleted
}

// RunExpiration performs bounded active expiration until the context is
// canceled. Lazy expiration remains active regardless of whether this worker
// is running.
func (k *Keyspace) RunExpiration(ctx context.Context, interval time.Duration, batchSize int) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			k.DeleteExpired(batchSize)
		}
	}
}
