package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
)

const maxMessageSize = 4096
var dataStore = make(map[string]string)
var dataStoreMutex sync.RWMutex

// var sortedSets = make(map[string]*storage.ZSet)

func readFull(conn net.Conn, buf []byte) error {
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

func writeAll(conn net.Conn, data []byte) error {
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

func parseCommand(data []byte) ([]string, error) {
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

	// Start reading after the nstr
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

func executeCommand(cmd []string) (uint32, []byte) {
	if len(cmd) == 0 {
		return 1, []byte("ERR empty command")
	}

	switch cmd[0] {
	case "GET":
		if len(cmd) != 2 {
			return 1, []byte("ERR wrong number of arguments for 'get' command")
		}
		return handleGet(cmd[1])
	case "SET":
		if len(cmd) != 3 {
			return 1, []byte("ERR wrong number of arguments for 'set' command")
		}
		return handleSet(cmd[1], cmd[2])
	case "DEL":
		if len(cmd) != 2 {
			return 1, []byte("ERR wrong number of arguments for 'del' command")
		}
		return handleDel(cmd[1])	
	default:
		return 1, []byte("ERR unknown command")
	}
}

func handleGet(key string) (uint32, []byte) {
	dataStoreMutex.RLock()
	defer dataStoreMutex.RUnlock()

	value, ok := dataStore[key]
	if !ok {
		return 1, []byte("ERR value not found")
	}

	response := []byte(value)
	return 0, response
}

func handleSet(key, value string) (uint32, []byte) {
	dataStoreMutex.Lock()
	defer dataStoreMutex.Unlock()

	dataStore[key] = value
	return 0, []byte("OK")
}

func handleDel(key string) (uint32, []byte) {
	dataStoreMutex.Lock()
	defer dataStoreMutex.Unlock()

	delete(dataStore, key)
	return 0, []byte("OK")
}

func sendResponse(c net.Conn, status uint32, data []byte) error {
    totalLength := uint32(4 + len(data))  // 4 bytes for status + data length
    
    lengthBytes := make([]byte, 4)
    binary.LittleEndian.PutUint32(lengthBytes, totalLength)
    err := writeAll(c, lengthBytes)
    if err != nil {
        return err
    }
    
    statusBytes := make([]byte, 4)
    binary.LittleEndian.PutUint32(statusBytes, status)
    err = writeAll(c, statusBytes)
    if err != nil {
        return err
    }
    
    err = writeAll(c, data)
    if err != nil {
        return err
    }
    
    return nil
}

func handleRequest(c net.Conn) error {
	length := make([]byte, 4)
	err := readFull(c, length)
	if err != nil {
		return err
	}

	lengthInt := binary.LittleEndian.Uint32(length)
	if lengthInt > maxMessageSize {
		return fmt.Errorf("message too large")
	}

	buf := make([]byte, lengthInt)
	err = readFull(c, buf)
	if err != nil {
		return err
	}
	// fmt.Println("client says:", string(buf))

	cmd, err := parseCommand(buf)
	if err != nil {
		return err
	}

	status, responseData := executeCommand(cmd)
	err = sendResponse(c, status, responseData)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	ln, err := net.Listen("tcp", ":1234")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept error:", err)
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			for {
				err := handleRequest(c)
				if err != nil {
					log.Println("Handle request error:", err)
					break
				}
			}
		}(conn)
	}
}


