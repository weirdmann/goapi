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
	main_context, cancel := context.WithCancel(context.Background())
	go gs.GracefulShutdown(logger, cancel)

	tcp := NewTcpEndpoint("0.0.0.0", 2137, &main_context, logger)
	recv_from_listener := make(chan []byte)
	recv_from_dialer := make(chan []byte)
	send_to_listener, _ := tcp.Listen(main_context, "0.0.0.0:2137", &recv_from_listener)
	tcp_d := NewTcpEndpoint("127.0.0.1", 2139, &main_context, logger)
	send_to_dialer, _ := tcp_d.Dial(main_context, "0.0.0.0:2138", &recv_from_dialer)

	handle := func(ctx context.Context, recv_chan *chan []byte, send_chan *chan []byte) {
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
			case <-ctx.Done():
				//time.Sleep(time.Second)
				stop = true
				break
			}
		}
		close(*send_chan)
	}
	wg.Add(2)
	go handle(main_context, &recv_from_listener, send_to_listener)
	go handle(main_context, &recv_from_dialer, send_to_dialer)
	wg.Wait()
}

// ---- Tcp Endpoint ---- //

type TcpEndpoint struct {
	Guid      guid.Guid
	listener  net.Listener
	dialer    net.Dialer
	logger    *log.Logger
	peers     map[string]*TcpPeer
	recv_chan *chan []byte
	send_chan chan []byte
}

func NewTcpEndpoint(ipAddr string, port uint16, ctx *context.Context, logger *log.Logger) *TcpEndpoint {
	return &TcpEndpoint{
		Guid:   *guid.New(),
		logger: logger,
		peers:  make(map[string]*TcpPeer),
	}
}

func (this *TcpEndpoint) log(s string, a ...any) {
  a = append([]any{this.Guid.String()[:4]}, a)
	this.logger.Printf("%s | " + s, a...)
}

func (this *TcpEndpoint) ListenerStop(ctx context.Context) {
	select {
	case <-(ctx).Done():
		this.log("Stopping TCP Listener %s", this.listener.Addr())
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
		this.listener.Close()
	}
}

func (this *TcpEndpoint) DialerStop(ctx context.Context) {
	select {
	case <-(ctx).Done():
		this.logger.Printf("Stopping TCP Dialer: %s", this.dialer.LocalAddr)
		for _, tcpPeer := range this.peers {
			tcpPeer.Close()
		}
		//	close(*this.recv_chan)
	}
}

// do it the "GO" way:
// Listen should not be started as a goroutine
// but start its own goroutine and return a channel that
// will receive the packets
func (this *TcpEndpoint) Listen(ctx context.Context, addr string, chan_for_received_data *chan []byte) (*chan []byte, error) {
	this.recv_chan = chan_for_received_data
	this.send_chan = make(chan []byte, 64)

	var err error

	this.listener, err = net.Listen("tcp4", addr)
	if err != nil {
		this.logger.Println(err)
		close(*this.recv_chan)
		return &this.send_chan, err
	}

	connectionContext, connectionCancel := context.WithCancel(ctx)
	go this.ListenerStop(connectionContext)
	go this.accept(connectionContext)

	// Sender coroutine - spread the sent data between all peers
	go func() {
		for to_send := range this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
		this.logger.Printf("%s x-  | Send channel closed, stopping TCP Listener", this.listener.Addr())
		// cancel the stop handler coroutine so it doesn't try to close a closed recv_chan
		connectionCancel()
	}()
	return &this.send_chan, nil
}

func (this *TcpEndpoint) accept(ctx context.Context) {
	for {
		// Accept incoming connections
		conn, err := this.listener.Accept()
		if errors.Is(err, net.ErrClosed) {
      this.log("%s > Goroutine this.accept: Connection closed", this.listener.Addr().String())
			return
		}
		if err != nil {
      this.logger.Printf("%s > Goroutine this.accept: Error: %q",  this.listener.Addr().String(), err)
			return
		}

		tcpPeer, _ := NewTcpPeer(conn, this.logger)
		this.peers[tcpPeer.Id.String()] = tcpPeer
		// Handle client connection in a goroutine
    this.logger.Printf("%s <- %s | New TCP Peer connected", this.listener.Addr().String(), tcpPeer.Conn.RemoteAddr().String())
	  tcpPeer.Receive(func() { delete(this.peers, tcpPeer.Id.String()) })

		go func(ctx context.Context, tcpPeer *TcpPeer) {
			for {
				select {
				case received, ok := <-tcpPeer.recv_chan:
          if !ok {
            this.logger.Printf("%s <- %s | Recv_chan closed, closing the fan-in func", this.listener.Addr().String(), tcpPeer.Conn.RemoteAddr().String())
            return
          }
					*this.recv_chan <- received
				case <-ctx.Done():
          this.logger.Printf("%s <- %s | Context cancelled, closing the fan-in func", this.listener.Addr().String(), tcpPeer.Conn.RemoteAddr().String())
					return
				}
			}
		}(ctx, tcpPeer)
	}
}

func (this *TcpEndpoint) Dial(ctx context.Context, addr string, chan_for_received_data *chan []byte) (*chan []byte, error) {
	this.recv_chan = chan_for_received_data
	this.send_chan = make(chan []byte, 64)

	connectionContext, connectionCancel := context.WithCancel(ctx)
	go this.DialerStop(connectionContext)

	var conn net.Conn
	this.dialer.Deadline.Add(5 * time.Second)
	conn, err := this.dialer.DialContext(ctx, "tcp4", addr)

	if err != nil {
    this.logger.Printf(" >x %s | TCP Client connection attempt failed for: %q", addr, err)
		// cancel the stop handler coroutine so it doesn't try to close a closed recv_chan
		connectionCancel()
		close(*this.recv_chan)
		return &this.send_chan, err
	}

	this.logger.Printf("%s -> %s | TCP Dialer connected", conn.LocalAddr().String(), conn.RemoteAddr().String())

	tcpPeer, _ := NewTcpPeer(conn, this.logger)
	this.peers[tcpPeer.Id.String()] = tcpPeer
	tcpPeer.Receive(func() { delete(this.peers, tcpPeer.Id.String()) })
  go func(ctx context.Context, tcpPeer *TcpPeer) {
    for {
      select {
      case received, ok := <-tcpPeer.recv_chan:
        if !ok {
          this.logger.Printf(" -> %s | Recv_chan closed, closing the fan-in func", tcpPeer.Conn.RemoteAddr().String())
          return
        }
        *this.recv_chan <- received
      case <-ctx.Done():
        this.logger.Printf(" -> %s | Context cancelled, closing the fan-in func", tcpPeer.Conn.RemoteAddr().String())
        return
      }
    }
  }(connectionContext, tcpPeer)
	// Sender coroutine - spread the sent data between all peers
	go func() {
		for to_send := range this.send_chan {
			for _, peer := range this.peers {
				peer.send_chan <- to_send
			}
		}
		this.logger.Printf(" x> %s | Send channel closed, stopping TCP Dialer", addr)
		// cancel the stop handler coroutine so it doesn't try to close a closed recv_chan
		connectionCancel()
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
  close(this.send_chan)
  close(this.recv_chan)
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
				this.logger.Printf("%s <x %s | Client disconnected", this.Conn.LocalAddr(), this.Conn.RemoteAddr())
				this.Close()
				on_disconnect()
				return
			}
			if errors.Is(err, net.ErrClosed) {
				on_disconnect()
				return
			}
			if err != nil {
				this.logger.Printf("%s ?? %s | [ERRR] Error: %s", this.Conn.LocalAddr(), this.Conn.RemoteAddr(), err)
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
