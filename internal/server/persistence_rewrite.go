package server

import (
	"errors"
	"fmt"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/snapshot"
)

var (
	ErrPersistenceDisabled = errors.New("append-only persistence is not enabled")
	ErrRewriteUnsupported  = errors.New("persistence sink does not support rewrite")
	ErrRewriteInProgress   = errors.New("append-only file rewrite is already in progress")
)

type mutationRewriter interface {
	Rewrite(commands [][][]byte) error
}

type AOFRewriteStatus struct {
	Running   bool
	LastError error
	LastKeys  int
	Completed int64
}

// RewriteAOF replaces persistence history with the minimum canonical command
// sequence for the current keyspace. The write gate makes snapshot creation
// and file replacement atomic with respect to live mutations.
func (e *Engine) RewriteAOF() (int, error) {
	e.writeMutex.Lock()
	defer e.writeMutex.Unlock()

	rewriter, err := e.rewriterLocked()
	if err != nil {
		return 0, err
	}

	keyspaceSnapshot := e.keyspace.Snapshot()
	if err := rewriter.Rewrite(snapshot.Commands(keyspaceSnapshot)); err != nil {
		e.persistenceError = err
		return 0, fmt.Errorf("rewrite append-only file: %w", err)
	}
	return len(keyspaceSnapshot), nil
}

// StartAOFRewrite runs a rewrite asynchronously, matching BGREWRITEAOF's
// command-level behavior. Writes pause while the snapshot is serialized, while
// reads continue after the keyspace snapshot has been copied.
func (e *Engine) StartAOFRewrite() error {
	e.rewriteMutex.Lock()
	defer e.rewriteMutex.Unlock()
	if e.rewriteRunning {
		return ErrRewriteInProgress
	}

	e.writeMutex.Lock()
	_, err := e.rewriterLocked()
	e.writeMutex.Unlock()
	if err != nil {
		return err
	}

	e.rewriteRunning = true
	e.rewriteLastError = nil
	e.rewriteWait.Add(1)
	go func() {
		defer e.rewriteWait.Done()
		keys, err := e.RewriteAOF()

		e.rewriteMutex.Lock()
		e.rewriteRunning = false
		e.rewriteLastError = err
		if err == nil {
			e.rewriteLastKeys = keys
			e.rewriteCompleted++
		}
		e.rewriteMutex.Unlock()
	}()
	return nil
}

func (e *Engine) AOFRewriteStatus() AOFRewriteStatus {
	e.rewriteMutex.Lock()
	defer e.rewriteMutex.Unlock()
	return AOFRewriteStatus{
		Running:   e.rewriteRunning,
		LastError: e.rewriteLastError,
		LastKeys:  e.rewriteLastKeys,
		Completed: e.rewriteCompleted,
	}
}

// WaitForAOFRewrite is used during process shutdown after client intake has
// stopped, ensuring the persistence file is not closed under a rewrite.
func (e *Engine) WaitForAOFRewrite() {
	e.rewriteWait.Wait()
}

func (e *Engine) handleBGRewriteAOF(arguments [][]byte) Result {
	if len(arguments) != 0 {
		return wrongArity("bgrewriteaof")
	}
	err := e.StartAOFRewrite()
	switch {
	case err == nil:
		return Result{Response: resp.SimpleString("Background append only file rewriting started")}
	case errors.Is(err, ErrRewriteInProgress):
		return Result{Response: resp.Error("ERR Background append only file rewriting already in progress")}
	case errors.Is(err, ErrPersistenceDisabled), errors.Is(err, ErrRewriteUnsupported):
		return Result{Response: resp.Error("ERR append only persistence is not enabled")}
	default:
		return persistenceFailure(err)
	}
}

func (e *Engine) rewriterLocked() (mutationRewriter, error) {
	if e.persistenceError != nil {
		return nil, fmt.Errorf("persistence is unavailable: %w", e.persistenceError)
	}
	if e.mutationSink == nil {
		return nil, ErrPersistenceDisabled
	}
	if provider, ok := e.mutationSink.(aofInfoProvider); ok {
		enabled, _, _, err := provider.AOFInfo()
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, ErrPersistenceDisabled
		}
	}
	rewriter, ok := e.mutationSink.(mutationRewriter)
	if !ok {
		return nil, ErrRewriteUnsupported
	}
	return rewriter, nil
}
