package lsp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// frame is one message between am and an editor window, one per WebSocket
// text message:
//
//	→ {"t":"resolve","id":1,"file":"/a/b.go","folder":"/a","language":"go"}
//	← {"t":"resolved","id":1,"status":{…}}     status is null with no server
//	← {"t":"status","status":{…},"capabilities":{…}}   capabilities once ready
//	⇄ {"t":"rpc","session":"gopls:/a","msg":{…}}       LSP, both ways
//	→ {"t":"files","changes":[{"path":"/a/c.go","type":2}]}
//	→ {"t":"release","session":"gopls:/a"}            no file of it open now
//	→ {"t":"restart","session":"gopls:/a"}
type frame struct {
	T            string          `json:"t"`
	ID           int             `json:"id,omitempty"`
	Session      string          `json:"session,omitempty"`
	File         string          `json:"file,omitempty"`
	Folder       string          `json:"folder,omitempty"`
	Language     string          `json:"language,omitempty"`
	Msg          json.RawMessage `json:"msg,omitempty"`
	Changes      []FileChange    `json:"changes,omitempty"`
	Status       *Status         `json:"status,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
}

// A Client is one editor window's connection.
type Client struct {
	host *Host
	conn *websocket.Conn
	out  chan []byte

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool
	stop     context.CancelFunc
}

const (
	// clientOutbox is how many frames may wait for a slow window before it
	// is dropped; it reconnects and opens its files again.
	clientOutbox = 4096
	clientWrite  = 10 * time.Second
)

func (h *Host) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/lsp" {
		http.NotFound(w, r)
		return
	}
	if !h.allowed(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// The origin is checked above, exactly; the library's check compares it
	// with the host, which a page from agentman-editor:// never matches.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxMessage)
	ctx, cancel := context.WithCancel(h.ctx)
	c := &Client{host: h, conn: conn, out: make(chan []byte, clientOutbox), sessions: map[string]*Session{}, stop: cancel}
	go c.write(ctx)
	c.read(ctx)
}

func (c *Client) read(ctx context.Context) {
	defer c.close()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		c.handle(f)
	}
}

func (c *Client) handle(f frame) {
	switch f.T {
	case "resolve":
		s := c.host.resolve(c, f.File, f.Folder, f.Language)
		reply := frame{T: "resolved", ID: f.ID}
		if s != nil {
			c.mu.Lock()
			c.sessions[s.Key] = s
			c.mu.Unlock()
			status := s.Status()
			reply.Status = &status
		}
		c.send(reply)
	case "rpc":
		s := c.session(f.Session)
		if s == nil {
			return
		}
		var msg message
		if json.Unmarshal(f.Msg, &msg) != nil {
			return
		}
		if msg.isResponse() {
			s.fromClientResponse(msg)
		} else {
			s.fromClient(c, msg)
		}
	case "files":
		c.host.filesChanged(f.Changes)
	case "release":
		c.mu.Lock()
		s := c.sessions[f.Session]
		delete(c.sessions, f.Session)
		c.mu.Unlock()
		if s != nil {
			s.detach(c)
		}
	case "restart":
		if s := c.session(f.Session); s != nil {
			s.restart()
		}
	}
}

func (c *Client) session(key string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[key]
}

func (c *Client) write(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-c.out:
			writeCtx, cancel := context.WithTimeout(ctx, clientWrite)
			err := c.conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

// send queues a frame for the window; one too slow to keep up is dropped.
func (c *Client) send(f frame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	select {
	case c.out <- data:
		c.mu.Unlock()
	default:
		c.mu.Unlock()
		c.close()
	}
}

func (c *Client) sendStatus(status Status, caps json.RawMessage) {
	c.send(frame{T: "status", Status: &status, Capabilities: caps})
}

func (c *Client) sendRPC(session string, msg message) {
	msg.JSONRPC = "2.0"
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.send(frame{T: "rpc", Session: session, Msg: raw})
}

// close ends the connection and lets go of every server it used.
func (c *Client) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	sessions := c.sessions
	c.sessions = map[string]*Session{}
	c.mu.Unlock()
	c.stop()
	_ = c.conn.Close(websocket.StatusNormalClosure, "")
	for _, s := range sessions {
		s.detach(c)
	}
}
