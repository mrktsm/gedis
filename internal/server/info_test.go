package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/store"
)

func TestInfoPersistenceForDisabledAOF(t *testing.T) {
	t.Parallel()

	response := NewEngine().Execute(stringsToBytes([]string{"INFO", "persistence"})).Response
	if response.Kind() != resp.KindBulkString {
		t.Fatalf("INFO response kind = %q, want bulk string", response.Kind())
	}
	assertInfoFields(t, string(response.Bytes()), map[string]string{
		"loading":                     "0",
		"aof_enabled":                 "0",
		"aof_rewrite_in_progress":     "0",
		"aof_last_bgrewrite_status":   "ok",
		"aof_rewrites":                "0",
		"aof_current_size":            "0",
		"gedis_aof_sync_policy":       "disabled",
		"gedis_aof_last_rewrite_keys": "0",
	})
}

func TestInfoPersistenceUsesSinkAndRewriteStatus(t *testing.T) {
	t.Parallel()

	sink := &infoRewriteSink{policy: "everysec", size: 4096}
	engine := NewEngineWithStoreAndSink(store.New(), sink)
	if _, err := engine.RewriteAOF(); err != nil {
		t.Fatalf("RewriteAOF() error = %v", err)
	}
	response := engine.Execute(stringsToBytes([]string{"INFO"})).Response
	assertInfoFields(t, string(response.Bytes()), map[string]string{
		"aof_enabled":                 "1",
		"aof_last_bgrewrite_status":   "ok",
		"aof_current_size":            "4096",
		"gedis_aof_sync_policy":       "everysec",
		"gedis_aof_last_rewrite_keys": "0",
	})
}

func TestInfoUnknownSectionIsEmpty(t *testing.T) {
	t.Parallel()

	response := NewEngine().Execute(stringsToBytes([]string{"INFO", "not-a-section"})).Response
	if response.Kind() != resp.KindBulkString || len(response.Bytes()) != 0 {
		t.Fatalf("INFO unknown response = kind %q, %q; want empty bulk string", response.Kind(), response.Bytes())
	}
}

func TestInfoReportsInspectionFailure(t *testing.T) {
	t.Parallel()

	sink := &infoRewriteSink{infoError: errors.New("stat failed")}
	response := NewEngineWithStoreAndSink(store.New(), sink).Execute(stringsToBytes([]string{"INFO"})).Response
	if response.Kind() != resp.KindError {
		t.Fatalf("INFO response = kind %q, %q; want error", response.Kind(), response.Bytes())
	}
}

func TestInfoReplicationReportsPrimaryBacklog(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	engine.SetReplicationInfoProvider(staticReplicationInfo{info: ReplicationInfo{
		Role:                   "master",
		ReplicationID:          "0123456789abcdef0123456789abcdef01234567",
		Offset:                 123,
		ConnectedReplicas:      2,
		BacklogActive:          true,
		BacklogSize:            1024,
		BacklogFirstByteOffset: 24,
		BacklogHistoryLength:   100,
	}})
	response := engine.Execute(stringsToBytes([]string{"INFO", "replication"})).Response
	if !strings.HasPrefix(string(response.Bytes()), "# Replication\r\n") {
		t.Fatalf("INFO replication = %q", response.Bytes())
	}
	assertInfoFieldValues(t, string(response.Bytes()), map[string]string{
		"role":                           "master",
		"connected_slaves":               "2",
		"master_replid":                  "0123456789abcdef0123456789abcdef01234567",
		"master_repl_offset":             "123",
		"repl_backlog_active":            "1",
		"repl_backlog_size":              "1024",
		"repl_backlog_first_byte_offset": "24",
		"repl_backlog_histlen":           "100",
	})
}

func TestInfoReplicationReportsReplicaLinkAndGedisCounters(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	engine.SetReplicationInfoProvider(staticReplicationInfo{info: ReplicationInfo{
		Role:                  "slave",
		PrimaryHost:           "primary.example",
		PrimaryPort:           6379,
		PrimaryLinkUp:         true,
		ReplicaReadOffset:     456,
		ReplicaAppliedOffset:  456,
		ReplicaReadOnly:       true,
		UpstreamReplicationID: "abcdef0123456789abcdef0123456789abcdef01",
		FullSyncs:             1,
		PartialSyncs:          2,
		Reconnects:            3,
		LastError:             "line one\r\nline two",
	}})
	response := engine.Execute(stringsToBytes([]string{"INFO", "replication"})).Response
	assertInfoFieldValues(t, string(response.Bytes()), map[string]string{
		"role":                            "slave",
		"master_host":                     "primary.example",
		"master_port":                     "6379",
		"master_link_status":              "up",
		"master_sync_in_progress":         "0",
		"slave_read_repl_offset":          "456",
		"slave_repl_offset":               "456",
		"slave_read_only":                 "1",
		"gedis_upstream_replid":           "abcdef0123456789abcdef0123456789abcdef01",
		"gedis_replication_full_syncs":    "1",
		"gedis_replication_partial_syncs": "2",
		"gedis_replication_reconnects":    "3",
		"gedis_replication_last_error":    "line one\\r\\nline two",
	})
}

func TestInfoCanSelectMultipleSections(t *testing.T) {
	t.Parallel()

	response := NewEngine().Execute(stringsToBytes([]string{"INFO", "persistence", "replication"})).Response
	text := string(response.Bytes())
	if !strings.Contains(text, "# Persistence\r\n") || !strings.Contains(text, "\r\n# Replication\r\n") {
		t.Fatalf("INFO sections = %q", text)
	}
}

func TestInfoClientsAndStatsUseRuntimeProvider(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	engine.setRuntimeInfoProvider(staticRuntimeInfo{info: RuntimeInfo{
		ConnectedClients:  4,
		ConnectedReplicas: 2,
		TotalConnections:  12,
		TotalCommands:     34,
		CommandErrors:     5,
		ProtocolErrors:    1,
	}})
	response := engine.Execute(stringsToBytes([]string{"INFO", "clients", "stats"})).Response
	text := string(response.Bytes())
	if !strings.HasPrefix(text, "# Clients\r\n") || !strings.Contains(text, "\r\n# Stats\r\n") {
		t.Fatalf("INFO clients stats = %q", text)
	}
	assertInfoFieldValues(t, text, map[string]string{
		"connected_clients":                   "4",
		"gedis_connected_replica_connections": "2",
		"gedis_connected_connections":         "6",
		"total_connections_received":          "12",
		"total_commands_processed":            "34",
		"gedis_command_errors":                "5",
		"gedis_protocol_errors":               "1",
	})
}

func assertInfoFields(t *testing.T, info string, want map[string]string) {
	t.Helper()
	if !strings.HasPrefix(info, "# Persistence\r\n") {
		t.Fatalf("INFO = %q, want Persistence section header", info)
	}
	assertInfoFieldValues(t, info, want)
}

func assertInfoFieldValues(t *testing.T, info string, want map[string]string) {
	t.Helper()
	got := make(map[string]string)
	for _, line := range strings.Split(info, "\r\n") {
		name, value, found := strings.Cut(line, ":")
		if found {
			got[name] = value
		}
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("INFO field %s = %q, want %q", name, got[name], value)
		}
	}
}

type staticReplicationInfo struct {
	info ReplicationInfo
}

func (s staticReplicationInfo) ReplicationInfo() ReplicationInfo {
	return s.info
}

type staticRuntimeInfo struct {
	info RuntimeInfo
}

func (s staticRuntimeInfo) RuntimeInfo() RuntimeInfo {
	return s.info
}

type infoRewriteSink struct {
	policy    string
	size      int64
	infoError error
}

func (s *infoRewriteSink) Append(_ [][]byte) error {
	return nil
}

func (s *infoRewriteSink) Rewrite(_ [][][]byte) error {
	return nil
}

func (s *infoRewriteSink) AOFInfo() (bool, string, int64, error) {
	return true, s.policy, s.size, s.infoError
}
