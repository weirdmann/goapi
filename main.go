package main

/*

TODO: There is some error with the Dialer locking the graceful shutdown
when it has some leftover values in the send channel but
it didn't establish the connection...
Look into it.

*/
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

	"strings"

	gs "github.com/weirdmann/go-graceful-shutdown"

	"github.com/beevik/guid"
)

var wg sync.WaitGroup

func main() {
	logger := log.Default()
	logger.Println("Starting...")
	tcp_context, cancel := context.WithCancel(context.Background())
	go gs.GracefulShutdown(logger, cancel)

	tcp := NewTcpEndpoint("0.0.0.0", 2137, &tcp_context, logger)
	recv_from_listener := make(chan []byte)
	recv_from_dialer := make(chan []byte)
	send_to_listener, _ := tcp.Listen(&recv_from_listener)
	tcp_d := NewTcpEndpoint("127.0.0.1", 2139, &tcp_context, logger)
  send_to_dialer, _ := tcp_d.Dial(&recv_from_dialer)

	handle := func(recv_chan *chan []byte, send_chan *chan []byte) {
		defer wg.Done()
		if recv_chan == nil || send_chan == nil {
			return
		}
		stop := false
		for !stop {
			select {
			case r, ok := <-*recv_chan:
				if !ok {
					log.Println("Endpoint closed the recv_chan, exiting...")
					stop = true
					break
				}
				//log.Printf("[INFO] Received: %s", r)
				str := string(r[:])
				str = strings.TrimSpace(str)
				*send_chan <- r
			case <-tcp_context.Done():
				return
			}
		}
		close(*send_chan)
	}
	wg.Add(2)
	go handle(&recv_from_listener, send_to_listener)
	go handle(&recv_from_dialer, send_to_dialer)
	wg.Wait()
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
	peers         map[string]*TcpPeer
	recv_chan     *chan []byte
	send_chan     chan []byte
}

func NewTcpEndpoint(ipAddr string, port uint16, ctx *context.Context, logger *log.Logger) *TcpEndpoint {
	return &TcpEndpoint{
		ctx:           ctx,
		Guid:          *guid.New(),
		config_IPAddr: net.ParseIP(ipAddr),
		config_Port:   port,
		logger:        logger,
		peers:         make(map[string]*TcpPeer),
	}
}

// func (this *TcpEndpoint) ListenerSoftReset() {
// 	this.listener.Close()
// 	for _, tcpPeer := range this.peers {
// 	tcpPeer.Close()
// 	}
// this.Listen(this.recv_chan, this.send_chan)
// }

func (this *TcpEndpoint) ListenerStop() {
	select {
	case <-(*this.ctx).Done():
		this.logger.Printf("Stopping TCP Listener %s", this.listener.Addr())
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
		this.listener.Close()
		close(*this.recv_chan)
	}
}

func (this *TcpEndpoint) DialerStop() {
	select {

	case <-(*this.ctx).Done():
		this.logger.Printf("Stopping TCP Dialer: %s", this.dialer.LocalAddr)
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
		close(*this.recv_chan)
	}
}

// do it the "GO" way:
// Listen should not be started as a goroutine
// but start its own goroutine and return a channel that
// will receive the packets
func (this *TcpEndpoint) Listen(chan_for_received_data *chan []byte) (*chan []byte, error) {
	this.recv_chan = chan_for_received_data
	this.send_chan = make(chan []byte, 64)

	var err error

	this.listener, err = net.Listen("tcp4", fmt.Sprintf("%s:%d", this.config_IPAddr.String(), this.config_Port))
	if err != nil {
		this.logger.Println(err)
		close(*this.recv_chan)
		return &this.send_chan, err
	}

	go this.ListenerStop()
	go func() {
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
			this.peers[tcpPeer.Id.String()] = tcpPeer
			// Handle client connection in a goroutine
			this.logger.Printf("New TCP Peer connected: %s", tcpPeer.Conn.RemoteAddr())
			tp_recv_chan := tcpPeer.Receive(func() { delete(this.peers, tcpPeer.Id.String()) })
			go func() {
				for received := range *tp_recv_chan {
					*this.recv_chan <- received
				}
			}()
		}
	}()
	// Sender coroutine - spread the sent data between all peers
	go func() {
		for to_send := range this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
    this.ListenerStop()
	}()
	return &this.send_chan, nil
}

func (this *TcpEndpoint) Dial(chan_for_received_data *chan []byte) (*chan []byte, error) {
	this.recv_chan = chan_for_received_data
	this.send_chan = make(chan []byte, 64)
	go this.DialerStop()

	addr := fmt.Sprintf("%s:%d", this.config_IPAddr, this.config_Port)
	var conn net.Conn
	this.dialer.Deadline.Add(5 * time.Second)
	conn, err := this.dialer.DialContext(*this.ctx, "tcp4", addr)

	if err != nil {
		this.logger.Printf("TCP Client connection attempt failed for %s, %s", addr, err)
		close(*this.recv_chan)
		return &this.send_chan, err
	}

	this.logger.Printf("TCP Client connected to %s", conn.RemoteAddr())

	tcpPeer, _ := NewTcpPeer(conn, this.logger)
	this.peers[tcpPeer.Id.String()] = tcpPeer
	tp_recv_chan := tcpPeer.Receive(func() { delete(this.peers, tcpPeer.Id.String()) })
	go func() {
		for received := range *tp_recv_chan {
			*this.recv_chan <- received
		}
	}()
	// Sender coroutine - spread the sent data between all peers
	go func() {
		for to_send := range this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
	}()
	return &this.send_chan, nil
}

// --------------------- //

// ---- Tcp Peer ---- //
// TcpPeer
type TcpPeer struct {
	Id        guid.Guid
	Conn      net.Conn
	recv_chan chan []byte // TcpPeer receives data and sends it to the recv_chan
	send_chan chan []byte // TcpPeer reads the send_chan and sends the data to its partner
	logger    *log.Logger
}

func NewTcpPeer(conn net.Conn, logger *log.Logger) (*TcpPeer, error) {
	n := &TcpPeer{
		Id:        *guid.New(),
		Conn:      conn,
		recv_chan: make(chan []byte, 64),
		send_chan: make(chan []byte, 64),
		logger:    logger,
	}

	return n, nil
}

func (this *TcpPeer) Close() {
	this.Conn.Close()
}

func (this *TcpPeer) GetConn() net.Conn {
	return this.Conn
}

func (this *TcpPeer) Send() {
	for to_send := range this.send_chan {
		this.Conn.Write(to_send)
	}
}

func (this *TcpPeer) Receive(on_disconnect func()) *chan []byte {
	buffer := make([]byte, 1024)
	go func() {
		for {
			// Read data from the client
			n, err := this.Conn.Read(buffer)

			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			if errors.Is(err, io.EOF) {
				this.logger.Printf("[INFO] Client disconnected: %s\n", this.Conn.RemoteAddr())
				this.Close()
				on_disconnect()
				return
			}
			if errors.Is(err, net.ErrClosed) {
				on_disconnect()
				return
			}
			if err != nil {
				this.logger.Println("[ERRR] Error:", err)
				return
			}
			this.recv_chan <- buffer[:n]
		}
	}()
	go this.Send()
	return &this.recv_chan
}

// ----------------- //

// function to update the timeout of blahblah
func UpdateTimeout[C net.Conn](conn C, seconds int) {
	conn.SetDeadline(time.Now().Add(time.Second * time.Duration(seconds)))
}
