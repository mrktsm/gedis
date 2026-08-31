package replication

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveAndLoadReplicationState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "replica.state")
	want := PersistentState{
		Version:        replicationStateVersion,
		PrimaryAddress: "127.0.0.1:6379",
		ReplicationID:  testReplicationID,
		Offset:         12345,
		AOFSize:        678,
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadState() = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".replica.state.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary state files = %q, error %v", matches, err)
	}
}

func TestSaveStateAtomicallyReplacesPreviousCheckpoint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "replica.state")
	state := PersistentState{
		Version:        replicationStateVersion,
		PrimaryAddress: "primary:6379",
		ReplicationID:  testReplicationID,
		Offset:         10,
		AOFSize:        20,
	}
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState(first) error = %v", err)
	}
	state.Offset = 30
	state.AOFSize = 40
	if err := SaveState(path, state); err != nil {
		t.Fatalf("SaveState(second) error = %v", err)
	}
	got, err := LoadState(path)
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("LoadState() = %#v, %v, want %#v", got, err, state)
	}
}

func TestLoadStateRejectsCorruptionAndUnknownFields(t *testing.T) {
	t.Parallel()

	for _, contents := range []string{
		"not-json",
		`{"version":1,"primary_address":"primary:6379","replication_id":"0123456789abcdef0123456789abcdef01234567","offset":1,"aof_size":2,"unknown":true}`,
		`{"version":1,"primary_address":"primary:6379","replication_id":"0123456789abcdef0123456789abcdef01234567","offset":1,"aof_size":2} {}`,
	} {
		path := filepath.Join(t.TempDir(), "replica.state")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		if _, err := LoadState(path); !errors.Is(err, ErrInvalidReplicationState) {
			t.Fatalf("LoadState(%q) error = %v, want ErrInvalidReplicationState", contents, err)
		}
	}
}

func TestPersistentStateValidation(t *testing.T) {
	t.Parallel()

	valid := PersistentState{
		Version:        replicationStateVersion,
		PrimaryAddress: "primary:6379",
		ReplicationID:  testReplicationID,
		Offset:         1,
		AOFSize:        2,
	}
	tests := []PersistentState{
		{},
		withState(valid, func(state *PersistentState) { state.Version = 2 }),
		withState(valid, func(state *PersistentState) { state.PrimaryAddress = "" }),
		withState(valid, func(state *PersistentState) { state.ReplicationID = "bad" }),
		withState(valid, func(state *PersistentState) { state.Offset = -1 }),
		withState(valid, func(state *PersistentState) { state.AOFSize = -1 }),
	}
	for _, state := range tests {
		if err := state.Validate(); !errors.Is(err, ErrInvalidReplicationState) {
			t.Errorf("Validate(%#v) error = %v", state, err)
		}
	}
}

func withState(state PersistentState, update func(*PersistentState)) PersistentState {
	update(&state)
	return state
}
