package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
)

const maxMessageSize = 4096

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

func query(conn net.Conn, text string) error {
	length := uint32(len(text))
	if length > maxMessageSize {
		return fmt.Errorf("message too large")
	}

	requestLength := make([]byte, 4)
	binary.LittleEndian.PutUint32(requestLength, length)
	err := writeAll(conn, requestLength)
	if err != nil {
		return err
	}
	
	request := []byte(text)
	err = writeAll(conn, request)
	if err != nil {
		return err
	}

	responseLength := make([]byte, 4)
	err = readFull(conn, responseLength)
	if err != nil {
		return err
	}
	
	responseLengthInt := binary.LittleEndian.Uint32(responseLength)
	if responseLengthInt > maxMessageSize {
		return fmt.Errorf("response too large")
	}

	response := make([]byte, responseLengthInt)
	err = readFull(conn, response)
	if err != nil {
		return err
	}

	fmt.Println(string(response))
	return nil
}

func main() {
	conn, err := net.Dial("tcp", ":1234")
	if err != nil {
		log.Fatal("Failed to connect to server:", err)
	}
	defer conn.Close()

	// Test multiple requests one-by-one (current approach)
	fmt.Println("=== Testing sequential requests ===")
	err = query(conn, "Hello, server! 1")
	if err != nil {
		log.Fatal("Failed to query server:", err)
	}
	
	err = query(conn, "Hello, server! 2")
	if err != nil {
		log.Fatal("Failed to query server:", err)
	}
	
	err = query(conn, "Hello, server! 3")
	if err != nil {
		log.Fatal("Failed to query server:", err)
	}

	fmt.Println("=== Testing pipelined requests ===")
	// Test pipelined requests (send all, then read all)
	requests := []string{"pipeline1", "pipeline2", "pipeline3"}
	
	// Send all requests first
	for _, req := range requests {
		length := uint32(len(req))
		if length > maxMessageSize {
			log.Fatal("Message too large")
		}
		
		requestLength := make([]byte, 4)
		binary.LittleEndian.PutUint32(requestLength, length)
		
		err = writeAll(conn, requestLength)
		if err != nil {
			log.Fatal("Failed to send request length:", err)
		}
		
		err = writeAll(conn, []byte(req))
		if err != nil {
			log.Fatal("Failed to send request:", err)
		}
	}
	
	// Now read all responses
	for i := 0; i < len(requests); i++ {
		responseLength := make([]byte, 4)
		err = readFull(conn, responseLength)
		if err != nil {
			log.Fatal("Failed to read response length:", err)
		}
		
		responseLengthInt := binary.LittleEndian.Uint32(responseLength)
		if responseLengthInt > maxMessageSize {
			log.Fatal("Response too large")
		}
		
		response := make([]byte, responseLengthInt)
		err = readFull(conn, response)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}
		
		fmt.Printf("server says: %s\n", string(response))
	}

	fmt.Println("All queries completed successfully")
}
