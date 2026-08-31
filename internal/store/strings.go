package store

import (
	"strconv"
	"time"
)

type SetCondition uint8

const (
	SetAlways SetCondition = iota
	SetIfAbsent
	SetIfPresent
)

type SetOptions struct {
	Condition      SetCondition
	ExpiresAt      time.Time
	TTL            time.Duration
	KeepTTL        bool
	ReturnPrevious bool
}

type SetResult struct {
	Previous       []byte
	PreviousExists bool
	Applied        bool
	ExpiresAt      time.Time
}

type StringPair struct {
	Key   string
	Value []byte
}

type StringResult struct {
	Value  []byte
	Exists bool
}

func (k *Keyspace) Get(key string) ([]byte, bool, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	current, exists := k.liveEntryLocked(key, now)
	if !exists {
		return nil, false, nil
	}
	if current.kind != KindString {
		return nil, false, ErrWrongType
	}
	return cloneBytes(current.stringData), true, nil
}

func (k *Keyspace) Set(key string, value []byte, options SetOptions) (SetResult, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	current, exists := k.liveEntryLocked(key, now)
	result := SetResult{Applied: false}
	if exists && options.ReturnPrevious {
		if current.kind != KindString {
			return SetResult{}, ErrWrongType
		}
		result.Previous = cloneBytes(current.stringData)
		result.PreviousExists = true
	}

	switch options.Condition {
	case SetIfAbsent:
		if exists {
			return result, nil
		}
	case SetIfPresent:
		if !exists {
			return result, nil
		}
	}

	expiresAt := options.ExpiresAt
	if options.TTL > 0 {
		expiresAt = now.Add(options.TTL)
	}
	if options.KeepTTL && exists {
		expiresAt = current.expiresAt
	}
	k.setEntryLocked(key, entry{
		kind:       KindString,
		stringData: cloneBytes(value),
		expiresAt:  expiresAt,
	})
	result.Applied = true
	result.ExpiresAt = expiresAt
	return result, nil
}

func (k *Keyspace) Increment(key string, increment int64) (int64, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	var value int64
	var err error
	if exists {
		if current.kind != KindString {
			return 0, ErrWrongType
		}
		value, err = strconv.ParseInt(string(current.stringData), 10, 64)
		if err != nil {
			return 0, ErrNotInteger
		}
	}

	if increment > 0 && value > int64(^uint64(0)>>1)-increment {
		return 0, ErrNotInteger
	}
	minimum := -int64(^uint64(0)>>1) - 1
	if increment < 0 && value < minimum-increment {
		return 0, ErrNotInteger
	}
	value += increment

	expiresAt := time.Time{}
	if exists {
		expiresAt = current.expiresAt
	}
	k.setEntryLocked(key, entry{
		kind:       KindString,
		stringData: []byte(strconv.FormatInt(value, 10)),
		expiresAt:  expiresAt,
	})
	return value, nil
}

func (k *Keyspace) MGet(keys ...string) []StringResult {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	results := make([]StringResult, len(keys))
	for index, key := range keys {
		current, exists := k.liveEntryLocked(key, now)
		if !exists || current.kind != KindString {
			continue
		}
		results[index] = StringResult{
			Value:  cloneBytes(current.stringData),
			Exists: true,
		}
	}
	return results
}

func (k *Keyspace) MSet(pairs ...StringPair) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	for _, pair := range pairs {
		k.setEntryLocked(pair.Key, entry{
			kind:       KindString,
			stringData: cloneBytes(pair.Value),
		})
	}
}
