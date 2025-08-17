package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"

	"redis-in-go/pkg/protocol"
	"redis-in-go/pkg/storage"
)


var dataStore = make(map[string]string)
var dataStoreMutex sync.RWMutex

var sortedSets = make(map[string]*storage.ZSet)

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
	case "ZADD":
		if len(cmd) != 4 {
			return 1, []byte("ERR wrong number of arguments for 'zadd' command")
		}
		return handleZAdd(cmd[1], cmd[2], cmd[3])
	case "ZREM":
		if len(cmd) != 3 {
			return 1, []byte("ERR wrong number of arguments for 'zrem' command")
		}
		return handleZRem(cmd[1], cmd[2])
	case "ZSCORE":
		if len(cmd) != 3 {
			return 1, []byte("ERR wrong number of arguments for 'zscore' command")
		}
		return handleZScore(cmd[1], cmd[2])
	case "ZRANGE":
		if len(cmd) != 4 {
			return 1, []byte("ERR wrong number of arguments for 'zrange' command")
		}
		return handleZRange(cmd[1], cmd[2], cmd[3])
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

func handleZAdd(key, scoreStr, member string) (uint32, []byte) {
	score, err := strconv.ParseFloat(scoreStr, 64)
	if err != nil {
		return 1, []byte("ERR invalid score")
	}

	dataStoreMutex.Lock()
	defer dataStoreMutex.Unlock()

	zset, exists := sortedSets[key]
	if !exists {
		zset = storage.NewZSet()
		sortedSets[key] = zset
	}

	zset.Add(score, member)
	return 0, []byte("OK")
}

func handleZRem(key, member string) (uint32, []byte) {
    dataStoreMutex.RLock()
    zset, exists := sortedSets[key]
    dataStoreMutex.RUnlock()

    if !exists {
        return 1, []byte("ERR key not found")
    }

    removed := zset.Remove(member)
    if !removed {
        return 1, []byte("ERR member not found")
    }

    return 0, []byte("OK")
}

func handleZScore(key, member string) (uint32, []byte) {
	dataStoreMutex.RLock()
	zset, exists := sortedSets[key]
	dataStoreMutex.RUnlock()

	if !exists {
		return 1, []byte("ERR key not found")
	}

	score, found := zset.GetScore(member)
	if !found {
		return 1, []byte("ERR member not found")
	}

	response := fmt.Sprintf("%.1f", score)
	return 0, []byte(response)
}


func handleZRange(key, minStr, maxStr string) (uint32, []byte) {
	min, err := strconv.ParseFloat(minStr, 64)
	if err != nil {
		return 1, []byte("ERR invalid min score")
	}

	max, err := strconv.ParseFloat(maxStr, 64)
	if err != nil {
		return 1, []byte("ERR invalid max score")
	}

	dataStoreMutex.RLock()
	zset, exists := sortedSets[key]
	dataStoreMutex.RUnlock()

	if !exists {
		return 1, []byte("ERR key not found")
	}

	entries := zset.Range(min, max)
	var result []string
	for _, entry := range entries {
        result = append(result, fmt.Sprintf("%s:%.1f", entry.Member, entry.Score))
	}

	if len(result) == 0 {
		return 0, []byte("[]")
	}

    response := fmt.Sprintf("[%s]", strings.Join(result, ","))
    return 0, []byte(response)
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


