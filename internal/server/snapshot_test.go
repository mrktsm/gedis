package server

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

func TestCaptureSnapshotPairsStateBeforeLaterMutation(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	assertResponse(t, engine, []string{"SET", "counter", "1"}, resp.SimpleString("OK"))
	captured := make(chan []store.SnapshotEntry, 1)
	release := make(chan struct{})
	snapshotDone := make(chan error, 1)
	go func() {
		snapshotDone <- engine.CaptureSnapshot(func(entries []store.SnapshotEntry) error {
			captured <- entries
			<-release
			return nil
		})
	}()

	entries := <-captured
	writeDone := make(chan resp.Value, 1)
	go func() {
		writeDone <- engine.Execute(stringsToBytes([]string{"INCR", "counter"})).Response
	}()
	select {
	case response := <-writeDone:
		t.Fatalf("write crossed snapshot barrier with response %#v", response)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-snapshotDone; err != nil {
		t.Fatalf("CaptureSnapshot() error = %v", err)
	}
	if response := <-writeDone; response.Int64() != 2 {
		t.Fatalf("INCR response = %#v, want 2", response)
	}
	if len(entries) != 1 || string(entries[0].StringValue) != "1" {
		t.Fatalf("captured entries = %#v, want counter=1", entries)
	}
}

func TestCaptureSnapshotValidatesAndPropagatesCallback(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	if err := engine.CaptureSnapshot(nil); !errors.Is(err, ErrNilSnapshotCallback) {
		t.Fatalf("CaptureSnapshot(nil) error = %v, want ErrNilSnapshotCallback", err)
	}
	callbackError := errors.New("capture failed")
	if err := engine.CaptureSnapshot(func([]store.SnapshotEntry) error {
		return callbackError
	}); !errors.Is(err, callbackError) {
		t.Fatalf("CaptureSnapshot(callback error) = %v", err)
	}
}

func TestInstallSnapshotReplacesStateAndRewritesPersistence(t *testing.T) {
	t.Parallel()

	keyspace := store.New()
	sink := &rewriteRecordingSink{}
	engine := NewEngineWithStoreAndSink(keyspace, sink)
	assertResponse(t, engine, []string{"SET", "old", "removed"}, resp.SimpleString("OK"))
	entries := []store.SnapshotEntry{
		{Key: "counter", Kind: store.KindString, StringValue: []byte("41")},
		{Key: "leaders", Kind: store.KindSortedSet, SortedSet: []store.ZItem{{Member: "alpha", Score: 10}}},
	}
	if err := engine.InstallSnapshot(entries); err != nil {
		t.Fatalf("InstallSnapshot() error = %v", err)
	}
	assertResponse(t, engine, []string{"EXISTS", "old"}, resp.Integer(0))
	assertResponse(t, engine, []string{"GET", "counter"}, resp.BulkStringString("41"))
	want := [][][]byte{
		{[]byte("SET"), []byte("counter"), []byte("41")},
		{[]byte("ZADD"), []byte("leaders"), []byte("10"), []byte("alpha")},
	}
	if !reflect.DeepEqual(sink.rewritten, want) {
		t.Fatalf("rewritten snapshot = %q, want %q", sink.rewritten, want)
	}
}

func TestInstallSnapshotSkipsRewriteWhenAOFDisabled(t *testing.T) {
	t.Parallel()

	sink := &disabledRewriteSink{}
	keyspace := store.New()
	engine := NewEngineWithStoreAndSink(keyspace, sink)
	if err := engine.InstallSnapshot([]store.SnapshotEntry{
		{Key: "key", Kind: store.KindString, StringValue: []byte("value")},
	}); err != nil {
		t.Fatalf("InstallSnapshot() error = %v", err)
	}
	if sink.rewrites != 0 {
		t.Fatalf("disabled sink rewrites = %d, want 0", sink.rewrites)
	}
	assertResponse(t, engine, []string{"GET", "key"}, resp.BulkStringString("value"))
}

type disabledRewriteSink struct {
	rewrites int
}

func (s *disabledRewriteSink) Append(_ [][]byte) error {
	return nil
}

func (s *disabledRewriteSink) Rewrite(_ [][][]byte) error {
	s.rewrites++
	return nil
}

func (s *disabledRewriteSink) AOFInfo() (bool, string, int64, error) {
	return false, "disabled", 0, nil
}
