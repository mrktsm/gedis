package replication

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

const (
	defaultReplicaDialTimeout      = 5 * time.Second
	defaultReplicaReconnectDelay   = 250 * time.Millisecond
	defaultReplicaMaxSnapshotBytes = 256 * 1024 * 1024
)

var (
	ErrPrimaryAddressRequired = errors.New("replication primary address is required")
	ErrReplicaEngineRequired  = errors.New("replication apply engine is required")
	ErrInvalidSnapshotLimit   = errors.New("replication snapshot limit must be positive")
	ErrNoReplicationState     = errors.New("replica has not completed synchronization")
)

type ReplicaConfig struct {
	PrimaryAddress   string
	ListeningPort    int
	DialTimeout      time.Duration
	ReconnectDelay   time.Duration
	MaxSnapshotBytes int64
	InitialState     *PersistentState
}

type ReplicaStats struct {
	PrimaryAddress string
	Connected      bool
	Syncing        bool
	ReplicationID  string
	Offset         int64
	FullSyncs      int64
	PartialSyncs   int64
	Reconnects     int64
	LastError      string
}

type Replica struct {
	config ReplicaConfig
	engine *server.Engine

	mutex sync.Mutex
	stats ReplicaStats
	ready chan struct{}
	once  sync.Once
}

func NewReplica(config ReplicaConfig, engine *server.Engine) (*Replica, error) {
	if config.PrimaryAddress == "" {
		return nil, ErrPrimaryAddressRequired
	}
	if engine == nil {
		return nil, ErrReplicaEngineRequired
	}
	if config.DialTimeout == 0 {
		config.DialTimeout = defaultReplicaDialTimeout
	}
	if config.DialTimeout < 0 {
		return nil, errors.New("replication dial timeout cannot be negative")
	}
	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = defaultReplicaReconnectDelay
	}
	if config.ReconnectDelay < 0 {
		return nil, errors.New("replication reconnect delay cannot be negative")
	}
	if config.MaxSnapshotBytes == 0 {
		config.MaxSnapshotBytes = defaultReplicaMaxSnapshotBytes
	}
	if config.MaxSnapshotBytes < 0 {
		return nil, ErrInvalidSnapshotLimit
	}
	stats := ReplicaStats{PrimaryAddress: config.PrimaryAddress}
	if config.InitialState != nil {
		if err := config.InitialState.Validate(); err != nil {
			return nil, err
		}
		if config.InitialState.PrimaryAddress != config.PrimaryAddress {
			return nil, fmt.Errorf(
				"%w: checkpoint primary %q does not match %q",
				ErrInvalidReplicationState,
				config.InitialState.PrimaryAddress,
				config.PrimaryAddress,
			)
		}
		stats.ReplicationID = config.InitialState.ReplicationID
		stats.Offset = config.InitialState.Offset
	}
	return &Replica{
		config: config,
		stats:  stats,
		ready:  make(chan struct{}),
		engine: engine,
	}, nil
}

func (r *Replica) Checkpoint(aofSize int64) (PersistentState, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.stats.ReplicationID == "" {
		return PersistentState{}, ErrNoReplicationState
	}
	state := PersistentState{
		Version:        replicationStateVersion,
		PrimaryAddress: r.config.PrimaryAddress,
		ReplicationID:  r.stats.ReplicationID,
		Offset:         r.stats.Offset,
		AOFSize:        aofSize,
	}
	if err := state.Validate(); err != nil {
		return PersistentState{}, err
	}
	return state, nil
}

func (r *Replica) Ready() <-chan struct{} {
	return r.ready
}

func (r *Replica) Stats() ReplicaStats {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.stats
}

// Run reconnects until the context is canceled. Transient network and protocol
// errors are recorded in Stats and retried after ReconnectDelay.
func (r *Replica) Run(ctx context.Context) error {
	for {
		r.setSyncing()
		err := r.synchronize(ctx)
		if ctx.Err() != nil {
			r.setDisconnected("")
			return nil
		}
		if err == nil {
			err = errors.New("replication stream ended")
		}
		r.setDisconnected(err.Error())
		r.mutex.Lock()
		r.stats.Reconnects++
		r.mutex.Unlock()

		timer := time.NewTimer(r.config.ReconnectDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (r *Replica) synchronize(ctx context.Context) error {
	dialer := net.Dialer{Timeout: r.config.DialTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", r.config.PrimaryAddress)
	if err != nil {
		return fmt.Errorf("replication: connect to primary: %w", err)
	}
	defer connection.Close()
	stopCloser := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopCloser:
		}
	}()
	defer close(stopCloser)

	reader := bufio.NewReaderSize(connection, 64*1024)
	writer := bufio.NewWriter(connection)
	if err := exchangeStatus(reader, writer, "PONG", "PING"); err != nil {
		return err
	}
	if err := exchangeStatus(
		reader,
		writer,
		"OK",
		"REPLCONF", "listening-port", strconv.Itoa(r.config.ListeningPort),
	); err != nil {
		return err
	}
	if err := exchangeStatus(reader, writer, "OK", "REPLCONF", "capa", "psync2"); err != nil {
		return err
	}

	replicationID, offset := r.resumePoint()
	requestedID := replicationID
	requestedOffset := int64(-1)
	if requestedID == "" {
		requestedID = "?"
	} else {
		if offset == math.MaxInt64 {
			return errors.New("replication: offset exhausted")
		}
		requestedOffset = offset + 1
	}
	if err := writeCommand(writer, "PSYNC", requestedID, strconv.FormatInt(requestedOffset, 10)); err != nil {
		return err
	}
	header, err := readReplicationLine(reader)
	if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(header, "+FULLRESYNC "):
		if err := r.acceptFullSync(reader, header); err != nil {
			return err
		}
	case strings.HasPrefix(header, "+CONTINUE"):
		if err := r.acceptPartialSync(header); err != nil {
			return err
		}
	case strings.HasPrefix(header, "-"):
		return fmt.Errorf("replication: primary rejected PSYNC: %s", strings.TrimPrefix(header, "-"))
	default:
		return fmt.Errorf("replication: unexpected PSYNC reply %q", header)
	}

	r.setConnected()
	stream := resp.NewReader(reader)
	for {
		command, err := stream.ReadCommand()
		if err != nil {
			return fmt.Errorf("replication: read mutation stream: %w", err)
		}
		result := r.engine.ApplyReplication(command)
		if result.Response.Kind() == resp.KindError {
			return fmt.Errorf("replication: apply %q: %s", command[0], result.Response.Bytes())
		}
		encoded, err := encodeCommand(command)
		if err != nil {
			return fmt.Errorf("replication: measure mutation: %w", err)
		}
		if err := r.advanceOffset(int64(len(encoded))); err != nil {
			return err
		}
	}
}

func (r *Replica) acceptFullSync(reader *bufio.Reader, header string) error {
	fields := strings.Fields(strings.TrimPrefix(header, "+"))
	if len(fields) != 3 || fields[0] != "FULLRESYNC" || !validReplicationID(fields[1]) {
		return fmt.Errorf("replication: malformed FULLRESYNC header %q", header)
	}
	offset, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || offset < 0 {
		return fmt.Errorf("replication: malformed FULLRESYNC offset %q", fields[2])
	}
	snapshotData, err := readSnapshotTransfer(reader, r.config.MaxSnapshotBytes)
	if err != nil {
		return err
	}
	entries, err := decodeSnapshot(snapshotData)
	if err != nil {
		return err
	}
	if err := r.engine.InstallSnapshot(entries); err != nil {
		return fmt.Errorf("replication: install full snapshot: %w", err)
	}
	r.mutex.Lock()
	r.stats.ReplicationID = fields[1]
	r.stats.Offset = offset
	r.stats.FullSyncs++
	r.mutex.Unlock()
	return nil
}

func (r *Replica) acceptPartialSync(header string) error {
	fields := strings.Fields(strings.TrimPrefix(header, "+"))
	if len(fields) < 1 || len(fields) > 2 || fields[0] != "CONTINUE" {
		return fmt.Errorf("replication: malformed CONTINUE header %q", header)
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.stats.ReplicationID == "" {
		return errors.New("replication: CONTINUE received without prior full sync")
	}
	if len(fields) == 2 {
		if !validReplicationID(fields[1]) {
			return fmt.Errorf("replication: malformed CONTINUE ID %q", fields[1])
		}
		r.stats.ReplicationID = fields[1]
	}
	r.stats.PartialSyncs++
	return nil
}

func (r *Replica) resumePoint() (string, int64) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.stats.ReplicationID, r.stats.Offset
}

func (r *Replica) setConnected() {
	r.mutex.Lock()
	r.stats.Connected = true
	r.stats.Syncing = false
	r.stats.LastError = ""
	r.mutex.Unlock()
	r.once.Do(func() { close(r.ready) })
}

func (r *Replica) setDisconnected(lastError string) {
	r.mutex.Lock()
	r.stats.Connected = false
	r.stats.Syncing = false
	r.stats.LastError = lastError
	r.mutex.Unlock()
}

func (r *Replica) setSyncing() {
	r.mutex.Lock()
	r.stats.Connected = false
	r.stats.Syncing = true
	r.mutex.Unlock()
}

func (r *Replica) advanceOffset(delta int64) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if delta < 0 || r.stats.Offset > math.MaxInt64-delta {
		return errors.New("replication: offset exhausted")
	}
	r.stats.Offset += delta
	return nil
}

func exchangeStatus(reader *bufio.Reader, writer *bufio.Writer, want string, command ...string) error {
	if err := writeCommand(writer, command...); err != nil {
		return err
	}
	line, err := readReplicationLine(reader)
	if err != nil {
		return err
	}
	if line != "+"+want {
		return fmt.Errorf("replication: %s reply = %q, want +%s", command[0], line, want)
	}
	return nil
}

func writeCommand(writer *bufio.Writer, command ...string) error {
	arguments := make([][]byte, len(command))
	for index, argument := range command {
		arguments[index] = []byte(argument)
	}
	if err := resp.NewWriter(writer).WriteCommand(arguments...); err != nil {
		return fmt.Errorf("replication: write %s: %w", command[0], err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("replication: flush %s: %w", command[0], err)
	}
	return nil
}

func readReplicationLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("replication: read response: %w", err)
	}
	if !strings.HasSuffix(line, "\r\n") {
		return "", errors.New("replication: response missing CRLF")
	}
	return strings.TrimSuffix(line, "\r\n"), nil
}

func readSnapshotTransfer(reader *bufio.Reader, maximum int64) ([]byte, error) {
	line, err := readReplicationLine(reader)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "$") {
		return nil, fmt.Errorf("replication: snapshot length = %q", line)
	}
	length, err := strconv.ParseInt(strings.TrimPrefix(line, "$"), 10, 64)
	if err != nil || length < 0 {
		return nil, fmt.Errorf("replication: invalid snapshot length %q", line)
	}
	if length > maximum {
		return nil, fmt.Errorf("replication: snapshot length %d exceeds limit %d", length, maximum)
	}
	if uint64(length) > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("replication: snapshot length %d exceeds platform capacity", length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, fmt.Errorf("replication: read snapshot payload: %w", err)
	}
	return data, nil
}

func decodeSnapshot(data []byte) ([]store.SnapshotEntry, error) {
	keyspace := store.New()
	engine := server.NewEngineWithStore(keyspace)
	reader := resp.NewReader(bytes.NewReader(data))
	for {
		command, err := reader.ReadCommand()
		if errors.Is(err, io.EOF) {
			return keyspace.Snapshot(), nil
		}
		if err != nil {
			return nil, fmt.Errorf("replication: decode snapshot: %w", err)
		}
		result := engine.Execute(command)
		if result.Response.Kind() == resp.KindError {
			return nil, fmt.Errorf("replication: apply snapshot command %q: %s", command[0], result.Response.Bytes())
		}
	}
}
