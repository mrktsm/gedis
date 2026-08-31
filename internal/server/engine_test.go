package server

import (
	"reflect"
	"testing"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestEngineExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command []string
		want    Result
	}{
		{
			name:    "PING",
			command: []string{"PING"},
			want:    Result{Response: resp.SimpleString("PONG")},
		},
		{
			name:    "case insensitive PING",
			command: []string{"ping"},
			want:    Result{Response: resp.SimpleString("PONG")},
		},
		{
			name:    "PING message",
			command: []string{"PING", "hello"},
			want:    Result{Response: resp.BulkStringString("hello")},
		},
		{
			name:    "binary ECHO",
			command: []string{"ECHO", "hello\x00world"},
			want:    Result{Response: resp.BulkStringString("hello\x00world")},
		},
		{
			name:    "QUIT",
			command: []string{"QUIT"},
			want:    Result{Response: resp.SimpleString("OK"), Close: true},
		},
		{
			name:    "unknown command preserves spelling",
			command: []string{"NoSuchCommand"},
			want:    Result{Response: resp.Error("ERR unknown command 'NoSuchCommand'")},
		},
		{
			name:    "unknown command escapes line endings",
			command: []string{"bad\r\ncommand"},
			want:    Result{Response: resp.Error("ERR unknown command 'bad\\r\\ncommand'")},
		},
		{
			name:    "empty command",
			command: nil,
			want:    Result{Response: resp.Error("ERR empty command")},
		},
		{
			name:    "empty command name",
			command: []string{""},
			want:    Result{Response: resp.Error("ERR empty command")},
		},
		{
			name:    "PING wrong arity",
			command: []string{"PING", "one", "two"},
			want: Result{Response: resp.Error(
				"ERR wrong number of arguments for 'ping' command",
			)},
		},
		{
			name:    "ECHO wrong arity",
			command: []string{"ECHO"},
			want: Result{Response: resp.Error(
				"ERR wrong number of arguments for 'echo' command",
			)},
		},
		{
			name:    "QUIT wrong arity does not close",
			command: []string{"QUIT", "now"},
			want: Result{Response: resp.Error(
				"ERR wrong number of arguments for 'quit' command",
			)},
		},
	}

	engine := NewEngine()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := engine.Execute(stringsToBytes(test.command))
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Execute() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func stringsToBytes(values []string) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = []byte(value)
	}
	return result
}
