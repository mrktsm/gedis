package server

import (
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

func TestStringCommandSequence(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	steps := []struct {
		command []string
		want    resp.Value
	}{
		{command: []string{"GET", "key"}, want: resp.NullBulkString()},
		{command: []string{"SET", "key", "value"}, want: resp.SimpleString("OK")},
		{command: []string{"GET", "key"}, want: resp.BulkStringString("value")},
		{command: []string{"TYPE", "key"}, want: resp.SimpleString("string")},
		{command: []string{"EXISTS", "key", "missing", "key"}, want: resp.Integer(2)},
		{command: []string{"DEL", "missing", "key"}, want: resp.Integer(1)},
		{command: []string{"TYPE", "key"}, want: resp.SimpleString("none")},
	}

	for _, step := range steps {
		got := engine.Execute(stringsToBytes(step.command)).Response
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("Execute(%q) = %#v, want %#v", step.command, got, step.want)
		}
	}
}

func TestSetOptionsMatchRedisSemantics(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"SET", "key", "old"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"SET", "key", "new", "NX", "GET"}, resp.BulkStringString("old"))
	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("old"))
	assertResponse(t, engine, []string{"SET", "key", "new", "XX", "GET"}, resp.BulkStringString("old"))
	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("new"))
	assertResponse(t, engine, []string{"DEL", "key"}, resp.Integer(1))
	assertResponse(t, engine, []string{"SET", "key", "first", "XX", "GET"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"SET", "key", "first", "NX", "GET"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("first"))
}

func TestSetExpiration(t *testing.T) {
	t.Parallel()

	clock := &engineFakeClock{now: time.Unix(100, 0)}
	keyspace := store.New(store.WithClock(clock))
	engine := NewEngineWithStore(keyspace)

	assertResponse(t, engine, []string{"SET", "seconds", "value", "EX", "2"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"SET", "milliseconds", "value", "PX", "1500"}, resp.SimpleString("OK"))
	clock.now = clock.now.Add(1500 * time.Millisecond)
	assertResponse(t, engine, []string{"GET", "seconds"}, resp.BulkStringString("value"))
	assertResponse(t, engine, []string{"GET", "milliseconds"}, resp.NullBulkString())
	clock.now = clock.now.Add(500 * time.Millisecond)
	assertResponse(t, engine, []string{"GET", "seconds"}, resp.NullBulkString())
}

func TestExpirationCommands(t *testing.T) {
	t.Parallel()

	clock := &engineFakeClock{now: time.Unix(100, 0)}
	keyspace := store.New(store.WithClock(clock))
	engine := NewEngineWithStore(keyspace)

	assertResponse(t, engine, []string{"TTL", "missing"}, resp.Integer(-2))
	assertResponse(t, engine, []string{"SET", "key", "value"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"TTL", "key"}, resp.Integer(-1))
	assertResponse(t, engine, []string{"PEXPIRE", "key", "1500"}, resp.Integer(1))
	assertResponse(t, engine, []string{"PTTL", "key"}, resp.Integer(1500))
	assertResponse(t, engine, []string{"TTL", "key"}, resp.Integer(2))
	assertResponse(t, engine, []string{"PERSIST", "key"}, resp.Integer(1))
	assertResponse(t, engine, []string{"PERSIST", "key"}, resp.Integer(0))
	assertResponse(t, engine, []string{"EXPIRE", "key", "0"}, resp.Integer(1))
	assertResponse(t, engine, []string{"EXISTS", "key"}, resp.Integer(0))
	assertResponse(t, engine, []string{"EXPIRE", "missing", "10"}, resp.Integer(0))
}

func TestKeepTTLAndIncrementPreserveExpiration(t *testing.T) {
	t.Parallel()

	clock := &engineFakeClock{now: time.Unix(100, 0)}
	keyspace := store.New(store.WithClock(clock))
	engine := NewEngineWithStore(keyspace)

	assertResponse(t, engine, []string{"SET", "key", "1", "EX", "10"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"SET", "key", "2", "KEEPTTL"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"INCR", "key"}, resp.Integer(3))
	clock.now = clock.now.Add(10 * time.Second)
	assertResponse(t, engine, []string{"GET", "key"}, resp.NullBulkString())
}

func TestSetRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command []string
		want    string
	}{
		{command: []string{"SET", "key", "value", "NX", "XX"}, want: "ERR syntax error"},
		{command: []string{"SET", "key", "value", "EX", "1", "PX", "1"}, want: "ERR syntax error"},
		{command: []string{"SET", "key", "value", "EX", "0"}, want: "ERR invalid expire time in 'set' command"},
		{command: []string{"SET", "key", "value", "EX", "nope"}, want: "ERR value is not an integer or out of range"},
		{command: []string{"SET", "key", "value", "UNKNOWN"}, want: "ERR syntax error"},
	}

	engine := NewEngine()
	for _, test := range tests {
		response := engine.Execute(stringsToBytes(test.command)).Response
		if response.Kind() != resp.KindError || string(response.Bytes()) != test.want {
			t.Fatalf("Execute(%q) = kind %q, %q, want error %q", test.command, response.Kind(), response.Bytes(), test.want)
		}
	}
}

func TestIncrementCommands(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"INCR", "counter"}, resp.Integer(1))
	assertResponse(t, engine, []string{"INCRBY", "counter", "41"}, resp.Integer(42))
	assertResponse(t, engine, []string{"SET", "counter", "not-a-number"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"INCR", "counter"}, resp.Error("ERR value is not an integer or out of range"))
	assertResponse(t, engine, []string{"INCRBY", "counter", "nope"}, resp.Error("ERR value is not an integer or out of range"))
}

func TestMultiStringCommands(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"MSET", "one", "1", "two", "2"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"MGET", "one", "missing", "two"}, resp.Array(
		resp.BulkStringString("1"),
		resp.NullBulkString(),
		resp.BulkStringString("2"),
	))
}

func TestStringCommandArities(t *testing.T) {
	t.Parallel()

	commands := [][]string{
		{"GET"}, {"GET", "one", "two"}, {"SET", "key"}, {"DEL"}, {"EXISTS"},
		{"INCR"}, {"INCR", "one", "two"}, {"INCRBY", "key"}, {"MGET"},
		{"MSET"}, {"MSET", "key"}, {"TYPE"}, {"EXPIRE", "key"},
		{"PEXPIRE", "key"}, {"TTL"}, {"PTTL"}, {"PERSIST"},
	}
	engine := NewEngine()
	for _, command := range commands {
		response := engine.Execute(stringsToBytes(command)).Response
		if response.Kind() != resp.KindError {
			t.Fatalf("Execute(%q) kind = %q, want error", command, response.Kind())
		}
	}
}

func assertResponse(t *testing.T, engine *Engine, command []string, want resp.Value) {
	t.Helper()

	got := engine.Execute(stringsToBytes(command)).Response
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Execute(%q) = %#v, want %#v", command, got, want)
	}
}

type engineFakeClock struct {
	now time.Time
}

func (c *engineFakeClock) Now() time.Time {
	return c.now
}
