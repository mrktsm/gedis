package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestCommandCountMatchesRegistry(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	count := engine.Execute(stringsToBytes([]string{"COMMAND", "COUNT"})).Response
	all := engine.Execute(stringsToBytes([]string{"COMMAND"})).Response
	if count.Kind() != resp.KindInteger || count.Int64() != int64(len(engine.Commands())) {
		t.Fatalf("COMMAND COUNT = %#v, commands = %d", count, len(engine.Commands()))
	}
	if all.Kind() != resp.KindArray || len(all.Values()) != int(count.Int64()) {
		t.Fatalf("COMMAND entries = %d, count = %d", len(all.Values()), count.Int64())
	}
}

func TestCommandInfoUsesRedisLegacyFields(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	response := engine.Execute(stringsToBytes([]string{"COMMAND", "INFO", "GET", "missing", "mset"})).Response
	want := resp.Array(
		resp.Array(
			resp.BulkStringString("get"),
			resp.Integer(2),
			resp.Array(resp.SimpleString("readonly"), resp.SimpleString("fast")),
			resp.Integer(1),
			resp.Integer(1),
			resp.Integer(1),
		),
		resp.NullArray(),
		resp.Array(
			resp.BulkStringString("mset"),
			resp.Integer(-3),
			resp.Array(resp.SimpleString("write")),
			resp.Integer(1),
			resp.Integer(-1),
			resp.Integer(2),
		),
	)
	if !reflect.DeepEqual(response, want) {
		t.Fatalf("COMMAND INFO = %#v, want %#v", response, want)
	}
}

func TestCommandInfoWithoutNamesReturnsSortedRegistry(t *testing.T) {
	t.Parallel()

	response := NewEngine().Execute(stringsToBytes([]string{"COMMAND", "INFO"})).Response
	if response.Kind() != resp.KindArray || len(response.Values()) == 0 {
		t.Fatalf("COMMAND INFO = %#v", response)
	}
	previous := ""
	for _, value := range response.Values() {
		name := string(value.Values()[0].Bytes())
		if name <= previous {
			t.Fatalf("COMMAND INFO order: %q after %q", name, previous)
		}
		previous = name
	}
}

func TestCommandSubcommandErrorsAndHelp(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	for _, command := range [][]string{
		{"COMMAND", "COUNT", "extra"},
		{"COMMAND", "HELP", "extra"},
	} {
		if response := engine.Execute(stringsToBytes(command)).Response; response.Kind() != resp.KindError {
			t.Errorf("Execute(%q) = %#v, want error", command, response)
		}
	}
	unknown := engine.Execute(stringsToBytes([]string{"COMMAND", "bad\r\nname"})).Response
	if unknown.Kind() != resp.KindError || strings.Contains(string(unknown.Bytes()), "\r\n") {
		t.Fatalf("COMMAND unknown = %#v", unknown)
	}
	help := engine.Execute(stringsToBytes([]string{"COMMAND", "HELP"})).Response
	if help.Kind() != resp.KindArray || len(help.Values()) != 4 {
		t.Fatalf("COMMAND HELP = %#v", help)
	}
}

func TestCommandMetadataIsCompleteAndIndependent(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	commands := engine.Commands()
	if len(commands) != 30 {
		t.Fatalf("Commands() length = %d, want 30", len(commands))
	}
	for _, command := range commands {
		if command.Name == "" || command.Arity == 0 || command.Group == "" || command.Syntax == "" {
			t.Errorf("incomplete command metadata: %#v", command)
		}
		if command.FirstKey == 0 && (command.LastKey != 0 || command.KeyStep != 0) {
			t.Errorf("invalid key metadata: %#v", command)
		}
	}
	commands[0].Flags = append(commands[0].Flags, "mutated")
	again := engine.Commands()
	if reflect.DeepEqual(commands[0].Flags, again[0].Flags) {
		t.Fatal("Commands() returned registry-owned flags")
	}
}
