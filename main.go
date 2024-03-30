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
	recv, send := make(chan []byte), make(chan []byte)
	rd, sd := make(chan []byte), make(chan []byte)
	tcp.Listen(&recv, &send)
	tcp_d := NewTcpEndpoint("127.0.0.1", 2139, &tcp_context, logger)
	tcp_d.Dial(&rd, &sd)

	handle := func(recv_chan *chan []byte, send_chan *chan []byte) {
		defer wg.Done()
		if recv_chan == nil || send_chan == nil {
			return
		}
		for {
			select {
			case r := <-*recv_chan:
				log.Printf("[INFO] Received: %s", r)
				str := string(r[:])
				str = strings.TrimSpace(str)
				if str == "RESET" {
					tcp.ListenerSoftReset()
				}
				*send_chan <- r
			case <-tcp_context.Done():
				return
			}
		}
	}
	wg.Add(2)
	go handle(&recv, &send)
	go handle(&rd, &sd)
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
	send_chan     *chan []byte
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

func (this *TcpEndpoint) ListenerSoftReset() {
	this.listener.Close()
	for _, tcpPeer := range this.peers {
		tcpPeer.Close()
	}
	this.Listen(this.recv_chan, this.send_chan)
}

func (this *TcpEndpoint) ListenerStop() {
	select {
	case <-(*this.ctx).Done():
		this.logger.Printf("Stopping TCP Listener %s", this.listener.Addr())
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
		this.listener.Close()
	}
}

func (this *TcpEndpoint) DialerStop() {
	select {

	case <-(*this.ctx).Done():
		this.logger.Printf("Stopping TCP Dialer: %s", this.dialer.LocalAddr)
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
	}
}

// do it the "GO" way:
// Listen should not be started as a goroutine
// but start its own goroutine and return a channel that
// will receive the packets
func (this *TcpEndpoint) Listen(chan_for_received_data *chan []byte, chan_for_data_to_send *chan []byte) {
	this.recv_chan = chan_for_received_data
	this.send_chan = chan_for_data_to_send

	var err error

	this.listener, err = net.Listen("tcp4", fmt.Sprintf("%s:%d", this.config_IPAddr.String(), this.config_Port))
	if err != nil {
		this.logger.Println(err)
		return
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
		for to_send := range *this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
	}()
}

func (this *TcpEndpoint) Dial(chan_for_received_data *chan []byte, chan_for_data_to_send *chan []byte) {
	this.recv_chan = chan_for_received_data
	this.send_chan = chan_for_data_to_send
	go this.DialerStop()

	addr := fmt.Sprintf("%s:%d", this.config_IPAddr, this.config_Port)
	var conn net.Conn
	this.dialer.Deadline.Add(5 * time.Second)
	conn, err := this.dialer.DialContext(*this.ctx, "tcp4", addr)

	if err != nil {
		this.logger.Printf("TCP Client connection attempt failed for %s, %s", addr, err)
		return
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
		for to_send := range *this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
	}()
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
	close(this.recv_chan)
	close(this.send_chan)
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
				UpdateTimeout(this.Conn, 5)
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
			UpdateTimeout(this.Conn, 5)
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
