package aof

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

var ErrClosed = errors.New("append-only log is closed")

type SyncPolicy string

const (
	SyncDisabled    SyncPolicy = "disabled"
	SyncAlways      SyncPolicy = "always"
	SyncEverySecond SyncPolicy = "everysec"
	SyncNever       SyncPolicy = "no"
)

func (p SyncPolicy) Valid() bool {
	switch p {
	case SyncDisabled, SyncAlways, SyncEverySecond, SyncNever:
		return true
	default:
		return false
	}
}

type Config struct {
	Path         string
	SyncPolicy   SyncPolicy
	SyncInterval time.Duration
}

// Log appends canonical RESP2 commands without interleaving writes from
// concurrent clients.
type Log struct {
	mutex  sync.Mutex
	file   *os.File
	path   string
	policy SyncPolicy

	backgroundError error
	closed          bool
	stopSync        chan struct{}
	syncStopped     chan struct{}
	stopOnce        sync.Once
}

func Open(config Config) (*Log, error) {
	if !config.SyncPolicy.Valid() {
		return nil, fmt.Errorf("aof: invalid sync policy %q", config.SyncPolicy)
	}
	log := &Log{policy: config.SyncPolicy}
	if config.SyncPolicy == SyncDisabled {
		return log, nil
	}
	if config.Path == "" {
		return nil, errors.New("aof: path is required when persistence is enabled")
	}
	if config.SyncInterval <= 0 {
		config.SyncInterval = time.Second
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
		return nil, fmt.Errorf("aof: create data directory: %w", err)
	}
	file, err := os.OpenFile(config.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("aof: open: %w", err)
	}
	log.file = file
	log.path = config.Path

	if config.SyncPolicy == SyncEverySecond {
		log.stopSync = make(chan struct{})
		log.syncStopped = make(chan struct{})
		go log.runSync(config.SyncInterval)
	}
	return log, nil
}

func (l *Log) Append(command [][]byte) error {
	if l.policy == SyncDisabled {
		return nil
	}
	encoded, err := encodeCommand(command)
	if err != nil {
		return fmt.Errorf("aof: encode command: %w", err)
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.closed {
		return ErrClosed
	}
	if l.backgroundError != nil {
		return fmt.Errorf("aof: background sync: %w", l.backgroundError)
	}
	if err := writeAll(l.file, encoded); err != nil {
		return fmt.Errorf("aof: append: %w", err)
	}
	if l.policy == SyncAlways {
		if err := l.file.Sync(); err != nil {
			return fmt.Errorf("aof: sync: %w", err)
		}
	}
	return nil
}

func (l *Log) Sync() error {
	if l.policy == SyncDisabled {
		return nil
	}
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.closed {
		return ErrClosed
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("aof: sync: %w", err)
	}
	return nil
}

func (l *Log) Close() error {
	if l.policy == SyncDisabled {
		l.mutex.Lock()
		l.closed = true
		l.mutex.Unlock()
		return nil
	}
	if l.stopSync != nil {
		l.stopOnce.Do(func() { close(l.stopSync) })
		<-l.syncStopped
	}

	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	syncError := l.file.Sync()
	closeError := l.file.Close()
	if l.backgroundError != nil {
		return fmt.Errorf("aof: background sync: %w", l.backgroundError)
	}
	if syncError != nil {
		return fmt.Errorf("aof: final sync: %w", syncError)
	}
	if closeError != nil {
		return fmt.Errorf("aof: close: %w", closeError)
	}
	return nil
}

func (l *Log) runSync(interval time.Duration) {
	defer close(l.syncStopped)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mutex.Lock()
			if l.backgroundError == nil {
				l.backgroundError = l.file.Sync()
			}
			l.mutex.Unlock()
		case <-l.stopSync:
			return
		}
	}
}

func encodeCommand(command [][]byte) ([]byte, error) {
	var encoded bytes.Buffer
	if err := resp.NewWriter(&encoded).WriteCommand(command...); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
