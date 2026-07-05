package hds

import (
	"errors"
	"net"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

// acceptTimeout - how long the accessory waits for the controller
// to open the TCP connection after SetupDataStreamTransport.
const acceptTimeout = 10 * time.Second

// Server - a single prepared HomeKit Data Stream session.
//
// The accessory allocates one TCP listener per SetupDataStreamTransport
// request and returns the port and salt to the controller. The controller
// then opens a TCP connection and sends a "hello" request over the
// encrypted channel.
type Server struct {
	ln            net.Listener
	sharedKey     []byte
	salt          string
	accessorySalt string
}

// NewServer creates a TCP listener for a new HDS session.
//
//	sharedKey - shared secret of the HAP connection (from Pair-Verify)
//	controllerSalt - 32 bytes of salt from the SetupDataStreamTransport request
func NewServer(sharedKey []byte, controllerSalt string) (*Server, error) {
	// The TCP port range for HDS must be >= 32768 (default ephemeral range).
	ln, err := net.ListenTCP("tcp", nil)
	if err != nil {
		return nil, err
	}

	accessorySalt := core.RandString(32, 0)

	return &Server{
		ln:            ln,
		sharedKey:     sharedKey,
		salt:          controllerSalt + accessorySalt,
		accessorySalt: accessorySalt,
	}, nil
}

// Port of the TCP listener for the SetupDataStreamTransport response.
func (s *Server) Port() uint16 {
	return uint16(s.ln.Addr().(*net.TCPAddr).Port)
}

// AccessorySalt for the SetupDataStreamTransport response.
func (s *Server) AccessorySalt() string {
	return s.accessorySalt
}

// Accept waits for the controller connection and performs the "hello"
// handshake. Returns the encrypted connection, ready for messaging.
func (s *Server) Accept() (*Conn, error) {
	defer s.ln.Close()

	_ = s.ln.(*net.TCPListener).SetDeadline(time.Now().Add(acceptTimeout))

	conn, err := s.ln.Accept()
	if err != nil {
		return nil, err
	}

	if conn, ok := conn.(*net.TCPConn); ok {
		_ = conn.SetNoDelay(true)
		_ = conn.SetKeepAlive(true)
	}

	c, err := NewConn(conn, s.sharedKey, s.salt, false)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	// first message from the controller should be the "hello" request
	_ = conn.SetReadDeadline(time.Now().Add(acceptTimeout))

	msg, err := c.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	if msg.Protocol != ProtoControl || msg.Type != TypeRequest || msg.Topic != TopicHello {
		_ = conn.Close()
		return nil, errors.New("hds: wrong first message: " + msg.String())
	}

	if err = c.WriteResponse(ProtoControl, TopicHello, msg.ID, StatusSuccess, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}

	_ = conn.SetReadDeadline(time.Time{})

	return c, nil
}

// Close stops the listener (safe to call multiple times).
func (s *Server) Close() error {
	return s.ln.Close()
}
