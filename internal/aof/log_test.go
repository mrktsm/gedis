package aof

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestLogAppendUsesCanonicalRESP(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := log.Append([][]byte{[]byte("SET"), []byte("key"), []byte("value")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	if string(got) != want {
		t.Fatalf("AOF = %q, want %q", got, want)
	}
	if err := log.Append([][]byte{[]byte("PING")}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append(after Close) error = %v, want ErrClosed", err)
	}
}

func TestLogSerializesConcurrentAppends(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncNever})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	const writers = 32
	const commandsPerWriter = 50
	var waitGroup sync.WaitGroup
	waitGroup.Add(writers)
	for writer := 0; writer < writers; writer++ {
		go func() {
			defer waitGroup.Done()
			for command := 0; command < commandsPerWriter; command++ {
				if err := log.Append([][]byte{[]byte("INCR"), []byte("counter")}); err != nil {
					t.Errorf("Append() error = %v", err)
					return
				}
			}
		}()
	}
	waitGroup.Wait()
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	result, err := ReplayFile(path, nil)
	if err != nil {
		t.Fatalf("ReplayFile() error = %v", err)
	}
	if want := int64(writers * commandsPerWriter); result.Commands != want {
		t.Fatalf("ReplayFile().Commands = %d, want %d", result.Commands, want)
	}
}

func TestEverySecondPolicySyncLoopStops(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{
		Path:         path,
		SyncPolicy:   SyncEverySecond,
		SyncInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := log.Append([][]byte{[]byte("SET"), []byte("key"), []byte("value")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestDisabledLogDoesNotRequirePath(t *testing.T) {
	t.Parallel()

	log, err := Open(Config{SyncPolicy: SyncDisabled})
	if err != nil {
		t.Fatalf("Open(disabled) error = %v", err)
	}
	if err := log.Append([][]byte{[]byte("SET")}); err != nil {
		t.Fatalf("Append(disabled) error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close(disabled) error = %v", err)
	}
}

func TestAOFInfoReportsPolicyAndCurrentSize(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncNever})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	command := [][]byte{[]byte("SET"), []byte("key"), []byte("value")}
	if err := log.Append(command); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	want, _ := encodeCommand(command)
	enabled, policy, size, err := log.AOFInfo()
	if err != nil {
		t.Fatalf("AOFInfo() error = %v", err)
	}
	if !enabled || policy != string(SyncNever) || size != int64(len(want)) {
		t.Fatalf("AOFInfo() = %t, %q, %d; want true, %q, %d", enabled, policy, size, SyncNever, len(want))
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestReplayAppliesCommandsInOrder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	commands := [][][]byte{
		{[]byte("SET"), []byte("key"), []byte("value")},
		{[]byte("INCR"), []byte("counter")},
		{[]byte("DEL"), []byte("key")},
	}
	for _, command := range commands {
		if err := log.Append(command); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var applied [][][]byte
	result, err := ReplayFile(path, func(command [][]byte) error {
		applied = append(applied, command)
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayFile() error = %v", err)
	}
	if result.Commands != 3 || !reflect.DeepEqual(applied, commands) {
		t.Fatalf("ReplayFile() = %#v, commands %#v; want 3 and %#v", result, applied, commands)
	}
}

func TestReplayReportsAndRepairsTruncatedTail(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	complete, _ := encodeCommand([][]byte{[]byte("SET"), []byte("key"), []byte("value")})
	truncated := "*2\r\n$4\r\nINCR\r\n$7\r\ncoun"
	if err := os.WriteFile(path, append(complete, truncated...), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	result, err := ReplayFile(path, nil)
	var replayError *ReplayError
	if !errors.As(err, &replayError) || !replayError.Truncated {
		t.Fatalf("ReplayFile() error = %v, want truncated ReplayError", err)
	}
	if result.Commands != 1 || replayError.Offset != int64(len(complete)) {
		t.Fatalf("ReplayFile() = %#v, error %#v", result, replayError)
	}
	if err := RepairTruncatedFile(path, replayError); err != nil {
		t.Fatalf("RepairTruncatedFile() error = %v", err)
	}
	if result, err := ReplayFile(path, nil); err != nil || result.Commands != 1 {
		t.Fatalf("ReplayFile(after repair) = %#v, %v, want one command", result, err)
	}
}

func TestReplayRejectsCorruptionWithoutRepair(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	if err := os.WriteFile(path, []byte("?not-resp\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := ReplayFile(path, nil)
	var replayError *ReplayError
	if !errors.As(err, &replayError) || replayError.Truncated {
		t.Fatalf("ReplayFile() error = %v, want non-truncated ReplayError", err)
	}
	if err := RepairTruncatedFile(path, replayError); err == nil {
		t.Fatal("RepairTruncatedFile(corruption) error = nil, want error")
	}
}
