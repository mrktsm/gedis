package main

import (
	"bytes"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestParseHealthOptions(t *testing.T) {
	t.Parallel()

	got, err := parseHealthOptions([]string{"-addr", "gedis:6380", "-timeout", "250ms"}, io.Discard)
	if err != nil {
		t.Fatalf("parseHealthOptions() error = %v", err)
	}
	want := healthOptions{address: "gedis:6380", timeout: 250 * time.Millisecond}
	if got != want {
		t.Fatalf("parseHealthOptions() = %#v, want %#v", got, want)
	}
	for _, arguments := range [][]string{
		{"positional"},
		{"-addr", ""},
		{"-timeout", "0"},
		{"-timeout", "-1s"},
	} {
		if _, err := parseHealthOptions(arguments, io.Discard); err == nil {
			t.Errorf("parseHealthOptions(%q) error = nil", arguments)
		}
	}
}

func TestCheckHealthUsesRESPPing(t *testing.T) {
	t.Parallel()

	address, requests, stop := startHealthTarget(t, resp.SimpleString("PONG"))
	defer stop()
	if err := checkHealth(address, time.Second); err != nil {
		t.Fatalf("checkHealth() error = %v", err)
	}
	if got := <-requests; !reflect.DeepEqual(got, [][]byte{[]byte("PING")}) {
		t.Fatalf("health request = %q", got)
	}
}

func TestCheckHealthRejectsUnexpectedResponse(t *testing.T) {
	t.Parallel()

	address, _, stop := startHealthTarget(t, resp.SimpleString("OK"))
	defer stop()
	if err := checkHealth(address, time.Second); err == nil || !strings.Contains(err.Error(), "PONG") {
		t.Fatalf("checkHealth() error = %v", err)
	}
}

func TestRunReportsUnhealthyTarget(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	var output bytes.Buffer
	if code := run([]string{"-addr", address, "-timeout", "100ms"}, &output); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(output.String(), "health check failed") {
		t.Fatalf("run() output = %q", output.String())
	}
}

func startHealthTarget(t *testing.T, response resp.Value) (string, <-chan [][]byte, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requests := make(chan [][]byte, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		command, err := resp.NewReader(connection).ReadCommand()
		if err != nil {
			return
		}
		requests <- command
		_ = resp.NewWriter(connection).WriteValue(response)
	}()
	stop := func() {
		_ = listener.Close()
		<-done
	}
	return listener.Addr().String(), requests, stop
}
