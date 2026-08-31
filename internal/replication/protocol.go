package replication

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mrktsm/gedis/internal/resp"
)

// HandleConnectionCommand implements the Redis-shaped handshake used by Gedis
// replicas. FULLRESYNC carries a Redis-style length-prefixed transfer of
// canonical RESP mutations rather than an RDB payload; the incremental stream
// is canonical RESP commands.
func (p *Primary) HandleConnectionCommand(
	ctx context.Context,
	command [][]byte,
	connection net.Conn,
	buffered *bufio.Writer,
) (bool, bool, error) {
	if len(command) == 0 {
		return false, false, nil
	}
	switch strings.ToUpper(string(command[0])) {
	case "REPLCONF":
		return p.handleReplConf(command, buffered)
	case "PSYNC":
		return p.handlePSync(ctx, command, connection, buffered)
	default:
		return false, false, nil
	}
}

func (p *Primary) handleReplConf(command [][]byte, buffered *bufio.Writer) (bool, bool, error) {
	if len(command) < 3 || len(command)%2 == 0 {
		return true, false, writeProtocolValue(buffered, resp.Error(
			"ERR wrong number of arguments for 'replconf' command",
		))
	}
	if strings.EqualFold(string(command[1]), "ACK") {
		return true, false, nil
	}
	return true, false, writeProtocolValue(buffered, resp.SimpleString("OK"))
}

func (p *Primary) handlePSync(
	ctx context.Context,
	command [][]byte,
	_ net.Conn,
	buffered *bufio.Writer,
) (bool, bool, error) {
	if len(command) != 3 {
		return true, false, writeProtocolValue(buffered, resp.Error(
			"ERR wrong number of arguments for 'psync' command",
		))
	}
	requestedOffset, err := strconv.ParseInt(string(command[2]), 10, 64)
	if err != nil {
		return true, false, writeProtocolValue(buffered, resp.Error(
			"ERR value is not an integer or out of range",
		))
	}

	requestedID := string(command[1])
	if requestedID != "?" && requestedOffset >= 0 {
		initial, subscription, _, ok := p.PartialSync(requestedID, requestedOffset-1)
		if ok {
			header := resp.SimpleString("CONTINUE " + p.Stats().ReplicationID)
			return true, true, streamReplication(ctx, buffered, header, nil, initial, subscription)
		}
	}

	full, subscription, err := p.FullSync()
	if err != nil {
		return true, false, writeProtocolValue(buffered, resp.Error(
			"ERR full synchronization failed: "+safeProtocolError(err),
		))
	}
	header := resp.SimpleString(fmt.Sprintf("FULLRESYNC %s %d", full.ReplicationID, full.Offset))
	return true, true, streamReplication(ctx, buffered, header, full.Data, nil, subscription)
}

func streamReplication(
	ctx context.Context,
	buffered *bufio.Writer,
	header resp.Value,
	snapshot []byte,
	initial []byte,
	subscription *Subscription,
) error {
	defer subscription.Close()
	writer := resp.NewWriter(buffered)
	if err := writer.WriteValue(header); err != nil {
		return err
	}
	if snapshot != nil {
		length := strconv.AppendInt([]byte{'$'}, int64(len(snapshot)), 10)
		length = append(length, '\r', '\n')
		if _, err := buffered.Write(length); err != nil {
			return err
		}
		if _, err := buffered.Write(snapshot); err != nil {
			return err
		}
	}
	if len(initial) > 0 {
		if _, err := buffered.Write(initial); err != nil {
			return err
		}
	}
	if err := buffered.Flush(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case chunk, ok := <-subscription.Chunks():
			if !ok {
				return nil
			}
			if _, err := buffered.Write(chunk.Data); err != nil {
				return err
			}
			if err := buffered.Flush(); err != nil {
				return err
			}
		}
	}
}

func writeProtocolValue(buffered *bufio.Writer, value resp.Value) error {
	if err := resp.NewWriter(buffered).WriteValue(value); err != nil {
		return err
	}
	return buffered.Flush()
}

func safeProtocolError(err error) string {
	return strings.NewReplacer("\r", "\\r", "\n", "\\n").Replace(err.Error())
}
