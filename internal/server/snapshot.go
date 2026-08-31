package server

import (
	"errors"
	"fmt"

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
