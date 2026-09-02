package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/aof"
	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

func TestParseOptionsDefaults(t *testing.T) {
	t.Parallel()

	got, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	want := options{
		address:               "127.0.0.1:6379",
		writeTimeout:          5 * time.Second,
		maxBulkLength:         resp.DefaultLimits.MaxBulkLength,
		maxArrayLength:        resp.DefaultLimits.MaxArrayLength,
		expireInterval:        100 * time.Millisecond,
		aofPath:               "data/appendonly.aof",
		aofSyncPolicy:         aof.SyncEverySecond,
		replBacklogBytes:      1024 * 1024,
		replSubscriberQueue:   256,
		replicaSyncTimeout:    10 * time.Second,
		replicaDialTimeout:    5 * time.Second,
		replicaReconnectDelay: 250 * time.Millisecond,
		replMaxSnapshotBytes:  256 * 1024 * 1024,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOptions() = %#v, want %#v", got, want)
	}
}

func TestParseOptionsOverrides(t *testing.T) {
	t.Parallel()

	got, err := parseOptions([]string{
		"-addr", "localhost:1234",
		"-read-timeout", "2s",
		"-write-timeout", "3s",
		"-max-bulk-bytes", "2048",
		"-max-array-length", "32",
		"-expire-interval", "250ms",
		"-appendonly",
		"-aof-path", "state/gedis.aof",
		"-appendfsync", "always",
		"-aof-repair-truncated",
		"-repl-backlog-bytes", "4096",
		"-repl-subscriber-queue", "8",
		"-replicaof", "primary.example:6379",
		"-replica-state-path", "state/replica.json",
		"-replica-sync-timeout", "4s",
		"-replica-dial-timeout", "2s",
		"-replica-reconnect-delay", "50ms",
		"-repl-max-snapshot-bytes", "1048576",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions() error = %v", err)
	}
	want := options{
		address:               "localhost:1234",
		readTimeout:           2 * time.Second,
		writeTimeout:          3 * time.Second,
		maxBulkLength:         2048,
		maxArrayLength:        32,
		expireInterval:        250 * time.Millisecond,
		appendOnly:            true,
		aofPath:               "state/gedis.aof",
		aofSyncPolicy:         aof.SyncAlways,
		repairAOF:             true,
		replBacklogBytes:      4096,
		replSubscriberQueue:   8,
		replicaOf:             "primary.example:6379",
		replicaStatePath:      "state/replica.json",
		replicaSyncTimeout:    4 * time.Second,
		replicaDialTimeout:    2 * time.Second,
		replicaReconnectDelay: 50 * time.Millisecond,
		replMaxSnapshotBytes:  1048576,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOptions() = %#v, want %#v", got, want)
	}
}

func TestParseOptionsRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"positional"},
		{"-addr", ""},
		{"-read-timeout", "-1s"},
		{"-write-timeout", "-1s"},
		{"-max-bulk-bytes", "0"},
		{"-max-array-length", "-1"},
		{"-expire-interval", "0"},
		{"-appendfsync", "sometimes"},
		{"-appendonly", "-aof-path", ""},
		{"-repl-backlog-bytes", "0"},
		{"-repl-subscriber-queue", "-1"},
		{"-replica-sync-timeout", "0"},
		{"-replica-dial-timeout", "-1s"},
		{"-replica-reconnect-delay", "0"},
		{"-repl-max-snapshot-bytes", "0"},
		{"-replicaof", "missing-port"},
		{"-replicaof", ":6379"},
		{"-replicaof", "primary:70000"},
		{"-replica-state-path", "state/replica.json"},
		{"-replicaof", "primary:6379", "-replica-state-path", "state/replica.json"},
	} {
		if _, err := parseOptions(arguments, io.Discard); err == nil {
			t.Fatalf("parseOptions(%q) error = nil, want error", arguments)
		}
	}
}

func TestRecoverAOFRestoresMixedData(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	appendLog, err := aof.Open(aof.Config{Path: path, SyncPolicy: aof.SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	live := server.NewEngineWithStoreAndSink(store.New(), appendLog)
	for _, command := range [][]string{
		{"SET", "message", "durable"},
		{"INCRBY", "counter", "42"},
		{"SET", "future", "value", "PXAT", "4102444800000"},
		{"ZADD", "leaders", "10", "alpha", "20", "bravo"},
	} {
		if response := executeStrings(live, command); response.Kind() == resp.KindError {
			t.Fatalf("Execute(%q) = %q", command, response.Bytes())
		}
	}
	if err := appendLog.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	recoveredStore := store.New()
	result, repaired, err := recoverAOF(path, recoveredStore, false)
	if err != nil {
		t.Fatalf("recoverAOF() error = %v", err)
	}
	if result.Commands != 4 || repaired {
		t.Fatalf("recoverAOF() = %#v, repaired %t; want 4 commands, unrepaired", result, repaired)
	}
	recovered := server.NewEngineWithStore(recoveredStore)
	assertMainResponse(t, recovered, []string{"GET", "message"}, resp.BulkStringString("durable"))
	assertMainResponse(t, recovered, []string{"GET", "counter"}, resp.BulkStringString("42"))
	remaining := executeStrings(recovered, []string{"PTTL", "future"})
	if remaining.Kind() != resp.KindInteger || remaining.Int64() <= 0 {
		t.Fatalf("PTTL future = %#v, want a positive integer", remaining)
	}
	assertMainResponse(t, recovered, []string{"ZRANGE", "leaders", "0", "-1"}, resp.Array(
		resp.BulkStringString("alpha"),
		resp.BulkStringString("bravo"),
	))
}

func TestRecoverAOFRequiresOptInForTruncatedTail(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	complete := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	truncated := "*2\r\n$4\r\nINCR\r\n$7\r\ncoun"
	if err := os.WriteFile(path, []byte(complete+truncated), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, _, err := recoverAOF(path, store.New(), false); err == nil {
		t.Fatal("recoverAOF(repair=false) error = nil, want truncated-tail error")
	}
	recoveredStore := store.New()
	result, repaired, err := recoverAOF(path, recoveredStore, true)
	if err != nil {
		t.Fatalf("recoverAOF(repair=true) error = %v", err)
	}
	if result.Commands != 1 || !repaired {
		t.Fatalf("recoverAOF(repair=true) = %#v, repaired %t", result, repaired)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) != complete {
		t.Fatalf("repaired AOF = %q, want %q", contents, complete)
	}
	assertMainResponse(t, server.NewEngineWithStore(recoveredStore), []string{"GET", "key"}, resp.BulkStringString("value"))
}

func TestRecoverAOFRejectsCommandErrors(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "appendonly.aof")
	appendLog, err := aof.Open(aof.Config{Path: path, SyncPolicy: aof.SyncAlways})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := appendLog.Append([][]byte{[]byte("NOTACOMMAND")}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := appendLog.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, _, err = recoverAOF(path, store.New(), true)
	var replayError *aof.ReplayError
	if !errors.As(err, &replayError) || replayError.Truncated {
		t.Fatalf("recoverAOF() error = %v, want non-truncated ReplayError", err)
	}
}

func executeStrings(engine *server.Engine, command []string) resp.Value {
	encoded := make([][]byte, len(command))
	for index, argument := range command {
		encoded[index] = []byte(argument)
	}
	return engine.Execute(encoded).Response
}

func assertMainResponse(t *testing.T, engine *server.Engine, command []string, want resp.Value) {
	t.Helper()
	got := executeStrings(engine, command)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Execute(%q) = %#v, want %#v", command, got, want)
	}
}
