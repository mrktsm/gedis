package protocol

import (
	"encoding/binary"
	"fmt"
	"net"
)

const MaxMessageSize = 4096

func ReadFull(conn net.Conn, buf []byte) error {
	bytesRead := 0
	for bytesRead < len(buf) {
		n, err := conn.Read(buf[bytesRead:])
		if err != nil {
			return err
		}
		bytesRead += n
	}
	return nil
}

func WriteAll(conn net.Conn, data []byte) error {
	bytesWritten := 0
	for bytesWritten < len(data) {
		n, err := conn.Write(data[bytesWritten:])
		if err != nil {
			return err
		}
		bytesWritten += n
	}
	return nil
}

func ReadMessage(conn net.Conn) ([]byte, error) {
	// Read the 4-byte length header
	lengthBytes := make([]byte, 4)
	err := ReadFull(conn, lengthBytes)
	if err != nil {
		return nil, err
	}

	// Parse the length
	length := binary.LittleEndian.Uint32(lengthBytes)
	if length > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	// Read the message body
	message := make([]byte, length)
	err = ReadFull(conn, message)
	if err != nil {
		return nil, err
	}

	return message, nil
}

func WriteMessage(conn net.Conn, data []byte) error {
	length := uint32(len(data))
	if length > MaxMessageSize {
		return fmt.Errorf("message too large: %d bytes", length)
	}

	lengthBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(lengthBytes, length)
	err := WriteAll(conn, lengthBytes)
	if err != nil {
		return err
	}

	return WriteAll(conn, data)
}
