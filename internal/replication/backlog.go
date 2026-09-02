package replication

import (
	"errors"
	"sync"
)

var ErrInvalidBacklogSize = errors.New("replication backlog size must be positive")

// Backlog retains the newest bytes from a replication stream. Offset is the
// total number of bytes appended, while FirstOffset is the Redis-style offset
// of the first retained byte (Offset - HistoryLength + 1).
type Backlog struct {
	mutex    sync.Mutex
	capacity int
	data     []byte
	offset   int64
}

func NewBacklog(capacity int) (*Backlog, error) {
	if capacity <= 0 {
		return nil, ErrInvalidBacklogSize
	}
	return &Backlog{
		capacity: capacity,
		data:     make([]byte, 0, capacity),
	}, nil
}

// Append adds bytes and returns their inclusive replication-offset range. An
// empty append returns Offset()+1, Offset() and changes no state.
func (b *Backlog) Append(data []byte) (startOffset, endOffset int64) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	startOffset = b.offset + 1
	b.offset += int64(len(data))
	if len(data) >= b.capacity {
		b.data = append(b.data[:0], data[len(data)-b.capacity:]...)
	} else {
		excess := len(b.data) + len(data) - b.capacity
		if excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, data...)
	}
	return startOffset, b.offset
}

func (b *Backlog) Offset() int64 {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.offset
}

func (b *Backlog) FirstOffset() int64 {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.offset - int64(len(b.data)) + 1
}

func (b *Backlog) HistoryLength() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return len(b.data)
}

func (b *Backlog) Capacity() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.capacity
}

// After returns bytes whose offsets are strictly greater than offset. The
// request succeeds when offset is current or is immediately before any byte
// still retained in the backlog.
func (b *Backlog) After(offset int64) ([]byte, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	oldestPrecedingOffset := b.offset - int64(len(b.data))
	if offset < oldestPrecedingOffset || offset > b.offset {
		return nil, false
	}
	start := int(offset - oldestPrecedingOffset)
	return append([]byte(nil), b.data[start:]...), true
}
