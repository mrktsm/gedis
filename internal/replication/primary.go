package replication

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/snapshot"
	"github.com/mrktsm/gedis/internal/store"
)

const (
	defaultBacklogBytes    = 1024 * 1024
	defaultSubscriberQueue = 256
)

var (
	ErrInvalidReplicationID = errors.New("replication ID must contain 40 hexadecimal characters")
	ErrInvalidQueueSize     = errors.New("replication subscriber queue must be positive")
	ErrPersistenceDisabled  = errors.New("append-only persistence is not enabled")
	ErrRewriteUnsupported   = errors.New("downstream persistence does not support rewrite")
	ErrSnapshotUnavailable  = errors.New("replication snapshot source is not configured")
)

type MutationSink interface {
	Append(command [][]byte) error
}

type Snapshotter interface {
	CaptureSnapshot(callback func([]store.SnapshotEntry) error) error
}

type PrimaryConfig struct {
	BacklogBytes    int
	SubscriberQueue int
	ReplicationID   string
	Downstream      MutationSink
}

type Chunk struct {
	Data        []byte
	StartOffset int64
	EndOffset   int64
}

type PrimaryStats struct {
	ReplicationID     string
	Offset            int64
	BacklogFirstByte  int64
	BacklogBytes      int
	ConnectedReplicas int
}

type FullSyncSnapshot struct {
	ReplicationID string
	Offset        int64
	Keys          int
	Data          []byte
}

type Primary struct {
	mutex sync.Mutex

	id              string
	backlog         *Backlog
	subscriberQueue int
	downstream      MutationSink
	snapshotter     Snapshotter
	nextSubscriber  uint64
	subscribers     map[uint64]chan Chunk
}

func (p *Primary) SetSnapshotter(snapshotter Snapshotter) {
	p.mutex.Lock()
	p.snapshotter = snapshotter
	p.mutex.Unlock()
}

func NewPrimary(config PrimaryConfig) (*Primary, error) {
	if config.BacklogBytes == 0 {
		config.BacklogBytes = defaultBacklogBytes
	}
	backlog, err := NewBacklog(config.BacklogBytes)
	if err != nil {
		return nil, err
	}
	if config.SubscriberQueue == 0 {
		config.SubscriberQueue = defaultSubscriberQueue
	}
	if config.SubscriberQueue < 0 {
		return nil, ErrInvalidQueueSize
	}
	if config.ReplicationID == "" {
		config.ReplicationID, err = NewID()
		if err != nil {
			return nil, fmt.Errorf("replication: generate ID: %w", err)
		}
	}
	if !validReplicationID(config.ReplicationID) {
		return nil, ErrInvalidReplicationID
	}
	return &Primary{
		id:              config.ReplicationID,
		backlog:         backlog,
		subscriberQueue: config.SubscriberQueue,
		downstream:      config.Downstream,
		subscribers:     make(map[uint64]chan Chunk),
	}, nil
}

// Append persists first, then advances the replication stream. A persistence
// failure therefore cannot become visible to replicas as an acknowledged
// primary mutation.
func (p *Primary) Append(command [][]byte) error {
	encoded, err := encodeCommand(command)
	if err != nil {
		return fmt.Errorf("replication: encode mutation: %w", err)
	}
	if p.downstream != nil {
		if err := p.downstream.Append(command); err != nil {
			return err
		}
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()
	start, end := p.backlog.Append(encoded)
	for id, subscriber := range p.subscribers {
		chunk := Chunk{
			Data:        append([]byte(nil), encoded...),
			StartOffset: start,
			EndOffset:   end,
		}
		select {
		case subscriber <- chunk:
		default:
			close(subscriber)
			delete(p.subscribers, id)
		}
	}
	return nil
}

// PartialSync atomically returns retained bytes after offset and registers for
// subsequent chunks. The caller owns the returned subscription and must close
// it when the connection ends.
func (p *Primary) PartialSync(replicationID string, offset int64) ([]byte, *Subscription, int64, bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if replicationID != p.id {
		return nil, nil, p.backlog.Offset(), false
	}
	initial, ok := p.backlog.After(offset)
	if !ok {
		return nil, nil, p.backlog.Offset(), false
	}
	subscription := p.subscribeLocked()
	return initial, subscription, p.backlog.Offset(), true
}

// FullSync captures state and, under the engine mutation barrier, atomically
// pairs it with the current offset and a subscription for later writes.
func (p *Primary) FullSync() (FullSyncSnapshot, *Subscription, error) {
	p.mutex.Lock()
	snapshotter := p.snapshotter
	p.mutex.Unlock()
	if snapshotter == nil {
		return FullSyncSnapshot{}, nil, ErrSnapshotUnavailable
	}

	var result FullSyncSnapshot
	var subscription *Subscription
	err := snapshotter.CaptureSnapshot(func(entries []store.SnapshotEntry) error {
		encoded, err := encodeCommands(snapshot.Commands(entries))
		if err != nil {
			return fmt.Errorf("replication: encode full snapshot: %w", err)
		}
		p.mutex.Lock()
		result = FullSyncSnapshot{
			ReplicationID: p.id,
			Offset:        p.backlog.Offset(),
			Keys:          len(entries),
			Data:          encoded,
		}
		subscription = p.subscribeLocked()
		p.mutex.Unlock()
		return nil
	})
	if err != nil {
		if subscription != nil {
			subscription.Close()
		}
		return FullSyncSnapshot{}, nil, err
	}
	return result, subscription, nil
}

func (p *Primary) Subscribe() (*Subscription, int64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.subscribeLocked(), p.backlog.Offset()
}

func (p *Primary) subscribeLocked() *Subscription {
	p.nextSubscriber++
	channel := make(chan Chunk, p.subscriberQueue)
	p.subscribers[p.nextSubscriber] = channel
	return &Subscription{
		id:      p.nextSubscriber,
		primary: p,
		chunks:  channel,
	}
}

func (p *Primary) removeSubscriber(id uint64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if channel, ok := p.subscribers[id]; ok {
		close(channel)
		delete(p.subscribers, id)
	}
}

func (p *Primary) Stats() PrimaryStats {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return PrimaryStats{
		ReplicationID:     p.id,
		Offset:            p.backlog.Offset(),
		BacklogFirstByte:  p.backlog.FirstOffset(),
		BacklogBytes:      p.backlog.HistoryLength(),
		ConnectedReplicas: len(p.subscribers),
	}
}

func (p *Primary) Rewrite(commands [][][]byte) error {
	if p.downstream == nil {
		return ErrPersistenceDisabled
	}
	rewriter, ok := p.downstream.(interface {
		Rewrite(commands [][][]byte) error
	})
	if !ok {
		return ErrRewriteUnsupported
	}
	return rewriter.Rewrite(commands)
}

func (p *Primary) AOFInfo() (bool, string, int64, error) {
	if p.downstream == nil {
		return false, "disabled", 0, nil
	}
	provider, ok := p.downstream.(interface {
		AOFInfo() (bool, string, int64, error)
	})
	if !ok {
		return true, "unknown", 0, nil
	}
	return provider.AOFInfo()
}

func validReplicationID(id string) bool {
	if len(id) != replicationIDBytes*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func encodeCommand(command [][]byte) ([]byte, error) {
	var encoded bytes.Buffer
	if err := resp.NewWriter(&encoded).WriteCommand(command...); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func encodeCommands(commands [][][]byte) ([]byte, error) {
	var encoded bytes.Buffer
	writer := resp.NewWriter(&encoded)
	for _, command := range commands {
		if err := writer.WriteCommand(command...); err != nil {
			return nil, err
		}
	}
	return encoded.Bytes(), nil
}

type Subscription struct {
	once    sync.Once
	id      uint64
	primary *Primary
	chunks  <-chan Chunk
}

func (s *Subscription) Chunks() <-chan Chunk {
	return s.chunks
}

func (s *Subscription) Close() {
	if s == nil || s.primary == nil {
		return
	}
	s.once.Do(func() {
		s.primary.removeSubscriber(s.id)
	})
}
