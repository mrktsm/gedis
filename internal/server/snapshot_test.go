package server

import (
	"errors"
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
