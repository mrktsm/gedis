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
	fmt.Println("client says:", string(buf))
	response := []byte("server says: hello")
	responseLength := make([]byte, 4)
	binary.LittleEndian.PutUint32(responseLength, uint32(len(response)))
	err = writeAll(c, responseLength)
	if err != nil {
		return err
	}
	err = writeAll(c, response)
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


