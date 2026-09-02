package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
)

func TestServerHandlesPipelinedCommands(t *testing.T) {
	server, address := startTestServer(t)
	defer stopTestServer(t, server)

	connection := dialTestServer(t, address)
	defer connection.Close()

	writer := resp.NewWriter(connection)
	if err := writer.WriteCommand([]byte("PING")); err != nil {
		t.Fatalf("write PING: %v", err)
	}
	if err := writer.WriteCommand([]byte("ECHO"), []byte("hello\x00world")); err != nil {
		t.Fatalf("write ECHO: %v", err)
	}

	reader := resp.NewReader(connection)
	first, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read PING response: %v", err)
	}
	second, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read ECHO response: %v", err)
	}

	if first.Kind() != resp.KindSimpleString || string(first.Bytes()) != "PONG" {
		t.Fatalf("PING response = kind %q, value %q", first.Kind(), first.Bytes())
	}
	if second.Kind() != resp.KindBulkString || string(second.Bytes()) != "hello\x00world" {
		t.Fatalf("ECHO response = kind %q, value %q", second.Kind(), second.Bytes())
	}
}

func TestServerClosesConnectionAfterQUIT(t *testing.T) {
	server, address := startTestServer(t)
	defer stopTestServer(t, server)

	connection := dialTestServer(t, address)
	defer connection.Close()

	if err := resp.NewWriter(connection).WriteCommand([]byte("QUIT")); err != nil {
		t.Fatalf("write QUIT: %v", err)
	}

	reader := resp.NewReader(connection)
	response, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read QUIT response: %v", err)
	}
	if response.Kind() != resp.KindSimpleString || string(response.Bytes()) != "OK" {
		t.Fatalf("QUIT response = kind %q, value %q", response.Kind(), response.Bytes())
	}

	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := reader.ReadValue(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after QUIT error = %v, want EOF", err)
	}
}

func TestServerReportsProtocolErrorAndCloses(t *testing.T) {
	server, address := startTestServer(t)
	defer stopTestServer(t, server)

	connection := dialTestServer(t, address)
	defer connection.Close()

	if _, err := io.WriteString(connection, "*1\r\n+PING\r\n"); err != nil {
		t.Fatalf("write malformed command: %v", err)
	}

	reader := resp.NewReader(connection)
	response, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read protocol error: %v", err)
	}
	if response.Kind() != resp.KindError || !strings.Contains(string(response.Bytes()), "Protocol error") {
		t.Fatalf("protocol response = kind %q, value %q", response.Kind(), response.Bytes())
	}

	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := reader.ReadValue(); !errors.Is(err, io.EOF) {
		t.Fatalf("read after protocol error = %v, want EOF", err)
	}
}

func TestServerHandlesConcurrentClients(t *testing.T) {
	server, address := startTestServer(t)
	defer stopTestServer(t, server)

	const clients = 32
	var waitGroup sync.WaitGroup
	waitGroup.Add(clients)
	for range clients {
		go func() {
			defer waitGroup.Done()
			connection, err := net.DialTimeout("tcp", address, time.Second)
			if err != nil {
				t.Errorf("dial %s: %v", address, err)
				return
			}
			defer connection.Close()

			if err := resp.NewWriter(connection).WriteCommand([]byte("PING")); err != nil {
				t.Errorf("write PING: %v", err)
				return
			}
			response, err := resp.NewReader(connection).ReadValue()
			if err != nil {
				t.Errorf("read PING: %v", err)
				return
			}
			if string(response.Bytes()) != "PONG" {
				t.Errorf("PING response = %q, want PONG", response.Bytes())
			}
		}()
	}
	waitGroup.Wait()
}

func TestServerShutdownClosesActiveConnections(t *testing.T) {
	server, address := startTestServer(t)
	connection := dialTestServer(t, address)
	defer connection.Close()

	stopTestServer(t, server)

	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 1)
	if _, err := connection.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("read after shutdown error = %v, want EOF", err)
	}
}

func TestServerConnectionHandlerCanUpgradeUntilShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	handler := &blockingConnectionHandler{started: make(chan struct{})}
	config := DefaultConfig()
	config.ConnectionCommandHandler = handler
	server := New(config, NewEngine())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	connection := dialTestServer(t, listener.Addr().String())
	defer connection.Close()
	writer := resp.NewWriter(connection)
	reader := resp.NewReader(connection)
	if err := writer.WriteCommand([]byte("PING")); err != nil {
		t.Fatalf("write PING: %v", err)
	}
	if response, err := reader.ReadValue(); err != nil || string(response.Bytes()) != "PONG" {
		t.Fatalf("PING response = %#v, %v", response, err)
	}
	if err := writer.WriteCommand([]byte("STREAM")); err != nil {
		t.Fatalf("write STREAM: %v", err)
	}
	if response, err := reader.ReadValue(); err != nil || string(response.Bytes()) != "UPGRADED" {
		t.Fatalf("STREAM response = %#v, %v", response, err)
	}
	<-handler.started
	stopTestServer(t, server)
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestServerMetricsExcludeReplicaStreamsFromConnectedClients(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	handler := &blockingConnectionHandler{started: make(chan struct{})}
	config := DefaultConfig()
	config.ConnectionCommandHandler = handler
	server := New(config, NewEngine())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	replica := dialTestServer(t, listener.Addr().String())
	defer replica.Close()
	if err := resp.NewWriter(replica).WriteCommand([]byte("STREAM")); err != nil {
		t.Fatalf("write STREAM: %v", err)
	}
	if response, err := resp.NewReader(replica).ReadValue(); err != nil || string(response.Bytes()) != "UPGRADED" {
		t.Fatalf("STREAM response = %#v, %v", response, err)
	}
	<-handler.started

	observer := dialTestServer(t, listener.Addr().String())
	defer observer.Close()
	writer := resp.NewWriter(observer)
	reader := resp.NewReader(observer)
	if err := writer.WriteCommand([]byte("INFO"), []byte("clients")); err != nil {
		t.Fatalf("write INFO clients: %v", err)
	}
	clients, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read INFO clients: %v", err)
	}
	assertInfoFieldValues(t, string(clients.Bytes()), map[string]string{
		"connected_clients":                   "1",
		"gedis_connected_replica_connections": "1",
		"gedis_connected_connections":         "2",
	})

	if err := writer.WriteCommand([]byte("INFO"), []byte("stats")); err != nil {
		t.Fatalf("write INFO stats: %v", err)
	}
	stats, err := reader.ReadValue()
	if err != nil {
		t.Fatalf("read INFO stats: %v", err)
	}
	assertInfoFieldValues(t, string(stats.Bytes()), map[string]string{
		"total_connections_received": "2",
		"total_commands_processed":   "3",
	})

	stopTestServer(t, server)
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestServerMetricsCountProtocolErrors(t *testing.T) {
	server, address := startTestServer(t)
	defer stopTestServer(t, server)

	malformed := dialTestServer(t, address)
	if _, err := io.WriteString(malformed, "*1\r\n+PING\r\n"); err != nil {
		t.Fatalf("write malformed command: %v", err)
	}
	if _, err := resp.NewReader(malformed).ReadValue(); err != nil {
		t.Fatalf("read protocol error: %v", err)
	}
	_ = malformed.Close()

	observer := dialTestServer(t, address)
	defer observer.Close()
	if err := resp.NewWriter(observer).WriteCommand([]byte("INFO"), []byte("stats")); err != nil {
		t.Fatalf("write INFO stats: %v", err)
	}
	response, err := resp.NewReader(observer).ReadValue()
	if err != nil {
		t.Fatalf("read INFO stats: %v", err)
	}
	assertInfoFieldValues(t, string(response.Bytes()), map[string]string{
		"total_connections_received": "2",
		"total_commands_processed":   "1",
		"gedis_command_errors":       "0",
		"gedis_protocol_errors":      "1",
	})
}

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := New(DefaultConfig(), NewEngine())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		default:
		}
	})
	return server, listener.Addr().String()
}

func stopTestServer(t *testing.T, server *Server) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func dialTestServer(t *testing.T, address string) net.Conn {
	t.Helper()

	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	return connection
}

type blockingConnectionHandler struct {
	started chan struct{}
}

func (h *blockingConnectionHandler) ClassifyConnectionCommand(command [][]byte) ConnectionRole {
	if len(command) > 0 && string(command[0]) == "STREAM" {
		return ConnectionRoleReplica
	}
	return ConnectionRoleClient
}

func (h *blockingConnectionHandler) HandleConnectionCommand(
	ctx context.Context,
	command [][]byte,
	_ net.Conn,
	writer *bufio.Writer,
) (bool, bool, error) {
	if len(command) == 0 || string(command[0]) != "STREAM" {
		return false, false, nil
	}
	if err := resp.NewWriter(writer).WriteValue(resp.SimpleString("UPGRADED")); err != nil {
		return true, true, err
	}
	if err := writer.Flush(); err != nil {
		return true, true, err
	}
	close(h.started)
	<-ctx.Done()
	return true, true, nil
}
