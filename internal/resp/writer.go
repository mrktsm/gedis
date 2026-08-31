package resp

import (
	"errors"
	"io"
	"strconv"
)

var ErrInvalidLine = errors.New("RESP simple strings and errors cannot contain CR or LF")

// Writer serializes RESP2 values to a stream. It does not buffer or flush the
// underlying writer; callers that need batching should pass a buffered writer.
type Writer struct {
	writer io.Writer
}

func NewWriter(writer io.Writer) *Writer {
	return &Writer{writer: writer}
}

func (w *Writer) WriteValue(value Value) error {
	switch value.kind {
	case KindSimpleString, KindError:
		return w.writeLineValue(value.kind, value.data)
	case KindInteger:
		return w.writeInteger(value.integer)
	case KindBulkString:
		return w.writeBulkString(value)
	case KindArray:
		return w.writeArray(value)
	default:
		return errors.New("unknown RESP value kind")
	}
}

// WriteCommand writes args as the canonical RESP2 array-of-bulk-strings form.
// This is used by clients, append-only persistence, and replication.
func (w *Writer) WriteCommand(args ...[]byte) error {
	if err := w.writeLength(KindArray, len(args)); err != nil {
		return err
	}

	for _, arg := range args {
		if err := w.WriteValue(BulkString(arg)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) writeLineValue(kind Kind, value []byte) error {
	for _, character := range value {
		if character == '\r' || character == '\n' {
			return ErrInvalidLine
		}
	}

	if err := writeAll(w.writer, []byte{byte(kind)}); err != nil {
		return err
	}
	if err := writeAll(w.writer, value); err != nil {
		return err
	}
	return writeAll(w.writer, []byte("\r\n"))
}

func (w *Writer) writeInteger(value int64) error {
	buffer := strconv.AppendInt([]byte{byte(KindInteger)}, value, 10)
	buffer = append(buffer, '\r', '\n')
	return writeAll(w.writer, buffer)
}

func (w *Writer) writeBulkString(value Value) error {
	if value.null {
		return writeAll(w.writer, []byte("$-1\r\n"))
	}
	if err := w.writeLength(KindBulkString, len(value.data)); err != nil {
		return err
	}
	if err := writeAll(w.writer, value.data); err != nil {
		return err
	}
	return writeAll(w.writer, []byte("\r\n"))
}

func (w *Writer) writeArray(value Value) error {
	if value.null {
		return writeAll(w.writer, []byte("*-1\r\n"))
	}
	if err := w.writeLength(KindArray, len(value.values)); err != nil {
		return err
	}
	for _, item := range value.values {
		if err := w.WriteValue(item); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) writeLength(kind Kind, length int) error {
	buffer := strconv.AppendInt([]byte{byte(kind)}, int64(length), 10)
	buffer = append(buffer, '\r', '\n')
	return writeAll(w.writer, buffer)
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
