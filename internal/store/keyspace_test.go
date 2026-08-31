package store

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSetGetAndDelete(t *testing.T) {
	t.Parallel()

	keyspace := New()
	input := []byte("value")
	result, err := keyspace.Set("key", input, SetOptions{})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if !result.Applied {
		t.Fatal("Set().Applied = false, want true")
	}

	input[0] = 'X'
	got, exists, err := keyspace.Get("key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !exists || string(got) != "value" {
		t.Fatalf("Get() = %q, %v, want value, true", got, exists)
	}

	got[0] = 'X'
	again, _, _ := keyspace.Get("key")
	if string(again) != "value" {
		t.Fatalf("Get() returned mutable storage: got %q", again)
	}

	if got := keyspace.Delete("missing", "key"); got != 1 {
		t.Fatalf("Delete() = %d, want 1", got)
	}
	if _, exists, _ := keyspace.Get("key"); exists {
		t.Fatal("Get() after Delete() exists = true")
	}
}

func TestSetConditionsAndPreviousValue(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.Set("key", []byte("old"), SetOptions{})

	notApplied, err := keyspace.Set("key", []byte("new"), SetOptions{
		Condition:      SetIfAbsent,
		ReturnPrevious: true,
	})
	if err != nil {
		t.Fatalf("Set(NX) error = %v", err)
	}
	if notApplied.Applied || !notApplied.PreviousExists || string(notApplied.Previous) != "old" {
		t.Fatalf("Set(NX) = %#v, want previous old without applying", notApplied)
	}

	applied, err := keyspace.Set("key", []byte("new"), SetOptions{
		Condition:      SetIfPresent,
		ReturnPrevious: true,
	})
	if err != nil {
		t.Fatalf("Set(XX) error = %v", err)
	}
	if !applied.Applied || string(applied.Previous) != "old" {
		t.Fatalf("Set(XX) = %#v, want applied with previous old", applied)
	}

	missing, err := keyspace.Set("missing", []byte("value"), SetOptions{Condition: SetIfPresent})
	if err != nil {
		t.Fatalf("Set(missing XX) error = %v", err)
	}
	if missing.Applied {
		t.Fatal("Set(missing XX).Applied = true, want false")
	}
}

func TestSetOverwritesTypeUnlessPreviousRequested(t *testing.T) {
	t.Parallel()

	keyspace := New()
	keyspace.entries["key"] = entry{kind: KindSortedSet}

	_, err := keyspace.Set("key", []byte("value"), SetOptions{ReturnPrevious: true})
	if !errors.Is(err, ErrWrongType) {
		t.Fatalf("Set(GET) error = %v, want ErrWrongType", err)
	}
	if kind, _ := keyspace.Kind("key"); kind != KindSortedSet {
		t.Fatalf("Set(GET) changed wrong-type key to kind %v", kind)
	}

	if _, err := keyspace.Set("key", []byte("value"), SetOptions{}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if value, _, err := keyspace.Get("key"); err != nil || string(value) != "value" {
		t.Fatalf("Get() = %q, %v, want value", value, err)
	}
}

func TestExistsCountsDuplicateArguments(t *testing.T) {
	t.Parallel()

	keyspace := New()
	_, _ = keyspace.Set("key", []byte("value"), SetOptions{})
	if got := keyspace.Exists("key", "missing", "key"); got != 2 {
		t.Fatalf("Exists() = %d, want 2", got)
	}
}

func TestGetWrongType(t *testing.T) {
	t.Parallel()

	keyspace := New()
	keyspace.entries["key"] = entry{kind: KindSortedSet}
	if _, _, err := keyspace.Get("key"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("Get() error = %v, want ErrWrongType", err)
	}
}

func TestIncrement(t *testing.T) {
	t.Parallel()

	keyspace := New()
	if got, err := keyspace.Increment("counter", 1); err != nil || got != 1 {
		t.Fatalf("Increment(missing) = %d, %v, want 1, nil", got, err)
	}
	if got, err := keyspace.Increment("counter", 41); err != nil || got != 42 {
		t.Fatalf("Increment(existing) = %d, %v, want 42, nil", got, err)
	}

	_, _ = keyspace.Set("not-integer", []byte("1.5"), SetOptions{})
	if _, err := keyspace.Increment("not-integer", 1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("Increment(non-integer) error = %v, want ErrNotInteger", err)
	}

	_, _ = keyspace.Set("maximum", []byte(strconv.FormatInt(math.MaxInt64, 10)), SetOptions{})
	if _, err := keyspace.Increment("maximum", 1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("Increment(maximum) error = %v, want ErrNotInteger", err)
	}

	_, _ = keyspace.Set("minimum", []byte(strconv.FormatInt(math.MinInt64, 10)), SetOptions{})
	if _, err := keyspace.Increment("minimum", -1); !errors.Is(err, ErrNotInteger) {
		t.Fatalf("Increment(minimum) error = %v, want ErrNotInteger", err)
	}
}

func TestIncrementIsAtomic(t *testing.T) {
	t.Parallel()

	keyspace := New()
	const workers = 100
	const increments = 100

	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			for range increments {
				if _, err := keyspace.Increment("counter", 1); err != nil {
					t.Errorf("Increment() error = %v", err)
					return
				}
			}
		}()
	}
	waitGroup.Wait()

	value, exists, err := keyspace.Get("counter")
	if err != nil || !exists || string(value) != "10000" {
		t.Fatalf("Get(counter) = %q, %v, %v, want 10000, true, nil", value, exists, err)
	}
}

func TestMGetAndMSet(t *testing.T) {
	t.Parallel()

	keyspace := New()
	keyspace.MSet(
		StringPair{Key: "one", Value: []byte("1")},
		StringPair{Key: "two", Value: []byte("2")},
	)
	keyspace.entries["other-type"] = entry{kind: KindSortedSet}

	results := keyspace.MGet("one", "missing", "other-type", "two")
	if len(results) != 4 || string(results[0].Previous) != "1" || results[1].PreviousExists ||
		results[2].PreviousExists || string(results[3].Previous) != "2" {
		t.Fatalf("MGet() = %#v", results)
	}
}

func TestLazyExpiration(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(withClock(clock))
	_, _ = keyspace.Set("key", []byte("value"), SetOptions{
		ExpiresAt: clock.Now().Add(time.Second),
	})

	if keyspace.Exists("key") != 1 {
		t.Fatal("Exists() before expiration = 0, want 1")
	}
	clock.Advance(time.Second)
	if keyspace.Exists("key") != 0 {
		t.Fatal("Exists() at expiration = 1, want 0")
	}
	if _, exists := keyspace.entries["key"]; exists {
		t.Fatal("expired key was not deleted")
	}
}

type fakeClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mutex.Lock()
	c.now = c.now.Add(duration)
	c.mutex.Unlock()
}
