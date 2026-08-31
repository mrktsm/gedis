package replication

import (
	"context"
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
