package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrktsm/gedis/internal/server"
)

func TestRenderCommandTableUsesRegistryAndEscapesSyntax(t *testing.T) {
	t.Parallel()

	commands := server.NewEngine().Commands()
	got := renderCommandTable(commands)
	if !strings.Contains(got, "currently exposes 30 top-level engine commands") {
		t.Fatalf("table count missing:\n%s", got)
	}
	if rows := strings.Count(got, "\n| "); rows != len(commands)+2 {
		t.Fatalf("table rows = %d, want %d", rows, len(commands)+2)
	}
	if !strings.Contains(got, "`SET key value [NX\\|XX]") {
		t.Fatal("SET option pipe was not escaped")
	}
	if !strings.Contains(got, "| server | `COMMAND` |") {
		t.Fatal("COMMAND registry row missing")
	}
}

func TestWriteCommandTablePropagatesWriterError(t *testing.T) {
	t.Parallel()

	if err := writeCommandTable(failingWriter{}, server.NewEngine().Commands()); !errors.Is(err, errWriteTable) {
		t.Fatalf("writeCommandTable() error = %v", err)
	}
}

var errWriteTable = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errWriteTable
}
