package server

import (
	"strconv"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
)

type aofInfoProvider interface {
	AOFInfo() (enabled bool, syncPolicy string, currentSize int64, err error)
}

type replicationInfoProvider interface {
	ReplicationInfo() ReplicationInfo
}

// ReplicationInfo contains only state with a defined meaning in Gedis. Redis
// field names are used where semantics align; Gedis-only evidence is prefixed.
type ReplicationInfo struct {
	Role                   string
	PrimaryHost            string
	PrimaryPort            int
	PrimaryLinkUp          bool
	PrimarySyncInProgress  bool
	ReplicaReadOffset      int64
	ReplicaAppliedOffset   int64
	ReplicaReadOnly        bool
	ReplicationID          string
	Offset                 int64
	ConnectedReplicas      int
	BacklogActive          bool
	BacklogSize            int
	BacklogFirstByteOffset int64
	BacklogHistoryLength   int
	UpstreamReplicationID  string
	FullSyncs              int64
	PartialSyncs           int64
	Reconnects             int64
	LastError              string
}

func (e *Engine) handleInfo(arguments [][]byte) Result {
	var info strings.Builder
	if requestsInfoSection(arguments, "PERSISTENCE") {
		if err := e.writePersistenceInfo(&info); err != nil {
			return Result{Response: resp.Error("ERR failed to inspect append only persistence")}
		}
	}
	if requestsInfoSection(arguments, "REPLICATION") {
		writeInfoSeparator(&info)
		e.writeReplicationInfo(&info)
	}
	return Result{Response: resp.BulkStringString(info.String())}
}

func (e *Engine) writePersistenceInfo(info *strings.Builder) error {
	rewrite := e.AOFRewriteStatus()
	enabled := e.mutationSink != nil
	policy := "unknown"
	size := int64(0)
	if provider, ok := e.mutationSink.(aofInfoProvider); ok {
		var err error
		enabled, policy, size, err = provider.AOFInfo()
		if err != nil {
			return err
		}
	} else if !enabled {
		policy = "disabled"
	}

	lastStatus := "ok"
	if rewrite.LastError != nil {
		lastStatus = "err"
	}
	info.WriteString("# Persistence\r\n")
	writeInfoField(info, "loading", "0")
	writeInfoField(info, "aof_enabled", boolDigit(enabled))
	writeInfoField(info, "aof_rewrite_in_progress", boolDigit(rewrite.Running))
	writeInfoField(info, "aof_last_bgrewrite_status", lastStatus)
	writeInfoField(info, "aof_rewrites", strconv.FormatInt(rewrite.Completed, 10))
	writeInfoField(info, "aof_current_size", strconv.FormatInt(size, 10))
	writeInfoField(info, "gedis_aof_sync_policy", policy)
	writeInfoField(info, "gedis_aof_last_rewrite_keys", strconv.Itoa(rewrite.LastKeys))
	return nil
}

func (e *Engine) writeReplicationInfo(info *strings.Builder) {
	e.replicationMutex.RLock()
	provider := e.replicationInfo
	e.replicationMutex.RUnlock()

	state := ReplicationInfo{Role: "master"}
	if provider != nil {
		state = provider.ReplicationInfo()
	}
	if state.Role != "slave" {
		state.Role = "master"
	}
	info.WriteString("# Replication\r\n")
	writeInfoField(info, "role", state.Role)
	if state.Role == "slave" {
		writeInfoField(info, "master_host", state.PrimaryHost)
		writeInfoField(info, "master_port", strconv.Itoa(state.PrimaryPort))
		linkStatus := "down"
		if state.PrimaryLinkUp {
			linkStatus = "up"
		}
		writeInfoField(info, "master_link_status", linkStatus)
		writeInfoField(info, "master_sync_in_progress", boolDigit(state.PrimarySyncInProgress))
		writeInfoField(info, "slave_read_repl_offset", strconv.FormatInt(state.ReplicaReadOffset, 10))
		writeInfoField(info, "slave_repl_offset", strconv.FormatInt(state.ReplicaAppliedOffset, 10))
		writeInfoField(info, "slave_read_only", boolDigit(state.ReplicaReadOnly))
	}
	writeInfoField(info, "connected_slaves", strconv.Itoa(state.ConnectedReplicas))
	if state.ReplicationID != "" {
		writeInfoField(info, "master_replid", state.ReplicationID)
	}
	writeInfoField(info, "master_repl_offset", strconv.FormatInt(state.Offset, 10))
	writeInfoField(info, "repl_backlog_active", boolDigit(state.BacklogActive))
	writeInfoField(info, "repl_backlog_size", strconv.Itoa(state.BacklogSize))
	writeInfoField(info, "repl_backlog_first_byte_offset", strconv.FormatInt(state.BacklogFirstByteOffset, 10))
	writeInfoField(info, "repl_backlog_histlen", strconv.Itoa(state.BacklogHistoryLength))
	if state.Role == "slave" {
		writeInfoField(info, "gedis_upstream_replid", state.UpstreamReplicationID)
		writeInfoField(info, "gedis_replication_full_syncs", strconv.FormatInt(state.FullSyncs, 10))
		writeInfoField(info, "gedis_replication_partial_syncs", strconv.FormatInt(state.PartialSyncs, 10))
		writeInfoField(info, "gedis_replication_reconnects", strconv.FormatInt(state.Reconnects, 10))
		if state.LastError != "" {
			writeInfoField(info, "gedis_replication_last_error", sanitizeInfoValue(state.LastError))
		}
	}
}

func requestsInfoSection(arguments [][]byte, section string) bool {
	if len(arguments) == 0 {
		return true
	}
	for _, argument := range arguments {
		switch strings.ToUpper(string(argument)) {
		case section, "ALL", "DEFAULT":
			return true
		}
	}
	return false
}

func writeInfoSeparator(info *strings.Builder) {
	if info.Len() > 0 {
		info.WriteString("\r\n")
	}
}

func writeInfoField(info *strings.Builder, name, value string) {
	info.WriteString(name)
	info.WriteByte(':')
	info.WriteString(value)
	info.WriteString("\r\n")
}

func sanitizeInfoValue(value string) string {
	return strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(value)
}

func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
