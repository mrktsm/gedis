package server

import (
	"strconv"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
)

type aofInfoProvider interface {
	AOFInfo() (enabled bool, syncPolicy string, currentSize int64, err error)
}

func (e *Engine) handleInfo(arguments [][]byte) Result {
	if !requestsInfoSection(arguments, "PERSISTENCE") {
		return Result{Response: resp.BulkStringString("")}
	}

	rewrite := e.AOFRewriteStatus()
	enabled := e.mutationSink != nil
	policy := "unknown"
	size := int64(0)
	if provider, ok := e.mutationSink.(aofInfoProvider); ok {
		var err error
		enabled, policy, size, err = provider.AOFInfo()
		if err != nil {
			return Result{Response: resp.Error("ERR failed to inspect append only persistence")}
		}
	} else if !enabled {
		policy = "disabled"
	}

	lastStatus := "ok"
	if rewrite.LastError != nil {
		lastStatus = "err"
	}
	var info strings.Builder
	info.WriteString("# Persistence\r\n")
	writeInfoField(&info, "loading", "0")
	writeInfoField(&info, "aof_enabled", boolDigit(enabled))
	writeInfoField(&info, "aof_rewrite_in_progress", boolDigit(rewrite.Running))
	writeInfoField(&info, "aof_last_bgrewrite_status", lastStatus)
	writeInfoField(&info, "aof_rewrites", strconv.FormatInt(rewrite.Completed, 10))
	writeInfoField(&info, "aof_current_size", strconv.FormatInt(size, 10))
	writeInfoField(&info, "gedis_aof_sync_policy", policy)
	writeInfoField(&info, "gedis_aof_last_rewrite_keys", strconv.Itoa(rewrite.LastKeys))
	return Result{Response: resp.BulkStringString(info.String())}
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

func writeInfoField(info *strings.Builder, name, value string) {
	info.WriteString(name)
	info.WriteByte(':')
	info.WriteString(value)
	info.WriteString("\r\n")
}

func boolDigit(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
