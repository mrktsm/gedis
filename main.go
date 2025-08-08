package main

import (
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
		log.Println("Connection accepted")
	}
}


