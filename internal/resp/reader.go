package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	defaultBufferSize     = 4 * 1024
	defaultMaxBulkLength  = 64 * 1024 * 1024
	defaultMaxArrayLength = 1024
	defaultMaxLineLength  = 64 * 1024
	defaultMaxDepth       = 16
)

// Limits bounds decoder memory use and recursive nesting. Zero values select
// the defaults.
type Limits struct {
	MaxBulkLength  int
	MaxArrayLength int
	MaxLineLength  int
	MaxDepth       int
}

var DefaultLimits = Limits{
	MaxBulkLength:  defaultMaxBulkLength,
	MaxArrayLength: defaultMaxArrayLength,
	MaxLineLength:  defaultMaxLineLength,
	MaxDepth:       defaultMaxDepth,
}

// ProtocolError indicates a syntactically invalid RESP frame. I/O errors are
// returned unchanged so a server can distinguish bad input from disconnects.
type ProtocolError struct {
	message string
}

func (e *ProtocolError) Error() string {
	return "RESP protocol error: " + e.message
}

// Reader incrementally decodes RESP2 values from a stream.
type Reader struct {
	reader *bufio.Reader
	limits Limits
}

func NewReader(reader io.Reader) *Reader {
	return NewReaderWithLimits(reader, DefaultLimits)
}

func NewReaderWithLimits(reader io.Reader, limits Limits) *Reader {
	limits = normalizeLimits(limits)
	return &Reader{
		reader: bufio.NewReaderSize(reader, defaultBufferSize),
		limits: limits,
	}
}

func (r *Reader) ReadValue() (Value, error) {
	return r.readValue(0)
}

// ReadCommand decodes the Redis request form: a non-empty array containing
// non-null bulk strings.
func (r *Reader) ReadCommand() ([][]byte, error) {
	value, err := r.ReadValue()
	if err != nil {
		return nil, err
	}
	if value.kind != KindArray || value.null {
		return nil, newProtocolError("expected a non-null array command")
	}
	if len(value.values) == 0 {
		return nil, newProtocolError("empty command")
	}

	command := make([][]byte, len(value.values))
	for index, argument := range value.values {
		if argument.kind != KindBulkString || argument.null {
			return nil, newProtocolError("command argument %d is not a bulk string", index)
		}
		command[index] = argument.data
	}
	return command, nil
}

func (r *Reader) readValue(depth int) (Value, error) {
	if depth > r.limits.MaxDepth {
		return Value{}, newProtocolError("maximum nesting depth exceeded")
	}

	prefix, err := r.reader.ReadByte()
	if err != nil {
		return Value{}, err
	}

	switch Kind(prefix) {
	case KindSimpleString:
		line, err := r.readLine()
		if err != nil {
			return Value{}, err
		}
		return Value{kind: KindSimpleString, data: line}, nil
	case KindError:
		line, err := r.readLine()
		if err != nil {
			return Value{}, err
		}
		return Value{kind: KindError, data: line}, nil
	case KindInteger:
		return r.readInteger()
	case KindBulkString:
		return r.readBulkString()
	case KindArray:
		return r.readArray(depth)
	default:
		return Value{}, newProtocolError("unknown type byte %q", prefix)
	}
}

func (r *Reader) readInteger() (Value, error) {
	line, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	if len(line) == 0 {
		return Value{}, newProtocolError("empty integer")
	}

	integer, err := strconv.ParseInt(string(line), 10, 64)
	if err != nil {
		return Value{}, newProtocolError("invalid integer %q", line)
	}
	return Value{kind: KindInteger, integer: integer}, nil
}

func (r *Reader) readBulkString() (Value, error) {
	length, null, err := r.readLength("bulk string", r.limits.MaxBulkLength)
	if err != nil {
		return Value{}, err
	}
	if null {
		return NullBulkString(), nil
	}

	data := make([]byte, length+2)
	if _, err := io.ReadFull(r.reader, data); err != nil {
		return Value{}, err
	}
	if data[length] != '\r' || data[length+1] != '\n' {
		return Value{}, newProtocolError("bulk string is not terminated by CRLF")
	}
	return Value{kind: KindBulkString, data: data[:length]}, nil
}

func (r *Reader) readArray(depth int) (Value, error) {
	length, null, err := r.readLength("array", r.limits.MaxArrayLength)
	if err != nil {
		return Value{}, err
	}
	if null {
		return NullArray(), nil
	}
	if length == 0 {
		return Array(), nil
	}

	values := make([]Value, length)
	for index := range values {
		value, err := r.readValue(depth + 1)
		if err != nil {
			return Value{}, err
		}
		values[index] = value
	}
	return Value{kind: KindArray, values: values}, nil
}

func (r *Reader) readLength(kind string, maximum int) (length int, null bool, err error) {
	line, err := r.readLine()
	if err != nil {
		return 0, false, err
	}
	if string(line) == "-1" {
		return 0, true, nil
	}
	if len(line) == 0 {
		return 0, false, newProtocolError("empty %s length", kind)
	}
	for _, digit := range line {
		if digit < '0' || digit > '9' {
			return 0, false, newProtocolError("invalid %s length %q", kind, line)
		}
	}

	parsed, parseErr := strconv.ParseUint(string(line), 10, 63)
	if parseErr != nil || parsed > uint64(maximum) {
		return 0, false, newProtocolError("%s length exceeds maximum of %d", kind, maximum)
	}
	return int(parsed), false, nil
}

func (r *Reader) readLine() ([]byte, error) {
	line := make([]byte, 0, 32)
	for {
		fragment, err := r.reader.ReadSlice('\n')
		if len(line)+len(fragment) > r.limits.MaxLineLength+2 {
			return nil, newProtocolError("line exceeds maximum of %d bytes", r.limits.MaxLineLength)
		}
		line = append(line, fragment...)

		switch {
		case err == nil:
			if len(line) < 2 || line[len(line)-2] != '\r' {
				return nil, newProtocolError("line is not terminated by CRLF")
			}
			return line[:len(line)-2], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

func normalizeLimits(limits Limits) Limits {
	if limits.MaxBulkLength <= 0 {
		limits.MaxBulkLength = DefaultLimits.MaxBulkLength
	}
	if limits.MaxArrayLength <= 0 {
		limits.MaxArrayLength = DefaultLimits.MaxArrayLength
	}
	if limits.MaxLineLength <= 0 {
		limits.MaxLineLength = DefaultLimits.MaxLineLength
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = DefaultLimits.MaxDepth
	}
	return limits
}

func newProtocolError(format string, arguments ...any) error {
	return &ProtocolError{message: fmt.Sprintf(format, arguments...)}
}
