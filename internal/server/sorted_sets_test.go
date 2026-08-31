package server

import (
	"testing"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestSortedSetCommandSequence(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"ZADD", "leaders", "10", "bravo", "10", "alpha", "20", "charlie"}, resp.Integer(3))
	assertResponse(t, engine, []string{"TYPE", "leaders"}, resp.SimpleString("zset"))
	assertResponse(t, engine, []string{"ZCARD", "leaders"}, resp.Integer(3))
	assertResponse(t, engine, []string{"ZSCORE", "leaders", "alpha"}, resp.BulkStringString("10"))
	assertResponse(t, engine, []string{"ZRANK", "leaders", "bravo"}, resp.Integer(1))
	assertResponse(t, engine, []string{"ZREVRANK", "leaders", "bravo"}, resp.Integer(1))
	assertResponse(t, engine, []string{"ZINCRBY", "leaders", "15.5", "alpha"}, resp.BulkStringString("25.5"))
	assertResponse(t, engine, []string{"ZREM", "leaders", "bravo", "missing"}, resp.Integer(1))
	assertResponse(t, engine, []string{"ZCARD", "leaders"}, resp.Integer(2))
}

func TestZAddOptionsMatchRedisFixtures(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"ZADD", "key", "1", "member", "2", "member"}, resp.Integer(1))
	assertResponse(t, engine, []string{"ZADD", "key", "CH", "3", "member", "4", "member"}, resp.Integer(2))
	assertResponse(t, engine, []string{"ZADD", "key", "NX", "5", "member"}, resp.Integer(0))
	assertResponse(t, engine, []string{"ZADD", "key", "XX", "5", "missing"}, resp.Integer(0))
	assertResponse(t, engine, []string{"ZADD", "key", "GT", "3", "member"}, resp.Integer(0))
	assertResponse(t, engine, []string{"ZADD", "key", "LT", "3", "member"}, resp.Integer(0))
	assertResponse(t, engine, []string{"ZSCORE", "key", "member"}, resp.BulkStringString("3"))
	assertResponse(t, engine, []string{"ZADD", "key", "INCR", "2", "member"}, resp.BulkStringString("5"))
	assertResponse(t, engine, []string{"ZADD", "key", "NX", "INCR", "2", "member"}, resp.NullBulkString())
}

func TestZRangeRankScoreReverseLimitAndScores(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{
		"ZADD", "key", "1", "one", "2", "two-a", "2", "two-b", "3", "three", "4", "four",
	}, resp.Integer(5))
	assertResponse(t, engine, []string{"ZRANGE", "key", "0", "-1"}, resp.Array(
		resp.BulkStringString("one"),
		resp.BulkStringString("two-a"),
		resp.BulkStringString("two-b"),
		resp.BulkStringString("three"),
		resp.BulkStringString("four"),
	))
	assertResponse(t, engine, []string{"ZRANGE", "key", "1", "-2", "REV", "WITHSCORES"}, resp.Array(
		resp.BulkStringString("three"), resp.BulkStringString("3"),
		resp.BulkStringString("two-b"), resp.BulkStringString("2"),
		resp.BulkStringString("two-a"), resp.BulkStringString("2"),
	))
	assertResponse(t, engine, []string{"ZRANGE", "key", "(1", "4", "BYSCORE", "LIMIT", "1", "2", "WITHSCORES"}, resp.Array(
		resp.BulkStringString("two-b"), resp.BulkStringString("2"),
		resp.BulkStringString("three"), resp.BulkStringString("3"),
	))
	assertResponse(t, engine, []string{"ZRANGE", "key", "+inf", "2", "BYSCORE", "REV"}, resp.Array(
		resp.BulkStringString("four"),
		resp.BulkStringString("three"),
		resp.BulkStringString("two-b"),
		resp.BulkStringString("two-a"),
	))
	assertResponse(t, engine, []string{"ZRANGE", "key", "0", "4", "BYSCORE", "LIMIT", "-1", "2"}, resp.Array())
}

func TestSortedSetMissingAndWrongType(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"ZSCORE", "missing", "member"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"ZRANK", "missing", "member"}, resp.NullBulkString())
	assertResponse(t, engine, []string{"ZCARD", "missing"}, resp.Integer(0))
	assertResponse(t, engine, []string{"ZRANGE", "missing", "0", "-1"}, resp.Array())
	assertResponse(t, engine, []string{"SET", "string", "value"}, resp.SimpleString("OK"))
	assertResponse(t, engine, []string{"ZADD", "string", "1", "member"}, resp.Error(
		"WRONGTYPE Operation against a key holding the wrong kind of value",
	))
}

func TestSortedSetErrorsMatchRedisFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command []string
		want    string
	}{
		{command: []string{"ZADD", "key", "NX", "GT", "1", "member"}, want: "ERR GT, LT, and/or NX options at the same time are not compatible"},
		{command: []string{"ZADD", "key", "NX", "XX", "1", "member"}, want: "ERR XX and NX options at the same time are not compatible"},
		{command: []string{"ZADD", "key", "INCR", "1", "one", "2", "two"}, want: "ERR INCR option supports a single increment-element pair"},
		{command: []string{"ZADD", "key", "nope", "member"}, want: "ERR value is not a valid float"},
		{command: []string{"ZRANGE", "key", "nope", "2"}, want: "ERR value is not an integer or out of range"},
		{command: []string{"ZRANGE", "key", "nope", "2", "BYSCORE"}, want: "ERR min or max is not a float"},
		{command: []string{"ZRANGE", "key", "0", "-1", "LIMIT", "0", "1"}, want: "ERR syntax error, LIMIT is only supported in combination with either BYSCORE or BYLEX"},
		{command: []string{"ZRANGE", "key", "0", "1", "BYSCORE", "LIMIT", "0", "nope"}, want: "ERR value is not an integer or out of range"},
	}

	engine := NewEngine()
	for _, test := range tests {
		response := engine.Execute(stringsToBytes(test.command)).Response
		if response.Kind() != resp.KindError || string(response.Bytes()) != test.want {
			t.Fatalf("Execute(%q) = kind %q, %q, want error %q", test.command, response.Kind(), response.Bytes(), test.want)
		}
	}
}

func TestSortedSetCommandArities(t *testing.T) {
	t.Parallel()

	commands := [][]string{
		{"ZADD"}, {"ZADD", "key", "1"}, {"ZREM", "key"}, {"ZSCORE", "key"},
		{"ZCARD"}, {"ZINCRBY", "key", "1"}, {"ZRANGE", "key", "0"},
		{"ZRANK", "key"}, {"ZREVRANK", "key"},
	}
	engine := NewEngine()
	for _, command := range commands {
		response := engine.Execute(stringsToBytes(command)).Response
		if response.Kind() != resp.KindError {
			t.Fatalf("Execute(%q) kind = %q, want error", command, response.Kind())
		}
	}
}
