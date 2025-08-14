package main

import (
	"log"
	"net"
	"sync"

	"redis-in-go/pkg/protocol"
)


var dataStore = make(map[string]string)
var dataStoreMutex sync.RWMutex

// var sortedSets = make(map[string]*storage.ZSet)

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



func handleRequest(c net.Conn) error {
	// Read the command message
	buf, err := protocol.ReadMessage(c)
	if err != nil {
		return err
	}

	// Parse the command
	cmd, err := protocol.ParseCommand(buf)
	if err != nil {
		return err
	}

	// Execute the command
	status, responseData := executeCommand(cmd)
	
	// Send the response
	return protocol.SendResponse(c, status, responseData)
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


