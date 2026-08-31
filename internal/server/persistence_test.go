package server

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

func TestEngineRecordsCanonicalMutations(t *testing.T) {
	t.Parallel()

	clock := &engineFakeClock{now: time.Unix(100, 0)}
	keyspace := store.New(store.WithClock(clock))
	sink := &recordingSink{}
	engine := NewEngineWithStoreAndSink(keyspace, sink)

	assertResponse(t, engine, []string{"GET", "missing"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"SET", "key", "old"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"SET", "key", "new", "NX"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"SET", "temporary", "value", "PX", "1500"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"PEXPIRE", "temporary", "2500"}, resp.Integer(1))
	assertResponse(t, engine, []string{"EXPIRE", "missing", "10"}, resp.Integer(0))

	want := [][][]byte{
		{[]byte("SET"), []byte("key"), []byte("old")},
		{[]byte("SET"), []byte("temporary"), []byte("value"), []byte("PXAT"), []byte("101500")},
		{[]byte("PEXPIREAT"), []byte("temporary"), []byte("102500")},
	}
	if got := sink.Commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded mutations = %q, want %q", got, want)
	}
}

func TestEngineWriteGatePreservesReplayOrder(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	liveStore := store.New()
	live := NewEngineWithStoreAndSink(liveStore, sink)

	const writers = 100
	var waitGroup sync.WaitGroup
	waitGroup.Add(writers)
	for writer := 0; writer < writers; writer++ {
		go func() {
			defer waitGroup.Done()
			value := fmt.Sprintf("value-%d", writer)
			response := live.Execute(stringsToBytes([]string{"SET", "key", value})).Response
			if response.Kind() != resp.KindSimpleString {
				t.Errorf("SET response = kind %q, %q", response.Kind(), response.Bytes())
			}
		}()
	}
	waitGroup.Wait()

	liveValue, _, err := liveStore.Get("key")
	if err != nil {
		t.Fatalf("live Get() error = %v", err)
	}
	recoveredStore := store.New()
	recovery := NewEngineWithStore(recoveredStore)
	for _, command := range sink.Commands() {
		response := recovery.Execute(command).Response
		if response.Kind() == resp.KindError {
			t.Fatalf("replay %q returned %q", command, response.Bytes())
		}
	}
	recoveredValue, _, err := recoveredStore.Get("key")
	if err != nil {
		t.Fatalf("recovered Get() error = %v", err)
	}
	if string(recoveredValue) != string(liveValue) {
		t.Fatalf("recovered value = %q, live value = %q", recoveredValue, liveValue)
	}
}

func TestEngineBlocksWritesAfterPersistenceFailure(t *testing.T) {
	t.Parallel()

	sinkError := errors.New("disk failed\nread-only filesystem")
	sink := &recordingSink{err: sinkError}
	keyspace := store.New()
	engine := NewEngineWithStoreAndSink(keyspace, sink)

	response := engine.Execute(stringsToBytes([]string{"SET", "key", "value"})).Response
	if response.Kind() != resp.KindError || !strings.HasPrefix(string(response.Bytes()), "MISCONF ") ||
		strings.Contains(string(response.Bytes()), "\n") {
		t.Fatalf("SET response = kind %q, %q, want safe MISCONF error", response.Kind(), response.Bytes())
	}
	if value, exists, _ := keyspace.Get("key"); !exists || string(value) != "value" {
		t.Fatalf("failed persisted write in memory = %q, %v, want value, true", value, exists)
	}

	response = engine.Execute(stringsToBytes([]string{"DEL", "key"})).Response
	if response.Kind() != resp.KindError || !strings.HasPrefix(string(response.Bytes()), "MISCONF ") {
		t.Fatalf("second write response = kind %q, %q", response.Kind(), response.Bytes())
	}
	if keyspace.Exists("key") != 1 {
		t.Fatal("write after persistence failure mutated the keyspace")
	}
	if sink.Calls() != 1 {
		t.Fatalf("sink calls = %d, want 1", sink.Calls())
	}

	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("value"))
}

func TestEngineDoesNotRecordFailedWrites(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	engine := NewEngineWithStoreAndSink(store.New(), sink)
	assertResponse(t, engine, []string{"SET", "key", "value"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"ZADD", "key", "1", "member"}, resp.Error(
		"WRONGTYPE Operation against a key holding the wrong kind of value",
	))
	if sink.Calls() != 1 {
		t.Fatalf("sink calls = %d, want only successful SET", sink.Calls())
	}
}

func TestAbsoluteExpirationSurvivesReplayDelay(t *testing.T) {
	t.Parallel()

	liveClock := &engineFakeClock{now: time.Unix(100, 0)}
	sink := &recordingSink{}
	live := NewEngineWithStoreAndSink(store.New(store.WithClock(liveClock)), sink)
	assertResponse(t, live, []string{"SET", "key", "value", "PX", "1500"}, resp.SimpleString("OK"))

	recoveryClock := &engineFakeClock{now: time.Unix(101, 0)}
	recoveredStore := store.New(store.WithClock(recoveryClock))
	recovery := NewEngineWithStore(recoveredStore)
	for _, command := range sink.Commands() {
		response := recovery.Execute(command).Response
		if response.Kind() == resp.KindError {
			t.Fatalf("replay %q returned %q", command, response.Bytes())
		}
	}
	if got := recoveredStore.PTTL("key"); got != 500 {
		t.Fatalf("recovered PTTL = %d, want remaining 500ms", got)
	}
	recoveryClock.now = recoveryClock.now.Add(500 * time.Millisecond)
	if recoveredStore.Exists("key") != 0 {
		t.Fatal("recovered key received a fresh TTL instead of its absolute deadline")
	}
}

func TestRewriteAOFUsesDeterministicMinimalMutations(t *testing.T) {
	t.Parallel()

	clock := &engineFakeClock{now: time.Unix(100, 0)}
	keyspace := store.New(store.WithClock(clock))
	sink := &rewriteRecordingSink{}
	engine := NewEngineWithStoreAndSink(keyspace, sink)
	assertResponse(t, engine, []string{"SET", "message", "old"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"SET", "message", "current", "PX", "10000"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"ZADD", "leaders", "20", "bravo", "10", "alpha"}, resp.Integer(2))
	assertResponse(t, engine, []string{"PEXPIRE", "leaders", "5000"}, resp.Integer(1))

	keys, err := engine.RewriteAOF()
	if err != nil {
		t.Fatalf("RewriteAOF() error = %v", err)
	}
	if keys != 2 {
		t.Fatalf("RewriteAOF() keys = %d, want 2", keys)
	}
	want := [][][]byte{
		{
			[]byte("ZADD"), []byte("leaders"),
			[]byte("10"), []byte("alpha"),
			[]byte("20"), []byte("bravo"),
		},
		{[]byte("PEXPIREAT"), []byte("leaders"), []byte("105000")},
		{[]byte("SET"), []byte("message"), []byte("current"), []byte("PXAT"), []byte("110000")},
	}
	if !reflect.DeepEqual(sink.rewritten, want) {
		t.Fatalf("rewritten commands = %q, want %q", sink.rewritten, want)
	}
}

func TestRewriteAOFFailureBlocksLaterWrites(t *testing.T) {
	t.Parallel()

	rewriteError := errors.New("rewrite disk failure")
	sink := &rewriteRecordingSink{rewriteError: rewriteError}
	engine := NewEngineWithStoreAndSink(store.New(), sink)
	assertResponse(t, engine, []string{"SET", "key", "value"}, resp.SimpleString("OK"))

	if _, err := engine.RewriteAOF(); !errors.Is(err, rewriteError) {
		t.Fatalf("RewriteAOF() error = %v, want rewrite failure", err)
	}
	response := engine.Execute(stringsToBytes([]string{"DEL", "key"})).Response
	if response.Kind() != resp.KindError || !strings.HasPrefix(string(response.Bytes()), "MISCONF ") {
		t.Fatalf("DEL after rewrite failure = kind %q, %q", response.Kind(), response.Bytes())
	}
	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("value"))
}

func TestRewriteAOFRequiresRewriteCapablePersistence(t *testing.T) {
	t.Parallel()

	if _, err := NewEngine().RewriteAOF(); !errors.Is(err, ErrPersistenceDisabled) {
		t.Fatalf("RewriteAOF(no sink) error = %v, want ErrPersistenceDisabled", err)
	}
	if _, err := NewEngineWithStoreAndSink(store.New(), &recordingSink{}).RewriteAOF(); !errors.Is(err, ErrRewriteUnsupported) {
		t.Fatalf("RewriteAOF(append-only sink) error = %v, want ErrRewriteUnsupported", err)
	}
}

type recordingSink struct {
	mutex    sync.Mutex
	commands [][][]byte
	err      error
	calls    int
}

type rewriteRecordingSink struct {
	rewritten    [][][]byte
	rewriteError error
}

func (s *rewriteRecordingSink) Append(_ [][]byte) error {
	return nil
}

func (s *rewriteRecordingSink) Rewrite(commands [][][]byte) error {
	if s.rewriteError != nil {
		return s.rewriteError
	}
	s.rewritten = make([][][]byte, len(commands))
	for index, command := range commands {
		s.rewritten[index] = cloneByteCommand(command)
	}
	return nil
}

func cloneByteCommand(command [][]byte) [][]byte {
	cloned := make([][]byte, len(command))
	for index, argument := range command {
		cloned[index] = append([]byte(nil), argument...)
	}
	return cloned
}

func (s *recordingSink) Append(command [][]byte) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.calls++
	if s.err != nil {
		return s.err
	}
	cloned := make([][]byte, len(command))
	for index, argument := range command {
		cloned[index] = append([]byte(nil), argument...)
	}
	s.commands = append(s.commands, cloned)
	return nil
}

func (s *recordingSink) Commands() [][][]byte {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	commands := make([][][]byte, len(s.commands))
	for commandIndex, command := range s.commands {
		commands[commandIndex] = make([][]byte, len(command))
		for argumentIndex, argument := range command {
			commands[commandIndex][argumentIndex] = append([]byte(nil), argument...)
		}
	}
	return commands
}

func (s *recordingSink) Calls() int {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.calls
}
