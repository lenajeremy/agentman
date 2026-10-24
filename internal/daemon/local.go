package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// LocalServer serves the daemon's protocol on a Unix socket, to apps running
// on this Mac. Agentman for Mac talks to its own daemon this way: no round
// trip through the relay, and it keeps working with no network at all.
//
// The socket lives in the config directory, which is 0700, and is itself
// 0600, so only this user can open it. The file system is the
// authentication, as it already is for the hook token beside it.
//
// The protocol is the phone's: the same envelopes, requests and events, one
// JSON envelope per line in each direction. Requests are routed by the same
// requestDispatch as the relay connection's, and every event the daemon sends
// reaches every connected app, which is what makes this a Transport.
type LocalServer struct {
	path string

	mu       sync.Mutex
	listener net.Listener
	conns    map[*localConn]struct{}
	next     uint64
}

// localConn is one connected app.
type localConn struct {
	id  string
	out chan []byte
	// stop ends the connection: its reader, its writer and its requests.
	stop context.CancelFunc
	// closed is set, under LocalServer.mu, once nothing more may be queued
	// for it: it is being torn down, or it fell too far behind.
	closed bool
}

const (
	// localOutbox is how many frames may wait for a slow app before it is
	// dropped. An app that stops reading must not hold up every other one, or
	// the daemon; it reconnects and asks for the current state again.
	localOutbox       = 256
	localWriteTimeout = 10 * time.Second
	// maxSocketPath is what a Unix socket path may be on macOS, whose
	// sockaddr_un has room for 104 bytes including the terminating NUL.
	maxSocketPath = 103
)

// NewLocalServer returns a server for the socket at path. Nothing is created
// until Serve.
func NewLocalServer(path string) *LocalServer {
	return &LocalServer{path: path, conns: map[*localConn]struct{}{}}
}

// Path is where the socket is.
func (s *LocalServer) Path() string { return s.path }

// ErrLocalSocketInUse means another daemon is already serving the socket.
var ErrLocalSocketInUse = errors.New("daemon: another am serve is already listening on the local socket")

// Listen creates the socket. A socket file left by a daemon that is no longer
// running is replaced; one another daemon is still answering on is not.
func (s *LocalServer) Listen() error {
	if len(s.path) > maxSocketPath {
		return fmt.Errorf("daemon: local socket path is too long for macOS (%d bytes): %s", len(s.path), s.path)
	}
	if conn, err := net.DialTimeout("unix", s.path, time.Second); err == nil {
		_ = conn.Close()
		return ErrLocalSocketInUse
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: remove stale local socket: %w", err)
	}
	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("daemon: listen on local socket: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("daemon: restrict local socket: %w", err)
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()
	return nil
}

// Serve accepts apps until ctx ends, handing each request to handler and
// reporting each app that leaves to disconnected, so its subscriptions end
// with it. Listen must have succeeded first.
func (s *LocalServer) Serve(
	ctx context.Context,
	handler func(context.Context, string, protocol.Request) protocol.Event,
	disconnected func(string),
) error {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener == nil {
		return errors.New("daemon: local socket is not listening")
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	defer os.Remove(s.path)

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("daemon: accept on local socket: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, conn, handler, disconnected)
		}()
	}
}

func (s *LocalServer) serveConn(
	ctx context.Context,
	conn net.Conn,
	handler func(context.Context, string, protocol.Request) protocol.Event,
	disconnected func(string),
) {
	connCtx, stop := context.WithCancel(ctx)
	s.mu.Lock()
	s.next++
	app := &localConn{
		id:   fmt.Sprintf("local:%d", s.next),
		out:  make(chan []byte, localOutbox),
		stop: stop,
	}
	s.conns[app] = struct{}{}
	s.mu.Unlock()

	// One writer per connection, so frames never interleave mid-line.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writer := bufio.NewWriter(conn)
		for {
			select {
			case <-connCtx.Done():
				return
			case frame := <-app.out:
				_ = conn.SetWriteDeadline(time.Now().Add(localWriteTimeout))
				if _, err := writer.Write(frame); err != nil {
					stop()
					return
				}
				// Flush when nothing else is waiting, so a burst goes out as one
				// write and a lone event goes out at once.
				if len(app.out) == 0 {
					if err := writer.Flush(); err != nil {
						stop()
						return
					}
				}
			}
		}
	}()

	dispatch := newRequestDispatch(connCtx, handler, func(replyTo string, event protocol.Event) {
		s.enqueue(app, replyTo, event)
	})
	defer func() {
		stop()
		s.mu.Lock()
		app.closed = true
		delete(s.conns, app)
		s.mu.Unlock()
		_ = conn.Close()
		dispatch.close()
		<-writerDone
		if disconnected != nil {
			disconnected(app.id)
		}
	}()

	go func() {
		<-connCtx.Done()
		_ = conn.Close() // unblocks the read below
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64<<10), maxFrame)
	for scanner.Scan() {
		var envelope protocol.Envelope
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.V != protocol.Version {
			s.enqueue(app, envelope.ID, protocol.Event{
				Type:  protocol.EvtError,
				Error: fmt.Sprintf("this daemon speaks protocol version %d", protocol.Version),
			})
			continue
		}
		req, err := DecodeRequest(envelope.Payload)
		if err != nil {
			continue
		}
		dispatch.dispatch(app.id, envelope.ID, req)
	}
	// The loop ends when the app hangs up or the daemon stops, or on a line
	// longer than maxFrame, which no request needs: all three end the
	// connection, and the app reconnects for a fresh start.
	_ = scanner.Err()
}

// Send delivers an event to every connected app.
func (s *LocalServer) Send(event protocol.Event) error {
	s.mu.Lock()
	apps := make([]*localConn, 0, len(s.conns))
	for app := range s.conns {
		apps = append(apps, app)
	}
	s.mu.Unlock()
	for _, app := range apps {
		s.enqueue(app, "", event)
	}
	return nil
}

// enqueue frames an event for one app. An app whose outbox is full has
// stopped reading; it is cut off rather than allowed to stall the daemon.
func (s *LocalServer) enqueue(app *localConn, replyTo string, event protocol.Event) {
	envelope, err := protocol.NewEnvelope(newID(), protocol.PeerApp, event)
	if err != nil {
		return
	}
	envelope.ReplyTo = replyTo
	frame, err := json.Marshal(envelope)
	if err != nil || len(frame) > maxFrame {
		return
	}
	frame = append(frame, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if app.closed {
		return
	}
	select {
	case app.out <- frame:
	default:
		app.closed = true
		delete(s.conns, app)
		app.stop()
	}
}
