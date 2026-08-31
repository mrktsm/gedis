package resp

import (
	"bytes"
	"errors"
	"testing"
)

func TestWriterWriteValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{name: "simple string", value: SimpleString("OK"), want: "+OK\r\n"},
		{name: "error", value: Error("ERR unknown command"), want: "-ERR unknown command\r\n"},
		{name: "positive integer", value: Integer(42), want: ":42\r\n"},
		{name: "negative integer", value: Integer(-7), want: ":-7\r\n"},
		{name: "bulk string", value: BulkStringString("hello"), want: "$5\r\nhello\r\n"},
		{name: "binary bulk string", value: BulkString([]byte{'a', 0, 'b'}), want: "$3\r\na\x00b\r\n"},
		{name: "empty bulk string", value: BulkString(nil), want: "$0\r\n\r\n"},
		{name: "null bulk string", value: NullBulkString(), want: "$-1\r\n"},
		{name: "empty array", value: Array(), want: "*0\r\n"},
		{name: "null array", value: NullArray(), want: "*-1\r\n"},
		{
			name: "nested array",
			value: Array(
				BulkStringString("GET"),
				Array(Integer(1), SimpleString("OK")),
			),
			want: "*2\r\n$3\r\nGET\r\n*2\r\n:1\r\n+OK\r\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			if err := NewWriter(&output).WriteValue(test.value); err != nil {
				t.Fatalf("WriteValue() error = %v", err)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("WriteValue() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWriterWriteCommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := NewWriter(&output).WriteCommand([]byte("SET"), []byte("key"), []byte("value"))
	if err != nil {
		t.Fatalf("WriteCommand() error = %v", err)
	}

	const want = "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	if got := output.String(); got != want {
		t.Fatalf("WriteCommand() = %q, want %q", got, want)
	}
}

func TestWriterRejectsCRLFInLineValues(t *testing.T) {
	t.Parallel()

	for _, value := range []Value{SimpleString("bad\rvalue"), Error("bad\nvalue")} {
		var output bytes.Buffer
		err := NewWriter(&output).WriteValue(value)
		if !errors.Is(err, ErrInvalidLine) {
			t.Fatalf("WriteValue() error = %v, want %v", err, ErrInvalidLine)
		}
	}
}

type shortWriter struct {
	buffer bytes.Buffer
}

func (w *shortWriter) Write(data []byte) (int, error) {
	if len(data) > 1 {
		data = data[:1]
	}
	return w.buffer.Write(data)
}

func TestWriterHandlesShortWrites(t *testing.T) {
	t.Parallel()

	writer := &shortWriter{}
	if err := NewWriter(writer).WriteValue(BulkStringString("hello")); err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	if got, want := writer.buffer.String(), "$5\r\nhello\r\n"; got != want {
		t.Fatalf("WriteValue() = %q, want %q", got, want)
	}
}
