package aof

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRewriteAtomicallyReplacesHistoryAndContinuesAppending(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for range 100 {
		if err := log.Append(command("SET", "key", "obsolete")); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	compact := [][][]byte{
		command("SET", "key", "current"),
		command("ZADD", "leaders", "10", "alpha", "20", "bravo"),
	}
	if err := log.Rewrite(compact); err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}
	if err := log.Append(command("INCR", "counter")); err != nil {
		t.Fatalf("Append(after Rewrite) error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var got [][][]byte
	result, err := ReplayFile(path, func(replayed [][]byte) error {
		got = append(got, cloneCommand(replayed))
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayFile() error = %v", err)
	}
	want := append(compact, command("INCR", "counter"))
	if result.Commands != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed = %q (%#v), want %q", got, result, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("AOF mode = %o, want 600", info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".appendonly.aof.rewrite-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("rewrite temporary files = %q, error %v", matches, err)
	}
}

func TestRewriteCanProduceAnEmptyLog(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncNever})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := log.Append(command("SET", "key", "value")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := log.Rewrite(nil); err != nil {
		t.Fatalf("Rewrite(nil) error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if len(contents) != 0 {
		t.Fatalf("rewritten AOF = %q, want empty", contents)
	}
}

func TestRewriteAfterCloseFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	log, err := Open(Config{Path: path, SyncPolicy: SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := log.Rewrite(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Rewrite(after Close) error = %v, want ErrClosed", err)
	}
}

func command(arguments ...string) [][]byte {
	result := make([][]byte, len(arguments))
	for index, argument := range arguments {
		result[index] = []byte(argument)
	}
	return result
}

func cloneCommand(command [][]byte) [][]byte {
	cloned := make([][]byte, len(command))
	for index, argument := range command {
		cloned[index] = append([]byte(nil), argument...)
	}
	return cloned
}
