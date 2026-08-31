package store

import (
	"container/heap"
	"errors"
	"sync"
	"time"
)

var (
	ErrWrongType        = errors.New("operation against a key holding the wrong kind of value")
	ErrNotInteger       = errors.New("value is not an integer or out of range")
	ErrNotFloat         = errors.New("value is not a valid float")
	ErrInvalidArguments = errors.New("invalid arguments")
)

// Kind identifies a value stored in the keyspace.
type Kind uint8

const (
	KindString Kind = iota + 1
	KindSortedSet
)

type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now()
}

type entry struct {
	kind       Kind
	stringData []byte
	sortedSet  *sortedSet
	expiresAt  time.Time
	generation uint64
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// Option configures a Keyspace.
type Option func(*Keyspace)

// Keyspace is a concurrent, typed in-memory keyspace. Each public operation is
// atomic with respect to every other operation.
type Keyspace struct {
	mutex   sync.RWMutex
	entries map[string]entry
	clock   Clock

	nextGeneration uint64
	expirations    expirationHeap
}

func New(options ...Option) *Keyspace {
	keyspace := &Keyspace{
		entries: make(map[string]entry),
		clock:   wallClock{},
	}
	for _, option := range options {
		option(keyspace)
	}
	return keyspace
}

func WithClock(clock Clock) Option {
	return func(keyspace *Keyspace) {
		keyspace.clock = clock
	}
}

func (k *Keyspace) Delete(keys ...string) int64 {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	var deleted int64
	for _, key := range keys {
		current, exists := k.entries[key]
		if !exists {
			continue
		}
		if current.expired(now) {
			delete(k.entries, key)
			continue
		}
		delete(k.entries, key)
		deleted++
	}
	return deleted
}

// Exists counts every key argument independently, including duplicates, as
// Redis does.
func (k *Keyspace) Exists(keys ...string) int64 {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	var found int64
	for _, key := range keys {
		current, exists := k.entries[key]
		if !exists {
			continue
		}
		if current.expired(now) {
			delete(k.entries, key)
			continue
		}
		found++
	}
	return found
}

func (k *Keyspace) Kind(key string) (Kind, bool) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return 0, false
	}
	return current.kind, true
}

func (k *Keyspace) liveEntryLocked(key string, now time.Time) (entry, bool) {
	current, exists := k.entries[key]
	if !exists {
		return entry{}, false
	}
	if current.expired(now) {
		delete(k.entries, key)
		return entry{}, false
	}
	return current, true
}

func (k *Keyspace) setEntryLocked(key string, value entry) {
	k.nextGeneration++
	if k.nextGeneration == 0 {
		k.nextGeneration++
	}
	value.generation = k.nextGeneration
	k.entries[key] = value
	if !value.expiresAt.IsZero() {
		heap.Push(&k.expirations, expirationItem{
			key:        key,
			expiresAt:  value.expiresAt,
			generation: value.generation,
		})
	}
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}
