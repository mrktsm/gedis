package server

import (
	"reflect"
	"testing"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

func TestReadOnlyEngineRejectsWritesAndServesReads(t *testing.T) {
	t.Parallel()

	keyspace := store.New()
	apply := NewEngineWithStore(keyspace)
	assertResponse(t, apply, []string{"SET", "key", "value"}, resp.SimpleString("OK"))

	sink := &recordingSink{}
	clients := NewEngineWithStoreAndSink(keyspace, sink)
	clients.SetReadOnly(true)
	if !clients.ReadOnly() {
		t.Fatal("ReadOnly() = false after SetReadOnly(true)")
	}
	assertResponse(t, clients, []string{"GET", "key"}, resp.BulkStringString("value"))

	writes := [][]string{
		{"SET", "key", "new"},
		{"DEL", "key"},
		{"INCR", "counter"},
		{"MSET", "one", "1"},
		{"PEXPIRE", "key", "1000"},
		{"PERSIST", "key"},
		{"ZADD", "leaders", "1", "member"},
	}
	want := resp.Error("READONLY You can't write against a read only replica.")
	for _, command := range writes {
		got := clients.Execute(stringsToBytes(command)).Response
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Execute(%q) = %#v, want READONLY", command, got)
		}
	}
	if sink.Calls() != 0 {
		t.Fatalf("read-only mutation sink calls = %d, want 0", sink.Calls())
	}
	assertResponse(t, clients, []string{"GET", "key"}, resp.BulkStringString("value"))
	if got := clients.ApplyReplication(stringsToBytes([]string{"SET", "key", "upstream"})).Response; !reflect.DeepEqual(got, resp.SimpleString("OK")) {
		t.Fatalf("ApplyReplication(SET) = %#v, want OK", got)
	}
	assertResponse(t, clients, []string{"GET", "key"}, resp.BulkStringString("upstream"))
	if sink.Calls() != 1 {
		t.Fatalf("replication mutation sink calls = %d, want 1", sink.Calls())
	}

	clients.SetReadOnly(false)
	assertResponse(t, clients, []string{"SET", "key", "new"}, resp.SimpleString("OK"))
}
