package main

import (
	"fmt"
	"log"
	"net"

	"redis-in-go/pkg/protocol"
)



func query(conn net.Conn, text string) error {
	// Send the message using protocol package
	err := protocol.WriteMessage(conn, []byte(text))
	if err != nil {
		return err
	}

	// Read the response using protocol package
	response, err := protocol.ReadMessage(conn)
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
	
	// Send all requests first using protocol package
	for _, req := range requests {
		err = protocol.WriteMessage(conn, []byte(req))
		if err != nil {
			log.Fatal("Failed to send request:", err)
		}
	}
	
	// Now read all responses using protocol package
	for i := 0; i < len(requests); i++ {
		response, err := protocol.ReadMessage(conn)
		if err != nil {
			log.Fatal("Failed to read response:", err)
		}
		
		fmt.Printf("server says: %s\n", string(response))
	}

	fmt.Println("All queries completed successfully")
}
