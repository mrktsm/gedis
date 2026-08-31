package aof

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrktsm/gedis/internal/resp"
)

const rewriteBufferSize = 64 * 1024

// Rewrite replaces the current log with commands that reconstruct the same
// logical state. The caller must serialize the snapshot with live mutations;
// Rewrite serializes only against Log operations and cannot infer commands that
// are absent from the supplied snapshot.
func (l *Log) Rewrite(commands [][][]byte) error {
	if l.policy == SyncDisabled {
		return nil
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.closed {
		return ErrClosed
	}
	if l.backgroundError != nil {
		return fmt.Errorf("aof: background sync: %w", l.backgroundError)
	}

	directory := filepath.Dir(l.path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(l.path)+".rewrite-*")
	if err != nil {
		return fmt.Errorf("aof: create rewrite file: %w", err)
	}
	temporaryPath := temporary.Name()
	renamed := false
	defer func() {
		_ = temporary.Close()
		if !renamed {
			_ = os.Remove(temporaryPath)
		}
	}()

	buffered := bufio.NewWriterSize(temporary, rewriteBufferSize)
	writer := resp.NewWriter(buffered)
	for _, command := range commands {
		if err := writer.WriteCommand(command...); err != nil {
			return fmt.Errorf("aof: encode rewrite command: %w", err)
		}
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("aof: flush rewrite file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("aof: sync rewrite file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("aof: close rewrite file: %w", err)
	}
	if err := os.Rename(temporaryPath, l.path); err != nil {
		return fmt.Errorf("aof: replace with rewrite file: %w", err)
	}
	renamed = true

	replacement, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return l.failAfterRewrite(fmt.Errorf("aof: reopen rewrite file: %w", err))
	}
	previous := l.file
	l.file = replacement
	if err := previous.Close(); err != nil {
		return l.failAfterRewrite(fmt.Errorf("aof: close replaced file: %w", err))
	}
	if err := syncDirectory(directory); err != nil {
		return l.failAfterRewrite(err)
	}
	return nil
}

func (l *Log) failAfterRewrite(err error) error {
	l.backgroundError = err
	return err
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("aof: open data directory for sync: %w", err)
	}
	syncError := directory.Sync()
	closeError := directory.Close()
	if syncError != nil {
		return fmt.Errorf("aof: sync data directory: %w", syncError)
	}
	if closeError != nil {
		return fmt.Errorf("aof: close data directory: %w", closeError)
	}
	return nil
}
