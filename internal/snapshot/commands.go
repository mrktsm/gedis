package snapshot

import (
	"math"
	"strconv"
	"time"

	"github.com/mrktsm/gedis/internal/store"
)

// Commands converts a deterministic keyspace snapshot into the minimum
// canonical mutation sequence shared by AOF rewrite and full replication sync.
func Commands(entries []store.SnapshotEntry) [][][]byte {
	commands := make([][][]byte, 0, len(entries)*2)
	for _, entry := range entries {
		key := []byte(entry.Key)
		switch entry.Kind {
		case store.KindString:
			command := [][]byte{[]byte("SET"), key, cloneBytes(entry.StringValue)}
			if !entry.ExpiresAt.IsZero() {
				command = append(
					command,
					[]byte("PXAT"),
					[]byte(strconv.FormatInt(entry.ExpiresAt.UnixMilli(), 10)),
				)
			}
			commands = append(commands, command)
		case store.KindSortedSet:
			if len(entry.SortedSet) == 0 {
				continue
			}
			command := make([][]byte, 0, 2+len(entry.SortedSet)*2)
			command = append(command, []byte("ZADD"), key)
			for _, item := range entry.SortedSet {
				command = append(command, []byte(formatScore(item.Score)), []byte(item.Member))
			}
			commands = append(commands, command)
			if !entry.ExpiresAt.IsZero() {
				commands = append(commands, expireAtCommand(key, entry.ExpiresAt))
			}
		}
	}
	return commands
}

func expireAtCommand(key []byte, expiresAt time.Time) [][]byte {
	return [][]byte{
		[]byte("PEXPIREAT"),
		cloneBytes(key),
		[]byte(strconv.FormatInt(expiresAt.UnixMilli(), 10)),
	}
}

func formatScore(score float64) string {
	switch {
	case math.IsInf(score, 1):
		return "inf"
	case math.IsInf(score, -1):
		return "-inf"
	default:
		return strconv.FormatFloat(score, 'g', -1, 64)
	}
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}
