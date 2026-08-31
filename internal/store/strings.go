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
	KeepTTL        bool
	ReturnPrevious bool
}

type SetResult struct {
	Previous       []byte
	PreviousExists bool
	Applied        bool
}

type StringPair struct {
	Key   string
	Value []byte
}

func (k *Keyspace) Get(key string) ([]byte, bool, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
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

	current, exists := k.liveEntryLocked(key, k.clock.Now())
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
	if options.KeepTTL && exists {
		expiresAt = current.expiresAt
	}
	k.entries[key] = entry{
		kind:       KindString,
		stringData: cloneBytes(value),
		expiresAt:  expiresAt,
	}
	result.Applied = true
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
	k.entries[key] = entry{
		kind:       KindString,
		stringData: []byte(strconv.FormatInt(value, 10)),
		expiresAt:  expiresAt,
	}
	return value, nil
}

func (k *Keyspace) MGet(keys ...string) []SetResult {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	now := k.clock.Now()
	results := make([]SetResult, len(keys))
	for index, key := range keys {
		current, exists := k.liveEntryLocked(key, now)
		if !exists || current.kind != KindString {
			continue
		}
		results[index] = SetResult{
			Previous:       cloneBytes(current.stringData),
			PreviousExists: true,
		}
	}
	return results
}

func (k *Keyspace) MSet(pairs ...StringPair) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	for _, pair := range pairs {
		k.entries[pair.Key] = entry{
			kind:       KindString,
			stringData: cloneBytes(pair.Value),
		}
	}
}
