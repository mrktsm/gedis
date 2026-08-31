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

func assertInfoFields(t *testing.T, info string, want map[string]string) {
	t.Helper()
	if !strings.HasPrefix(info, "# Persistence\r\n") {
		t.Fatalf("INFO = %q, want Persistence section header", info)
	}
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
