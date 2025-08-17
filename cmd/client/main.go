package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"

	"redis-in-go/pkg/protocol"
)

func sendCommand(conn net.Conn, cmd []string) error {
	// Convert command array to the format the server expects
	var cmdBytes []byte
	
	// Add number of strings (4 bytes, little endian)
	nstrBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(nstrBytes, uint32(len(cmd)))
	cmdBytes = append(cmdBytes, nstrBytes...)
	
	// Add each string with its length prefix
	for _, str := range cmd {
		strBytes := []byte(str)
		// Add string length (4 bytes, little endian)
		lenBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(lenBytes, uint32(len(strBytes)))
		cmdBytes = append(cmdBytes, lenBytes...)
		cmdBytes = append(cmdBytes, strBytes...)
	}

	// Send the command using protocol package
	err := protocol.WriteMessage(conn, cmdBytes)
	if err != nil {
		return err
	}

	// Read the response using protocol package
	response, err := protocol.ReadMessage(conn)
	if err != nil {
		return err
	}

	fmt.Printf("Response: %s\n", string(response))
	return nil
}

func main() {
	conn, err := net.Dial("tcp", ":1234")
	if err != nil {
		log.Fatal("Failed to connect to server:", err)
	}
	defer conn.Close()

	fmt.Println("=== Testing Redis ZSet Commands ===")
	
	// Test basic key-value commands first
	fmt.Println("\n1. Testing basic commands:")
	err = sendCommand(conn, []string{"SET", "mykey", "myvalue"})
	if err != nil {
		log.Fatal("Failed to send SET:", err)
	}
	
	err = sendCommand(conn, []string{"GET", "mykey"})
	if err != nil {
		log.Fatal("Failed to send GET:", err)
	}

	// Test ZADD - Add members to leaderboard
	fmt.Println("\n2. Adding players to leaderboard:")
	err = sendCommand(conn, []string{"ZADD", "leaderboard", "1500", "player1"})
	if err != nil {
		log.Fatal("Failed to send ZADD:", err)
	}
	
	err = sendCommand(conn, []string{"ZADD", "leaderboard", "2000", "player2"})
	if err != nil {
		log.Fatal("Failed to send ZADD:", err)
	}
	
	err = sendCommand(conn, []string{"ZADD", "leaderboard", "1200", "player3"})
	if err != nil {
		log.Fatal("Failed to send ZADD:", err)
	}
	
	err = sendCommand(conn, []string{"ZADD", "leaderboard", "1800", "player4"})
	if err != nil {
		log.Fatal("Failed to send ZADD:", err)
	}

	// Test ZSCORE - Get individual scores
	fmt.Println("\n3. Getting individual scores:")
	err = sendCommand(conn, []string{"ZSCORE", "leaderboard", "player1"})
	if err != nil {
		log.Fatal("Failed to send ZSCORE:", err)
	}
	
	err = sendCommand(conn, []string{"ZSCORE", "leaderboard", "player2"})
	if err != nil {
		log.Fatal("Failed to send ZSCORE:", err)
	}

	// Test ZRANGE - Get players in score range
	fmt.Println("\n4. Getting players with scores 1000-1800:")
	err = sendCommand(conn, []string{"ZRANGE", "leaderboard", "1000", "1800"})
	if err != nil {
		log.Fatal("Failed to send ZRANGE:", err)
	}

	// Test ZREM - Remove a player
	fmt.Println("\n5. Removing player3:")
	err = sendCommand(conn, []string{"ZREM", "leaderboard", "player3"})
	if err != nil {
		log.Fatal("Failed to send ZREM:", err)
	}

	// Test ZINCRBY - Increment scores
	fmt.Println("\n6. Testing ZINCRBY - Give player1 bonus points:")
	err = sendCommand(conn, []string{"ZINCRBY", "leaderboard", "250", "player1"})
	if err != nil {
		log.Fatal("Failed to send ZINCRBY:", err)
	}
	
	err = sendCommand(conn, []string{"ZINCRBY", "leaderboard", "-100", "player2"})
	if err != nil {
		log.Fatal("Failed to send ZINCRBY:", err)
	}
	
	// Test ZINCRBY with new member
	err = sendCommand(conn, []string{"ZINCRBY", "leaderboard", "1000", "newplayer"})
	if err != nil {
		log.Fatal("Failed to send ZINCRBY:", err)
	}

	// Test ZRANGE again - Full leaderboard
	fmt.Println("\n7. Final leaderboard (scores 0-3000):")
	err = sendCommand(conn, []string{"ZRANGE", "leaderboard", "0", "3000"})
	if err != nil {
		log.Fatal("Failed to send ZRANGE:", err)
	}

	// Test edge cases
	fmt.Println("\n8. Testing edge cases:")
	
	// Try to get score of non-existent member
	err = sendCommand(conn, []string{"ZSCORE", "leaderboard", "nonexistent"})
	if err != nil {
		log.Fatal("Failed to send ZSCORE:", err)
	}
	
	// Try to get range from non-existent key
	err = sendCommand(conn, []string{"ZRANGE", "nonexistent", "0", "100"})
	if err != nil {
		log.Fatal("Failed to send ZRANGE:", err)
	}

	fmt.Println("\n✅ All ZSet tests completed successfully!")
}
