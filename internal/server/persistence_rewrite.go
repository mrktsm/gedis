package server

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/mrktsm/gedis/internal/store"
)

var (
	ErrPersistenceDisabled = errors.New("append-only persistence is not enabled")
	ErrRewriteUnsupported  = errors.New("persistence sink does not support rewrite")
)

type mutationRewriter interface {
	Rewrite(commands [][][]byte) error
}

// RewriteAOF replaces persistence history with the minimum canonical command
// sequence for the current keyspace. The write gate makes snapshot creation
// and file replacement atomic with respect to live mutations.
func (e *Engine) RewriteAOF() (int, error) {
	e.writeMutex.Lock()
	defer e.writeMutex.Unlock()

	if e.persistenceError != nil {
		return 0, fmt.Errorf("persistence is unavailable: %w", e.persistenceError)
	}
	if e.mutationSink == nil {
		return 0, ErrPersistenceDisabled
	}
	rewriter, ok := e.mutationSink.(mutationRewriter)
	if !ok {
		return 0, ErrRewriteUnsupported
	}

	snapshot := e.keyspace.Snapshot()
	if err := rewriter.Rewrite(snapshotMutations(snapshot)); err != nil {
		e.persistenceError = err
		return 0, fmt.Errorf("rewrite append-only file: %w", err)
	}
	return len(snapshot), nil
}

func snapshotMutations(snapshot []store.SnapshotEntry) [][][]byte {
	commands := make([][][]byte, 0, len(snapshot)*2)
	for _, entry := range snapshot {
		key := []byte(entry.Key)
		switch entry.Kind {
		case store.KindString:
			command := [][]byte{[]byte("SET"), key, entry.StringValue}
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
				commands = append(commands, canonicalExpireAtMutation(key, entry.ExpiresAt))
			}
		}
	}
	return commands
}
