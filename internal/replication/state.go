package replication

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const replicationStateVersion = 1

var ErrInvalidReplicationState = errors.New("invalid replication state")

type PersistentState struct {
	Version        int    `json:"version"`
	PrimaryAddress string `json:"primary_address"`
	ReplicationID  string `json:"replication_id"`
	Offset         int64  `json:"offset"`
	AOFSize        int64  `json:"aof_size"`
}

func (s PersistentState) Validate() error {
	switch {
	case s.Version != replicationStateVersion:
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidReplicationState, s.Version)
	case s.PrimaryAddress == "":
		return fmt.Errorf("%w: primary address is empty", ErrInvalidReplicationState)
	case !validReplicationID(s.ReplicationID):
		return fmt.Errorf("%w: malformed replication ID", ErrInvalidReplicationState)
	case s.Offset < 0:
		return fmt.Errorf("%w: negative offset", ErrInvalidReplicationState)
	case s.AOFSize < 0:
		return fmt.Errorf("%w: negative AOF size", ErrInvalidReplicationState)
	default:
		return nil
	}
}

func LoadState(path string) (PersistentState, error) {
	if path == "" {
		return PersistentState{}, fmt.Errorf("%w: path is empty", ErrInvalidReplicationState)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PersistentState{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state PersistentState
	if err := decoder.Decode(&state); err != nil {
		return PersistentState{}, fmt.Errorf("%w: decode: %v", ErrInvalidReplicationState, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PersistentState{}, err
	}
	if err := state.Validate(); err != nil {
		return PersistentState{}, err
	}
	return state, nil
}

// SaveState fsyncs a same-directory temporary file, atomically renames it, and
// fsyncs the directory so a clean checkpoint cannot expose partial JSON.
func SaveState(path string, state PersistentState) error {
	if path == "" {
		return fmt.Errorf("%w: path is empty", ErrInvalidReplicationState)
	}
	if err := state.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("replication: encode state: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("replication: create state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("replication: create state file: %w", err)
	}
	temporaryPath := temporary.Name()
	renamed := false
	defer func() {
		_ = temporary.Close()
		if !renamed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("replication: chmod state file: %w", err)
	}
	if err := writeStateBytes(temporary, data); err != nil {
		return fmt.Errorf("replication: write state file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("replication: sync state file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("replication: close state file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replication: replace state file: %w", err)
	}
	renamed = true
	if err := syncStateDirectory(directory); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("%w: trailing JSON value", ErrInvalidReplicationState)
	}
	return fmt.Errorf("%w: trailing data: %v", ErrInvalidReplicationState, err)
}

func writeStateBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func syncStateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("replication: open state directory for sync: %w", err)
	}
	syncError := directory.Sync()
	closeError := directory.Close()
	if syncError != nil {
		return fmt.Errorf("replication: sync state directory: %w", syncError)
	}
	if closeError != nil {
		return fmt.Errorf("replication: close state directory: %w", closeError)
	}
	return nil
}
