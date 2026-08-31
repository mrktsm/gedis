package resp

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReaderReadValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{name: "simple string", input: "+OK\r\n", want: SimpleString("OK")},
		{name: "error", input: "-ERR failure\r\n", want: Error("ERR failure")},
		{name: "integer", input: ":-42\r\n", want: Integer(-42)},
		{name: "bulk string", input: "$5\r\nhello\r\n", want: BulkStringString("hello")},
		{name: "binary bulk string", input: "$3\r\na\x00b\r\n", want: BulkString([]byte{'a', 0, 'b'})},
		{name: "empty bulk string", input: "$0\r\n\r\n", want: BulkString([]byte{})},
		{name: "null bulk string", input: "$-1\r\n", want: NullBulkString()},
		{name: "empty array", input: "*0\r\n", want: Array()},
		{name: "null array", input: "*-1\r\n", want: NullArray()},
		{
			name:  "nested array",
			input: "*2\r\n$3\r\nGET\r\n*2\r\n:1\r\n+OK\r\n",
			want:  Array(BulkStringString("GET"), Array(Integer(1), SimpleString("OK"))),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewReader(strings.NewReader(test.input)).ReadValue()
			if err != nil {
				t.Fatalf("ReadValue() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ReadValue() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestReaderReadsFragmentedInput(t *testing.T) {
	t.Parallel()

	input := &oneByteReader{reader: strings.NewReader("$5\r\nhello\r\n")}
	got, err := NewReader(input).ReadValue()
	if err != nil {
		t.Fatalf("ReadValue() error = %v", err)
	}
	if string(got.Bytes()) != "hello" {
		t.Fatalf("ReadValue() = %q, want hello", got.Bytes())
	}
}

func TestReaderReadsPipelinedCommands(t *testing.T) {
	t.Parallel()

	reader := NewReader(strings.NewReader(
		"*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n" +
			"*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n",
	))

	first, err := reader.ReadCommand()
	if err != nil {
		t.Fatalf("first ReadCommand() error = %v", err)
	}
	second, err := reader.ReadCommand()
	if err != nil {
		t.Fatalf("second ReadCommand() error = %v", err)
	}

	if got, want := bytesToStrings(first), []string{"GET", "key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first ReadCommand() = %q, want %q", got, want)
	}
	if got, want := bytesToStrings(second), []string{"SET", "key", "value"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second ReadCommand() = %q, want %q", got, want)
	}
}

func TestReaderRejectsMalformedFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		limits Limits
	}{
		{name: "unknown type", input: "?wat\r\n"},
		{name: "LF without CR", input: "+OK\n"},
		{name: "invalid integer", input: ":twelve\r\n"},
		{name: "empty integer", input: ":\r\n"},
		{name: "negative bulk length", input: "$-2\r\n"},
		{name: "signed bulk length", input: "$+2\r\nhi\r\n"},
		{name: "bulk too large", input: "$5\r\nhello\r\n", limits: Limits{MaxBulkLength: 4}},
		{name: "bad bulk terminator", input: "$2\r\nhiXX"},
		{name: "negative array length", input: "*-2\r\n"},
		{name: "array too large", input: "*2\r\n+1\r\n+2\r\n", limits: Limits{MaxArrayLength: 1}},
		{name: "line too large", input: "+long\r\n", limits: Limits{MaxLineLength: 3}},
		{name: "nesting too deep", input: "*1\r\n*1\r\n+OK\r\n", limits: Limits{MaxDepth: 1}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewReaderWithLimits(strings.NewReader(test.input), test.limits).ReadValue()
			var protocolErr *ProtocolError
			if !errors.As(err, &protocolErr) {
				t.Fatalf("ReadValue() error = %v, want ProtocolError", err)
			}
		})
	}
}

func TestReaderReportsTruncatedFrames(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"+OK", "$5\r\nhel", "*1\r\n"} {
		_, err := NewReader(strings.NewReader(input)).ReadValue()
		if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadValue(%q) error = %v, want EOF", input, err)
		}
	}
}

func TestReaderRejectsInvalidCommands(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"+PING\r\n",
		"*-1\r\n",
		"*0\r\n",
		"*1\r\n+PING\r\n",
		"*1\r\n$-1\r\n",
	} {
		_, err := NewReader(strings.NewReader(input)).ReadCommand()
		var protocolErr *ProtocolError
		if !errors.As(err, &protocolErr) {
			t.Fatalf("ReadCommand(%q) error = %v, want ProtocolError", input, err)
		}
	}
}

func FuzzReader(f *testing.F) {
	for _, seed := range []string{
		"+OK\r\n",
		"$5\r\nhello\r\n",
		"*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n",
		"*-2\r\n",
		"\x00\xff\r\n",
	} {
		f.Add([]byte(seed))
	}

	limits := Limits{
		MaxBulkLength:  1024,
		MaxArrayLength: 64,
		MaxLineLength:  128,
		MaxDepth:       8,
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		reader := NewReaderWithLimits(strings.NewReader(string(input)), limits)
		for range 32 {
			if _, err := reader.ReadValue(); err != nil {
				return
			}
		}
	})
}

type oneByteReader struct {
	reader io.Reader
}

func (r *oneByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return r.reader.Read(buffer)
}

func bytesToStrings(values [][]byte) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
