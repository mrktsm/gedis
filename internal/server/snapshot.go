package server

import (
	"errors"
	"fmt"

	"github.com/mrktsm/gedis/internal/snapshot"
	"github.com/mrktsm/gedis/internal/store"
)

var ErrNilSnapshotCallback = errors.New("snapshot callback is required")

// CaptureSnapshot holds the mutation gate while copying live state and running
// a short coordination callback. Replication uses the callback to pair that
// state with an offset and live subscription before writes resume.
func (e *Engine) CaptureSnapshot(callback func([]store.SnapshotEntry) error) error {
	if callback == nil {
		return ErrNilSnapshotCallback
	}
	e.writeMutex.Lock()
	defer e.writeMutex.Unlock()
	if e.persistenceError != nil {
		return fmt.Errorf("persistence is unavailable: %w", e.persistenceError)
	}
	return callback(e.keyspace.Snapshot())
}

// InstallSnapshot atomically replaces live state under the mutation gate. When
// AOF is enabled, it rewrites persistence to exactly the installed state before
// later replication commands can apply.
func (e *Engine) InstallSnapshot(entries []store.SnapshotEntry) error {
	e.writeMutex.Lock()
	defer e.writeMutex.Unlock()
	if e.persistenceError != nil {
		return fmt.Errorf("persistence is unavailable: %w", e.persistenceError)
	}
	if err := e.keyspace.Restore(entries); err != nil {
		return err
	}
	if e.mutationSink == nil {
		return nil
	}
	if provider, ok := e.mutationSink.(aofInfoProvider); ok {
		enabled, _, _, err := provider.AOFInfo()
		if err != nil {
			e.persistenceError = err
			return err
		}
		if !enabled {
			return nil
		}
	}
	rewriter, ok := e.mutationSink.(mutationRewriter)
	if !ok {
		e.persistenceError = ErrRewriteUnsupported
		return ErrRewriteUnsupported
	}
	installed := e.keyspace.Snapshot()
	if err := rewriter.Rewrite(snapshot.Commands(installed)); err != nil {
		e.persistenceError = err
		return fmt.Errorf("rewrite installed snapshot: %w", err)
	}
	return nil
}
