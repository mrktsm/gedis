package replication

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrktsm/gedis/internal/resp"
	"github.com/mrktsm/gedis/internal/server"
	"github.com/mrktsm/gedis/internal/store"
)

func TestReplicationProtocolFullSyncThenLiveMutation(t *testing.T) {
	t.Parallel()

	primary, engine := newProtocolPrimary(t)
	assertReplicationResponse(t, engine, []string{"SET", "key", "snapshot"}, resp.SimpleString("OK"))

	client, cancel, done := startProtocolStream(t, primary, byteCommand("PSYNC", "?", "-1"))
	defer func() {
		cancel()
		_ = client.Close()
		if err := <-done; err != nil {
			t.Errorf("HandleConnectionCommand() error = %v", err)
		}
	}()
	buffered := bufio.NewReader(client)
	header, err := buffered.ReadString('\n')
	if err != nil {
		t.Fatalf("read FULLRESYNC header: %v", err)
	}
	wantHeader := "+FULLRESYNC " + testReplicationID + " " + strconv.FormatInt(primary.Stats().Offset, 10) + "\r\n"
	if header != wantHeader {
		t.Fatalf("FULLRESYNC header = %q, want %q", header, wantHeader)
	}
	lengthLine, err := buffered.ReadString('\n')
	if err != nil || !strings.HasPrefix(lengthLine, "$") {
		t.Fatalf("read snapshot length = %q, %v", lengthLine, err)
	}
	length, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(lengthLine, "$"), "\r\n"))
	if err != nil {
		t.Fatalf("parse snapshot length %q: %v", lengthLine, err)
	}
	snapshotData := make([]byte, length)
	if _, err := io.ReadFull(buffered, snapshotData); err != nil {
		t.Fatalf("read snapshot payload: %v", err)
	}
	snapshotReader := resp.NewReader(bytes.NewReader(snapshotData))
	snapshotCommand, err := snapshotReader.ReadCommand()
	if err != nil || string(snapshotCommand[0]) != "SET" || string(snapshotCommand[2]) != "snapshot" {
		t.Fatalf("snapshot command = %q, %v", snapshotCommand, err)
	}
	if _, err := snapshotReader.ReadCommand(); !errors.Is(err, io.EOF) {
		t.Fatalf("snapshot trailing read error = %v, want EOF", err)
	}

	assertReplicationResponse(t, engine, []string{"SET", "key", "live"}, resp.SimpleString("OK"))
	live, err := resp.NewReader(buffered).ReadCommand()
	if err != nil || string(live[0]) != "SET" || string(live[2]) != "live" {
		t.Fatalf("live command = %q, %v", live, err)
	}
}

func TestReplicationProtocolPartialSyncUsesRequestedNextOffset(t *testing.T) {
	t.Parallel()

	primary, engine := newProtocolPrimary(t)
	first := byteCommand("SET", "one", "1")
	second := byteCommand("SET", "two", "2")
	engine.Execute(first)
	firstBytes, _ := encodeCommand(first)
	engine.Execute(second)

	requestOffset := int64(len(firstBytes)) + 1
	client, cancel, done := startProtocolStream(t, primary, byteCommand(
		"PSYNC", testReplicationID, strconv.FormatInt(requestOffset, 10),
	))
	defer func() {
		cancel()
		_ = client.Close()
		if err := <-done; err != nil {
			t.Errorf("HandleConnectionCommand() error = %v", err)
		}
	}()
	reader := resp.NewReader(client)
	header, err := reader.ReadValue()
	if err != nil || string(header.Bytes()) != "CONTINUE "+testReplicationID {
		t.Fatalf("CONTINUE header = %#v, %v", header, err)
	}
	replayed, err := reader.ReadCommand()
	if err != nil || string(replayed[1]) != "two" {
		t.Fatalf("partial command = %q, %v", replayed, err)
	}
}

func TestReplicationProtocolHandlesReplConfAndMalformedPSync(t *testing.T) {
	t.Parallel()

	primary, _ := newProtocolPrimary(t)
	serverConnection, clientConnection := net.Pipe()
	defer serverConnection.Close()
	defer clientConnection.Close()
	buffered := bufio.NewWriter(serverConnection)

	done := make(chan error, 1)
	go func() {
		handled, terminal, err := primary.HandleConnectionCommand(
			context.Background(),
			byteCommand("REPLCONF", "capa", "psync2"),
			serverConnection,
			buffered,
		)
		if !handled || terminal {
			done <- errors.New("REPLCONF was not handled as a non-terminal command")
			return
		}
		done <- err
	}()
	response, err := resp.NewReader(clientConnection).ReadValue()
	if err != nil || string(response.Bytes()) != "OK" {
		t.Fatalf("REPLCONF response = %#v, %v", response, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("REPLCONF handler error = %v", err)
	}

	go func() {
		_, _, err := primary.HandleConnectionCommand(
			context.Background(),
			byteCommand("PSYNC", testReplicationID, "not-an-offset"),
			serverConnection,
			buffered,
		)
		done <- err
	}()
	response, err = resp.NewReader(clientConnection).ReadValue()
	if err != nil || response.Kind() != resp.KindError {
		t.Fatalf("malformed PSYNC response = %#v, %v", response, err)
	}
	if err := <-done; err != nil {
		t.Fatalf("malformed PSYNC handler error = %v", err)
	}
}

func newProtocolPrimary(t *testing.T) (*Primary, *server.Engine) {
	t.Helper()
	primary := newTestPrimary(t, 4096, 8, nil)
	engine := server.NewEngineWithStoreAndSink(store.New(), primary)
	primary.SetSnapshotter(engine)
	return primary, engine
}

func startProtocolStream(
	t *testing.T,
	primary *Primary,
	command [][]byte,
) (net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	serverConnection, clientConnection := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		defer serverConnection.Close()
		handled, terminal, err := primary.HandleConnectionCommand(
			ctx,
			command,
			serverConnection,
			bufio.NewWriter(serverConnection),
		)
		if !handled || !terminal {
			done <- errors.New("PSYNC was not handled as a terminal command")
			return
		}
		done <- err
	}()
	_ = clientConnection.SetDeadline(time.Now().Add(2 * time.Second))
	return clientConnection, cancel, done
}
