package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrktsm/gedis/internal/aof"
	"github.com/mrktsm/gedis/internal/replication"
	"github.com/mrktsm/gedis/internal/server"
)

const testReplicationID = "0123456789abcdef0123456789abcdef01234567"

func TestResolveReplicaStatePath(t *testing.T) {
	t.Parallel()

	if got := resolveReplicaStatePath(options{}); got != "" {
		t.Fatalf("resolveReplicaStatePath(primary) = %q, want empty", got)
	}
	configured := options{
		appendOnly:       true,
		aofPath:          "state/data.aof",
		replicaOf:        "primary:6379",
		replicaStatePath: "custom/checkpoint.json",
	}
	if got := resolveReplicaStatePath(configured); got != configured.replicaStatePath {
		t.Fatalf("resolveReplicaStatePath(configured) = %q", got)
	}
	configured.replicaStatePath = ""
	if got := resolveReplicaStatePath(configured); got != configured.aofPath+".replstate" {
		t.Fatalf("resolveReplicaStatePath(default) = %q", got)
	}
}

func TestLoadReplicaStateRequiresMatchingPrimaryAndAOF(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	missing, err := loadReplicaState(filepath.Join(directory, "missing.json"), "primary:6379", 42)
	if err != nil || missing != nil {
		t.Fatalf("loadReplicaState(missing) = %#v, %v", missing, err)
	}

	path := filepath.Join(directory, "checkpoint.json")
	want := replication.PersistentState{
		Version:        1,
		PrimaryAddress: "primary:6379",
		ReplicationID:  testReplicationID,
		Offset:         123,
		AOFSize:        42,
	}
	if err := replication.SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	got, err := loadReplicaState(path, want.PrimaryAddress, want.AOFSize)
	if err != nil || got == nil || *got != want {
		t.Fatalf("loadReplicaState(match) = %#v, %v; want %#v", got, err, want)
	}
	if _, err := loadReplicaState(path, "other:6379", want.AOFSize); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("loadReplicaState(primary mismatch) error = %v", err)
	}
	if _, err := loadReplicaState(path, want.PrimaryAddress, want.AOFSize+1); err == nil || !strings.Contains(err.Error(), "AOF size") {
		t.Fatalf("loadReplicaState(size mismatch) error = %v", err)
	}
}

func TestSaveReplicaStatePairsSyncedAOFSizeAndOffset(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	aofPath := filepath.Join(directory, "appendonly.aof")
	appendLog, err := aof.Open(aof.Config{Path: aofPath, SyncPolicy: aof.SyncNever})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer appendLog.Close()
	if err := appendLog.Append([][]byte{[]byte("SET"), []byte("key"), []byte("value")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	replica, err := replication.NewReplica(replication.ReplicaConfig{
		PrimaryAddress: "primary:6379",
		InitialState: &replication.PersistentState{
			Version:        1,
			PrimaryAddress: "primary:6379",
			ReplicationID:  testReplicationID,
			Offset:         456,
			AOFSize:        0,
		},
	}, server.NewEngine())
	if err != nil {
		t.Fatalf("NewReplica() error = %v", err)
	}
	statePath := filepath.Join(directory, "replica.json")
	if err := saveReplicaState(statePath, replica, appendLog); err != nil {
		t.Fatalf("saveReplicaState() error = %v", err)
	}
	state, err := replication.LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	info, err := os.Stat(aofPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if state.Offset != 456 || state.AOFSize != info.Size() {
		t.Fatalf("saved state = %#v, AOF size = %d", state, info.Size())
	}
}
