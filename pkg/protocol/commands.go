package protocol

import (
	"encoding/binary"
	"fmt"
	"net"
)

func ParseCommand(data []byte) ([]string, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("command too short")
	}

	nstr := binary.LittleEndian.Uint32(data[:4])

	if nstr == 0 {
		return nil, fmt.Errorf("empty command")
	}

	if nstr > 10 {
		return nil, fmt.Errorf("too many arguments")
	}

	cmd := make([]string, 0, nstr)

	offset := 4
	
	for i := uint32(0); i < nstr; i++ {
		if offset + 4 > len(data) {
			return nil, fmt.Errorf("incomplete string length")
		}

		length := binary.LittleEndian.Uint32(data[offset:offset+4])
		offset += 4

		if offset + int(length) > len(data) {
			return nil, fmt.Errorf("incomplete string")
		}

		str := string(data[offset:offset+int(length)])
		cmd = append(cmd, str)
		offset += int(length)
	}

	return cmd, nil
}

func SendResponse(conn net.Conn, status uint32, data []byte) error {
	totalLength := uint32(4 + len(data))
	
	lengthBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(lengthBytes, totalLength)
	err := WriteAll(conn, lengthBytes)
	if err != nil {
		return err
	}
	
	statusBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(statusBytes, status)
	err = WriteAll(conn, statusBytes)
	if err != nil {
		return err
	}
	
	return WriteAll(conn, data)
}
