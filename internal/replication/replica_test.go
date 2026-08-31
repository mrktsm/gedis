package replication

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

func TestReplicaFullSyncAndLiveMutations(t *testing.T) {
	primary, primaryEngine := newProtocolPrimary(t)
	assertReplicationResponse(t, primaryEngine, []string{"SET", "message", "snapshot"}, resp.SimpleString("OK"))
	primaryServer, address, stopPrimary := startPrimaryServer(t, primary, primaryEngine, "127.0.0.1:0")
	defer stopPrimary()
	_ = primaryServer

	replicaStore := store.New()
	replicaEngine := server.NewEngineWithStore(replicaStore)
	replicaEngine.SetReadOnly(true)
	replica, err := NewReplica(ReplicaConfig{
		PrimaryAddress: address,
		ReconnectDelay: 10 * time.Millisecond,
	}, replicaEngine)
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- replica.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Replica.Run() error = %v", err)
		}
	}()

	awaitReplicaReady(t, replica)
	awaitStringValue(t, replicaEngine, "message", "snapshot")
	assertReplicationResponse(t, primaryEngine, []string{"SET", "message", "live"}, resp.SimpleString("OK"))
	assertReplicationResponse(t, primaryEngine, []string{"ZADD", "leaders", "10", "alpha"}, resp.Integer(1))
	awaitStringValue(t, replicaEngine, "message", "live")
	awaitResponse(t, replicaEngine, []string{"ZSCORE", "leaders", "alpha"}, resp.BulkStringString("10"))

	stats := replica.Stats()
	if !stats.Connected || stats.FullSyncs != 1 || stats.PartialSyncs != 0 || stats.Offset != primary.Stats().Offset {
		t.Fatalf("Replica Stats() = %#v, primary %#v", stats, primary.Stats())
	}
	assertReplicationResponse(t, replicaEngine, []string{"SET", "message", "client-write"}, resp.Error(
		"READONLY You can't write against a read only replica.",
	))
}

func TestReplicaReconnectsWithPartialSync(t *testing.T) {
	primary, primaryEngine := newProtocolPrimary(t)
	firstServer, address, stopFirst := startPrimaryServer(t, primary, primaryEngine, "127.0.0.1:0")
	_ = firstServer

	replicaEngine := server.NewEngineWithStore(store.New())
	replicaEngine.SetReadOnly(true)
	replica, err := NewReplica(ReplicaConfig{
		PrimaryAddress: address,
		ReconnectDelay: 10 * time.Millisecond,
	}, replicaEngine)
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- replica.Run(ctx) }()
	defer func() {
		cancel()
		stopFirst()
		if err := <-done; err != nil {
			t.Errorf("Replica.Run() error = %v", err)
		}
	}()

	awaitReplicaReady(t, replica)
	stopFirst()
	assertReplicationResponse(t, primaryEngine, []string{"SET", "during-outage", "caught-up"}, resp.SimpleString("OK"))
	_, _, stopSecond := startPrimaryServer(t, primary, primaryEngine, address)
	defer stopSecond()

	awaitStringValue(t, replicaEngine, "during-outage", "caught-up")
	deadline := time.Now().Add(2 * time.Second)
	for {
		stats := replica.Stats()
		if stats.Connected && stats.FullSyncs == 1 && stats.PartialSyncs >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica did not partial resync: %#v", stats)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReplicaCheckpointResumesAfterObjectRestart(t *testing.T) {
	primary, primaryEngine := newProtocolPrimary(t)
	_, address, stopPrimary := startPrimaryServer(t, primary, primaryEngine, "127.0.0.1:0")
	defer stopPrimary()
	assertReplicationResponse(t, primaryEngine, []string{"SET", "counter", "1"}, resp.SimpleString("OK"))

	firstStore := store.New()
	firstEngine := server.NewEngineWithStore(firstStore)
	firstEngine.SetReadOnly(true)
	first, err := NewReplica(ReplicaConfig{
		PrimaryAddress: address,
		ReconnectDelay: 10 * time.Millisecond,
	}, firstEngine)
	if err != nil {
		t.Fatalf("NewReplica(first) error = %v", err)
	}
	firstContext, stopFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Run(firstContext) }()
	awaitReplicaReady(t, first)
	awaitStringValue(t, firstEngine, "counter", "1")
	stopFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Replica.Run() error = %v", err)
	}
	checkpoint, err := first.Checkpoint(123)
	if err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
	restoredState := firstStore.Snapshot()

	assertReplicationResponse(t, primaryEngine, []string{"INCR", "counter"}, resp.Integer(2))
	restartedStore := store.New()
	if err := restartedStore.Restore(restoredState); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	restartedEngine := server.NewEngineWithStore(restartedStore)
	restartedEngine.SetReadOnly(true)
	restarted, err := NewReplica(ReplicaConfig{
		PrimaryAddress: address,
		ReconnectDelay: 10 * time.Millisecond,
		InitialState:   &checkpoint,
	}, restartedEngine)
	if err != nil {
		t.Fatalf("NewReplica(restarted) error = %v", err)
	}
	restartedContext, stopRestarted := context.WithCancel(context.Background())
	restartedDone := make(chan error, 1)
	go func() { restartedDone <- restarted.Run(restartedContext) }()
	defer func() {
		stopRestarted()
		if err := <-restartedDone; err != nil {
			t.Errorf("restarted Replica.Run() error = %v", err)
		}
	}()
	awaitReplicaReady(t, restarted)
	awaitStringValue(t, restartedEngine, "counter", "2")
	stats := restarted.Stats()
	if stats.FullSyncs != 0 || stats.PartialSyncs != 1 {
		t.Fatalf("restarted replica Stats() = %#v, want one partial sync", stats)
	}
}

func TestDecodeSnapshotRejectsCommandErrors(t *testing.T) {
	t.Parallel()

	data, _ := encodeCommand(byteCommand("NOTACOMMAND"))
	if _, err := decodeSnapshot(data); err == nil {
		t.Fatal("decodeSnapshot(invalid command) error = nil")
	}
}

func TestNewReplicaValidatesConfiguration(t *testing.T) {
	t.Parallel()

	engine := server.NewEngine()
	if _, err := NewReplica(ReplicaConfig{}, engine); err == nil {
		t.Fatal("NewReplica(empty address) error = nil")
	}
	if _, err := NewReplica(ReplicaConfig{PrimaryAddress: "primary:6379"}, nil); err == nil {
		t.Fatal("NewReplica(nil engine) error = nil")
	}
	if _, err := NewReplica(ReplicaConfig{PrimaryAddress: "primary:6379", MaxSnapshotBytes: -1}, engine); err == nil {
		t.Fatal("NewReplica(negative snapshot limit) error = nil")
	}
	checkpoint := PersistentState{
		Version:        replicationStateVersion,
		PrimaryAddress: "other:6379",
		ReplicationID:  testReplicationID,
		Offset:         1,
		AOFSize:        2,
	}
	if _, err := NewReplica(ReplicaConfig{
		PrimaryAddress: "primary:6379",
		InitialState:   &checkpoint,
	}, engine); !errors.Is(err, ErrInvalidReplicationState) {
		t.Fatalf("NewReplica(mismatched checkpoint) error = %v", err)
	}
}

func TestReplicaCheckpointRequiresSynchronization(t *testing.T) {
	t.Parallel()

	replica, err := NewReplica(ReplicaConfig{PrimaryAddress: "primary:6379"}, server.NewEngine())
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	if _, err := replica.Checkpoint(0); !errors.Is(err, ErrNoReplicationState) {
		t.Fatalf("Checkpoint() error = %v, want ErrNoReplicationState", err)
	}
}

func startPrimaryServer(
	t *testing.T,
	primary *Primary,
	engine *server.Engine,
	address string,
) (*server.Server, string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen %s: %v", address, err)
	}
	config := server.DefaultConfig()
	config.ConnectionCommandHandler = primary
	instance := server.New(config, engine)
	done := make(chan error, 1)
	go func() { done <- instance.Serve(listener) }()
	var once bool
	stop := func() {
		if once {
			return
		}
		once = true
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := instance.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	}
	return instance, listener.Addr().String(), stop
}

func awaitReplicaReady(t *testing.T, replica *Replica) {
	t.Helper()
	select {
	case <-replica.Ready():
	case <-time.After(2 * time.Second):
		t.Fatalf("replica did not become ready: %#v", replica.Stats())
	}
}

func awaitStringValue(t *testing.T, engine *server.Engine, key, want string) {
	t.Helper()
	awaitResponse(t, engine, []string{"GET", key}, resp.BulkStringString(want))
}

func awaitResponse(t *testing.T, engine *server.Engine, command []string, want resp.Value) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		encoded := make([][]byte, len(command))
		for index, argument := range command {
			encoded[index] = []byte(argument)
		}
		got := engine.Execute(encoded).Response
		if reflect.DeepEqual(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Execute(%q) = %#v, want %#v", command, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}
