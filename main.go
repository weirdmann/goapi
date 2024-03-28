package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/beevik/guid"
)

var main_WG sync.WaitGroup

func main() {
	fmt.Println("Hello, Cloo!")
	tcp_context, cancel := context.WithCancel(context.Background())

	tcp := NewTcpEndpoint("0.0.0.0", 2137, &tcp_context)

	main_WG.Add(1)
	go tcp.Listen()

	go func() {
		time.Sleep(10 * time.Second)
		cancel()
	}()
	main_WG.Wait()

}

/*

// A Context carries a deadline, cancellation signal, and request-scoped values
// across API boundaries. Its methods are safe for simultaneous use by multiple
// goroutines.
type Context interface {
    // Done returns a channel that is closed when this Context is canceled
    // or times out.
    Done() <-chan struct{}

    // Err indicates why this context was canceled, after the Done channel
    // is closed.
    Err() error

    // Deadline returns the time when this Context will be canceled, if any.
    Deadline() (deadline time.Time, ok bool)

    // Value returns the value associated with key or nil if none.
    Value(key interface{}) interface{}
}

type Server struct {
    listener net.Listener
    quit     chan interface{}
    wg       sync.WaitGroup
}

func NewServer(addr string) *Server {
    s := &Server{
        quit: make(chan interface{}),
    }
    l, err := net.Listen("tcp", addr)
    if err != nil {
        log.Fatal(err)
    }
    s.listener = l
    s.wg.Add(1)
    go s.serve()
    return s
}

func (s *Server) Stop() {
    close(s.quit)
    s.listener.Close()
    s.wg.Wait()
}

func (s *Server) serve() {
    defer s.wg.Done()

    for {
        conn, err := s.listener.Accept()
        if err != nil {
            select {
            case <-s.quit:
                return
            default:
                log.Println("accept error", err)
            }
        } else {
            s.wg.Add(1)
            go func() {
                s.handleConection(conn)
                s.wg.Done()
            }()
        }
    }
}

func (s *Server) handleConection(conn net.Conn) {
    defer conn.Close()
    buf := make([]byte, 2048)
    for {
        n, err := conn.Read(buf)
        if err != nil && err != io.EOF {
            log.Println("read error", err)
            return
        }
        if n == 0 {
            return
        }
        log.Printf("received from %v: %s", conn.RemoteAddr(), string(buf[:n]))
    }
}

*/
// ---- Tcp Endpoint ---- //

type TcpEndpoint struct {
	Guid          guid.Guid
	ctx           *context.Context
	config_IPAddr net.IP
	config_Port   uint16
	conn          net.Conn
	listener      net.Listener
}

func NewTcpEndpoint(ipAddr string, port uint16, ctx *context.Context) *TcpEndpoint {
	return &TcpEndpoint{
		ctx:           ctx,
		Guid:          *guid.New(),
		config_IPAddr: net.ParseIP(ipAddr),
		config_Port:   port,
	}
}

func (this *TcpEndpoint) ListenerStop() {
	select {
	case <-(*this.ctx).Done():
		this.listener.Close()
	}

}

func (this *TcpEndpoint) Listen() {

	defer main_WG.Done()
	var err error

	this.listener, err = net.Listen("tcp4", fmt.Sprintf("%s:%d", this.config_IPAddr.String(), this.config_Port))
	if err != nil {
		fmt.Println(err)
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
			fmt.Println("Error:", err)
			return
		}

		// Handle client connection in a goroutine
		go handleClient(conn)
	}

}

// --------------------- //

// ---- Tcp Peer ---- //

type TcpPeer struct {
	Id        guid.Guid
	Conn      net.Conn
	recv_chan chan []byte
	send_chan chan []byte
}

func NewTcpPeer(conn net.Conn) (*TcpPeer, error) {
	n := &TcpPeer{
		Id:        *guid.New(),
		Conn:      conn,
		recv_chan: make(chan []byte, 64),
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
			fmt.Printf("[INFO] Client disconnected: %s\n", this.Conn.RemoteAddr())
			return
		}

		if err != nil {
			fmt.Println("[ERRR] Error:", err)
			return
		}

		// Process and use the data (here, we'll just print it)
		fmt.Printf("[VERB] Received: \n%s\n", buffer[:n])
		UpdateTimeout(this.Conn, 5)
	}
}

// ----------------- //

// function to update the timeout of blahblah
func UpdateTimeout[C net.Conn](conn C, seconds int) {
	conn.SetDeadline(time.Now().Add(time.Second * time.Duration(seconds)))
}

func handleClient(conn net.Conn) {

	tp, err := NewTcpPeer(conn)
	if err != nil {
		fmt.Println("Error creating a TCP Peer")
		panic(0)
	}

	fmt.Println(tp.Id.String())
	fmt.Println(tp.GetConn().RemoteAddr())
	UpdateTimeout(conn, 5)
	defer conn.Close()

	// Create a buffer to read data into
	buffer := make([]byte, 1024)

	fmt.Printf("[INFO] Client connected: %s\n", conn.RemoteAddr())
	for {
		// Read data from the client
		n, err := conn.Read(buffer)

		if errors.Is(err, os.ErrDeadlineExceeded) {
			UpdateTimeout(conn, 5)
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
		UpdateTimeout(conn, 5)
	}
}
