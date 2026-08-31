package replication

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

const testReplicationID = "0123456789abcdef0123456789abcdef01234567"

func TestPrimaryAppendPersistsAndPublishesCanonicalStream(t *testing.T) {
	t.Parallel()

	downstream := &recordingDownstream{}
	primary := newTestPrimary(t, 1024, 4, downstream)
	subscription, offset := primary.Subscribe()
	defer subscription.Close()
	if offset != 0 {
		t.Fatalf("Subscribe() offset = %d, want 0", offset)
	}

	command := byteCommand("SET", "key", "value")
	if err := primary.Append(command); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	chunk := <-subscription.Chunks()
	want, _ := encodeCommand(command)
	if string(chunk.Data) != string(want) || chunk.StartOffset != 1 || chunk.EndOffset != int64(len(want)) {
		t.Fatalf("chunk = %#v, want data %q at 1..%d", chunk, want, len(want))
	}
	if !reflect.DeepEqual(downstream.commands, [][][]byte{command}) {
		t.Fatalf("downstream commands = %q, want %q", downstream.commands, command)
	}
	stats := primary.Stats()
	if stats.Offset != int64(len(want)) || stats.ConnectedReplicas != 1 {
		t.Fatalf("Stats() = %#v", stats)
	}
}

func TestPrimaryDoesNotReplicatePersistenceFailure(t *testing.T) {
	t.Parallel()

	downstreamError := errors.New("disk failed")
	primary := newTestPrimary(t, 1024, 1, &recordingDownstream{appendError: downstreamError})
	subscription, _ := primary.Subscribe()
	defer subscription.Close()
	if err := primary.Append(byteCommand("SET", "key", "value")); !errors.Is(err, downstreamError) {
		t.Fatalf("Append() error = %v, want downstream failure", err)
	}
	if stats := primary.Stats(); stats.Offset != 0 || stats.BacklogBytes != 0 {
		t.Fatalf("Stats() after failure = %#v", stats)
	}
	select {
	case chunk := <-subscription.Chunks():
		t.Fatalf("persistence failure published chunk %#v", chunk)
	default:
	}
}

func TestPrimaryPartialSyncReturnsHistoryThenLiveChunks(t *testing.T) {
	t.Parallel()

	primary := newTestPrimary(t, 1024, 4, nil)
	first := byteCommand("SET", "one", "1")
	second := byteCommand("SET", "two", "2")
	third := byteCommand("INCR", "counter")
	_ = primary.Append(first)
	firstBytes, _ := encodeCommand(first)
	_ = primary.Append(second)
	secondBytes, _ := encodeCommand(second)

	initial, subscription, offset, ok := primary.PartialSync(testReplicationID, int64(len(firstBytes)))
	if !ok {
		t.Fatal("PartialSync() rejected retained offset")
	}
	defer subscription.Close()
	if string(initial) != string(secondBytes) || offset != int64(len(firstBytes)+len(secondBytes)) {
		t.Fatalf("PartialSync() = %q, offset %d", initial, offset)
	}

	_ = primary.Append(third)
	thirdBytes, _ := encodeCommand(third)
	chunk := <-subscription.Chunks()
	if string(chunk.Data) != string(thirdBytes) || chunk.StartOffset != offset+1 {
		t.Fatalf("live chunk = %#v, want %q after %d", chunk, thirdBytes, offset)
	}
}

func TestPrimaryPartialSyncRejectsWrongIDAndTrimmedOffset(t *testing.T) {
	t.Parallel()

	primary := newTestPrimary(t, 8, 1, nil)
	_ = primary.Append(byteCommand("SET", "key", "a-value-longer-than-backlog"))
	if _, subscription, _, ok := primary.PartialSync("ffffffffffffffffffffffffffffffffffffffff", 0); ok || subscription != nil {
		t.Fatal("PartialSync() accepted wrong replication ID")
	}
	if _, subscription, _, ok := primary.PartialSync(testReplicationID, 0); ok || subscription != nil {
		t.Fatal("PartialSync() accepted trimmed offset")
	}
	stats := primary.Stats()
	if _, subscription, _, ok := primary.PartialSync(testReplicationID, stats.Offset); !ok || subscription == nil {
		t.Fatal("PartialSync() rejected current offset")
	} else {
		subscription.Close()
	}
}

func TestPrimaryDisconnectsSlowSubscriber(t *testing.T) {
	t.Parallel()

	primary := newTestPrimary(t, 1024, 1, nil)
	subscription, _ := primary.Subscribe()
	if err := primary.Append(byteCommand("SET", "one", "1")); err != nil {
		t.Fatalf("Append(first) error = %v", err)
	}
	if err := primary.Append(byteCommand("SET", "two", "2")); err != nil {
		t.Fatalf("Append(second) error = %v", err)
	}
	if stats := primary.Stats(); stats.ConnectedReplicas != 0 {
		t.Fatalf("ConnectedReplicas = %d, want 0", stats.ConnectedReplicas)
	}
	if _, ok := <-subscription.Chunks(); !ok {
		t.Fatal("subscriber lost already-buffered chunk")
	}
	if _, ok := <-subscription.Chunks(); ok {
		t.Fatal("slow subscriber channel is still open")
	}
}

func TestPrimaryDelegatesAOFOperations(t *testing.T) {
	t.Parallel()

	downstream := &recordingDownstream{enabled: true, policy: "always", size: 123}
	primary := newTestPrimary(t, 1024, 1, downstream)
	commands := [][][]byte{byteCommand("SET", "key", "value")}
	if err := primary.Rewrite(commands); err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}
	if !reflect.DeepEqual(downstream.rewritten, commands) {
		t.Fatalf("rewritten = %q, want %q", downstream.rewritten, commands)
	}
	enabled, policy, size, err := primary.AOFInfo()
	if err != nil || !enabled || policy != "always" || size != 123 {
		t.Fatalf("AOFInfo() = %t, %q, %d, %v", enabled, policy, size, err)
	}

	withoutAOF := newTestPrimary(t, 1024, 1, nil)
	if err := withoutAOF.Rewrite(nil); !errors.Is(err, ErrPersistenceDisabled) {
		t.Fatalf("Rewrite(no AOF) error = %v, want ErrPersistenceDisabled", err)
	}
	if enabled, policy, _, _ := withoutAOF.AOFInfo(); enabled || policy != "disabled" {
		t.Fatalf("AOFInfo(no AOF) = %t, %q", enabled, policy)
	}
}

func TestPrimaryFullSyncPairsSnapshotOffsetAndLiveStream(t *testing.T) {
	t.Parallel()

	keyspace := store.New()
	primary := newTestPrimary(t, 4096, 4, nil)
	engine := server.NewEngineWithStoreAndSink(keyspace, primary)
	primary.SetSnapshotter(engine)
	assertReplicationResponse(t, engine, []string{"SET", "counter", "1"}, resp.SimpleString("OK"))
	assertReplicationResponse(t, engine, []string{"ZADD", "leaders", "10", "alpha"}, resp.Integer(1))

	full, subscription, err := primary.FullSync()
	if err != nil {
		t.Fatalf("FullSync() error = %v", err)
	}
	defer subscription.Close()
	stats := primary.Stats()
	if full.ReplicationID != testReplicationID || full.Offset != stats.Offset || full.Keys != 2 {
		t.Fatalf("FullSync() = %#v, stats %#v", full, stats)
	}

	recovered := server.NewEngine()
	reader := resp.NewReader(bytes.NewReader(full.Data))
	for {
		command, err := reader.ReadCommand()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadCommand(snapshot) error = %v", err)
		}
		if response := recovered.Execute(command).Response; response.Kind() == resp.KindError {
			t.Fatalf("snapshot command %q returned %q", command, response.Bytes())
		}
	}
	assertReplicationResponse(t, recovered, []string{"GET", "counter"}, resp.BulkStringString("1"))
	assertReplicationResponse(t, recovered, []string{"ZSCORE", "leaders", "alpha"}, resp.BulkStringString("10"))

	assertReplicationResponse(t, engine, []string{"INCR", "counter"}, resp.Integer(2))
	select {
	case chunk := <-subscription.Chunks():
		if chunk.StartOffset != full.Offset+1 {
			t.Fatalf("live chunk starts at %d, want %d", chunk.StartOffset, full.Offset+1)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mutation after full snapshot")
	}
}

func TestPrimaryFullSyncRequiresSnapshotter(t *testing.T) {
	t.Parallel()

	primary := newTestPrimary(t, 1024, 1, nil)
	if _, subscription, err := primary.FullSync(); !errors.Is(err, ErrSnapshotUnavailable) || subscription != nil {
		t.Fatalf("FullSync() = subscription %v, error %v; want ErrSnapshotUnavailable", subscription, err)
	}
}

func TestNewPrimaryValidatesConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewPrimary(PrimaryConfig{BacklogBytes: -1}); !errors.Is(err, ErrInvalidBacklogSize) {
		t.Fatalf("NewPrimary(backlog=-1) error = %v", err)
	}
	if _, err := NewPrimary(PrimaryConfig{SubscriberQueue: -1}); !errors.Is(err, ErrInvalidQueueSize) {
		t.Fatalf("NewPrimary(queue=-1) error = %v", err)
	}
	if _, err := NewPrimary(PrimaryConfig{ReplicationID: "not-an-id"}); !errors.Is(err, ErrInvalidReplicationID) {
		t.Fatalf("NewPrimary(invalid ID) error = %v", err)
	}
}

func newTestPrimary(t *testing.T, backlogBytes, queue int, downstream MutationSink) *Primary {
	t.Helper()
	primary, err := NewPrimary(PrimaryConfig{
		BacklogBytes:    backlogBytes,
		SubscriberQueue: queue,
		ReplicationID:   testReplicationID,
		Downstream:      downstream,
	})
	if err != nil {
		t.Fatalf("NewPrimary() error = %v", err)
	}
	return primary
}

func byteCommand(arguments ...string) [][]byte {
	command := make([][]byte, len(arguments))
	for index, argument := range arguments {
		command[index] = []byte(argument)
	}
	return command
}

type recordingDownstream struct {
	commands    [][][]byte
	rewritten   [][][]byte
	appendError error
	enabled     bool
	policy      string
	size        int64
}

func (s *recordingDownstream) Append(command [][]byte) error {
	if s.appendError != nil {
		return s.appendError
	}
	s.commands = append(s.commands, cloneCommand(command))
	return nil
}

func (s *recordingDownstream) Rewrite(commands [][][]byte) error {
	s.rewritten = make([][][]byte, len(commands))
	for index, command := range commands {
		s.rewritten[index] = cloneCommand(command)
	}
	return nil
}

func (s *recordingDownstream) AOFInfo() (bool, string, int64, error) {
	return s.enabled, s.policy, s.size, nil
}

func cloneCommand(command [][]byte) [][]byte {
	cloned := make([][]byte, len(command))
	for index, argument := range command {
		cloned[index] = append([]byte(nil), argument...)
	}
	return cloned
}

func assertReplicationResponse(t *testing.T, engine *server.Engine, command []string, want resp.Value) {
	t.Helper()
	encoded := make([][]byte, len(command))
	for index, argument := range command {
		encoded[index] = []byte(argument)
	}
	got := engine.Execute(encoded).Response
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Execute(%q) = %#v, want %#v", command, got, want)
	}
}
