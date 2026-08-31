package store

import (
	"context"
	"testing"
	"time"
)

func TestTTLStatesAndRedisRounding(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))

	if got := keyspace.TTL("missing"); got != TTLKeyMissing {
		t.Fatalf("TTL(missing) = %d, want %d", got, TTLKeyMissing)
	}
	_, _ = keyspace.Set("persistent", []byte("value"), SetOptions{})
	if got := keyspace.TTL("persistent"); got != TTLNoExpiry {
		t.Fatalf("TTL(persistent) = %d, want %d", got, TTLNoExpiry)
	}
	_, _ = keyspace.Set("expiring", []byte("value"), SetOptions{TTL: 1500 * time.Millisecond})
	if got := keyspace.PTTL("expiring"); got != 1500 {
		t.Fatalf("PTTL(expiring) = %d, want 1500", got)
	}
	if got := keyspace.TTL("expiring"); got != 2 {
		t.Fatalf("TTL(expiring) = %d, want Redis-rounded value 2", got)
	}
}

func TestExpireAndPersist(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("key", []byte("value"), SetOptions{})

	if applied, _ := keyspace.Expire("key", 2*time.Second); !applied {
		t.Fatal("Expire(existing) = false, want true")
	}
	if applied, _ := keyspace.Expire("missing", time.Second); applied {
		t.Fatal("Expire(missing) = true, want false")
	}
	if !keyspace.Persist("key") {
		t.Fatal("Persist(expiring) = false, want true")
	}
	if keyspace.Persist("key") {
		t.Fatal("Persist(persistent) = true, want false")
	}
	if got := keyspace.TTL("key"); got != TTLNoExpiry {
		t.Fatalf("TTL(persisted) = %d, want %d", got, TTLNoExpiry)
	}
	if applied, _ := keyspace.Expire("key", 0); !applied {
		t.Fatal("Expire(existing, 0) = false, want true")
	}
	if keyspace.Exists("key") != 0 {
		t.Fatal("Expire(existing, 0) did not delete key")
	}
}

func TestActiveExpirationIgnoresStaleHeapEntries(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("key", []byte("first"), SetOptions{TTL: time.Second})
	_, _ = keyspace.Set("key", []byte("second"), SetOptions{TTL: 3 * time.Second})

	clock.Advance(time.Second)
	if got := keyspace.DeleteExpired(0); got != 0 {
		t.Fatalf("DeleteExpired() = %d, want 0 for stale deadline", got)
	}
	if value, exists, _ := keyspace.Get("key"); !exists || string(value) != "second" {
		t.Fatalf("Get(key) = %q, %v, want second, true", value, exists)
	}

	clock.Advance(2 * time.Second)
	if got := keyspace.DeleteExpired(0); got != 1 {
		t.Fatalf("DeleteExpired() = %d, want 1", got)
	}
	if keyspace.Exists("key") != 0 {
		t.Fatal("actively expired key still exists")
	}
}

func TestActiveExpirationHonorsBatchSize(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	for _, key := range []string{"one", "two", "three"} {
		_, _ = keyspace.Set(key, []byte("value"), SetOptions{TTL: time.Second})
	}
	clock.Advance(time.Second)

	if got := keyspace.DeleteExpired(2); got != 2 {
		t.Fatalf("DeleteExpired(2) = %d, want 2", got)
	}
	if got := keyspace.DeleteExpired(2); got != 1 {
		t.Fatalf("second DeleteExpired(2) = %d, want 1", got)
	}
}

func TestRunExpirationStopsWithContext(t *testing.T) {
	t.Parallel()

	keyspace := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		keyspace.RunExpiration(ctx, time.Millisecond, 100)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunExpiration() did not stop after cancellation")
	}
}

func TestRunExpirationActivelyDeletesKeys(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{now: time.Unix(100, 0)}
	keyspace := New(WithClock(clock))
	_, _ = keyspace.Set("key", []byte("value"), SetOptions{TTL: time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go keyspace.RunExpiration(ctx, time.Millisecond, 100)
	clock.Advance(time.Second)

	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		keyspace.mutex.RLock()
		_, exists := keyspace.entries["key"]
		keyspace.mutex.RUnlock()
		if !exists {
			return
		}

		select {
		case <-deadline:
			t.Fatal("RunExpiration() did not actively delete expired key")
		case <-ticker.C:
		}
	}
}
