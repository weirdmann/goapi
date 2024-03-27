package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
  "github.com/beevik/guid" 
)

func main() {
	fmt.Println("Hello, Cloo!")

	//TcpListen()

}

type TcpPeer struct {
  id  guid.Guid
  recv_chan chan []byte
  send_chan chan []byte
}

func TcpListen() {
	PORT := ":2137"

	listener, err := net.Listen("tcp4", PORT)

	if err != nil {
		fmt.Println(err)
		return
	}

	defer listener.Close()

	for {
		// Accept incoming connections
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		// Handle client connection in a goroutine
		go handleClient(conn)
	}
}
// function to update the timeout of blahblah
func UpdateTimeout[C net.Conn](conn C) {
	conn.SetDeadline(time.Now().Add(5 * time.Second))
}

func handleClient(conn net.Conn) {

	UpdateTimeout(conn)
	defer conn.Close()
  
  

	// Create a buffer to read data into
	buffer := make([]byte, 1024)

	fmt.Printf("[INFO] Client connected: %s\n", conn.RemoteAddr())
	for {
		// Read data from the client
		n, err := conn.Read(buffer)

		if errors.Is(err, os.ErrDeadlineExceeded) {
			UpdateTimeout(conn)
			continue
		}

		if errors.Is(err, io.EOF) {
			fmt.Printf("[INFO] Client disconnected: %s\n", conn.RemoteAddr())
			return
		}

		if err != nil {
			fmt.Println("[ERRR] Error:", err)
			return
		}

		// Process and use the data (here, we'll just print it)
		fmt.Printf("[VERB] Received: \n%s\n", buffer[:n])
		UpdateTimeout(conn)
	}
}
