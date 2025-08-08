package main

import (
	"fmt"
	"log"
	"net"
)

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
			buf := make([]byte, 64)
			for {
				n, err := c.Read(buf)
				if err != nil {
					log.Println("Read error:", err)
					break
				}
				fmt.Print("client says:", string(buf[:n]))
			}
		}(conn)
	}
}


