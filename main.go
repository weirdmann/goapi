package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/weirdmann/go-graceful-shutdown"

	"github.com/beevik/guid"
)

var main_WG sync.WaitGroup

func main() {
	logger := log.Default()
	logger.Println("Starting...")
	tcp_context, cancel := context.WithCancel(context.Background())

	tcp := NewTcpEndpoint("0.0.0.0", 2137, &tcp_context, logger)

	main_WG.Add(1)
	go tcp.Listen()

	logger.Println("Started.")
	go graceful_shutdown.GracefulShutdown(logger, cancel)
	main_WG.Wait()

}

// ---- Tcp Endpoint ---- //

type TcpEndpoint struct {
	Guid          guid.Guid
	ctx           *context.Context
	config_IPAddr net.IP
	config_Port   uint16
	conn          net.Conn
	listener      net.Listener
	dialer        net.Dialer
	logger        *log.Logger
}

func NewTcpEndpoint(ipAddr string, port uint16, ctx *context.Context, logger *log.Logger) *TcpEndpoint {
	return &TcpEndpoint{
		ctx:           ctx,
		Guid:          *guid.New(),
		config_IPAddr: net.ParseIP(ipAddr),
		config_Port:   port,
		logger:        logger,
	}
}

func (this *TcpEndpoint) ListenerStop() {
	select {
	case <-(*this.ctx).Done():
		this.logger.Printf("Stopping TCP Listener %s", this.listener.Addr())
		this.listener.Close()
	}
}

func (this *TcpEndpoint) DialerStop() {
  select {

    case <- (*this.ctx).Done():
      this.logger.Printf("Stopping TCP Dialer: %s", this.dialer.LocalAddr)
  }
}

func (this *TcpEndpoint) Listen() {

	defer main_WG.Done()
	var err error

	this.listener, err = net.Listen("tcp4", fmt.Sprintf("%s:%d", this.config_IPAddr.String(), this.config_Port))
	if err != nil {
		this.logger.Println(err)
		return
	}

	go this.ListenerStop()
	for {
		// Accept incoming connections
		conn, err := this.listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			this.logger.Println("Error:", err)
			return
		}

		tcpPeer, _ := NewTcpPeer(conn, this.logger)
		// Handle client connection in a goroutine
		this.logger.Printf("New TCP Peer connected: %s", tcpPeer.Conn.RemoteAddr())
		go tcpPeer.Receive()
	}
}

func (this *TcpEndpoint) Dial() {

  this.dialer.Dial("tcp4", fmt.Sprintf("%s:%d", this.config_IPAddr, this.config_Port))


}

// --------------------- //

// ---- Tcp Peer ---- //

type TcpPeer struct {
	Id        guid.Guid
	Conn      net.Conn
	recv_chan chan []byte
	send_chan chan []byte
	logger    *log.Logger
}

func NewTcpPeer(conn net.Conn, logger *log.Logger) (*TcpPeer, error) {
	n := &TcpPeer{
		Id:        *guid.New(),
		Conn:      conn,
		recv_chan: make(chan []byte, 64),
		logger:    logger,
	}

	return n, nil
}

func (this *TcpPeer) GetConn() net.Conn {
	return this.Conn
}

func (this *TcpPeer) Receive() {
	buffer := make([]byte, 1024)
	for {
		// Read data from the client
		n, err := this.Conn.Read(buffer)

		if errors.Is(err, os.ErrDeadlineExceeded) {
			UpdateTimeout(this.Conn, 5)
			continue
		}

		if errors.Is(err, io.EOF) {
			this.logger.Printf("[INFO] Client disconnected: %s\n", this.Conn.RemoteAddr())
			return
		}

		if err != nil {
			this.logger.Println("[ERRR] Error:", err)
			return
		}

		// Process and use the data (here, we'll just print it)
		this.logger.Printf("[VERB] Received: %s", buffer[:n])
		UpdateTimeout(this.Conn, 5)
	}
}

// ----------------- //

// function to update the timeout of blahblah
func UpdateTimeout[C net.Conn](conn C, seconds int) {
	conn.SetDeadline(time.Now().Add(time.Second * time.Duration(seconds)))
}

/*
func handleClient(conn net.Conn) {

	tp, err := NewTcpPeer(conn)
	if err != nil {
		this.logger.Println("Error creating a TCP Peer")
		panic(0)
	}

	this.logger.Println(tp.Id.String())
	this.logger.Println(tp.GetConn().RemoteAddr())
	UpdateTimeout(conn, 5)
	defer conn.Close()

	// Create a buffer to read data into
	buffer := make([]byte, 1024)

	logger.Printf("[INFO] Client connected: %s\n", conn.RemoteAddr())
	for {
		// Read data from the client
		n, err := conn.Read(buffer)

		if errors.Is(err, os.ErrDeadlineExceeded) {
			UpdateTimeout(conn, 5)
			continue
		}

		if errors.Is(err, io.EOF) {
			logger.Printf("[INFO] Client disconnected: %s\n", conn.RemoteAddr())
			return
		}

		if err != nil {
			logger.Println("[ERRR] Error:", err)
			return
		}

		// Process and use the data (here, we'll just print it)
		logger.Printf("[VERB] Received: \n%s\n", buffer[:n])
		UpdateTimeout(conn, 5)
	}
}
*/
