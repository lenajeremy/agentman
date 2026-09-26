package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Cursor ACP uses newline-delimited JSON-RPC on a private child process's
// stdio. One client owns one active turn; a later turn loads the same session.
type cursorACPClient struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan cursorACPEnvelope
	handle  func(cursorACPEnvelope)
	closed  chan struct{}
	once    sync.Once
}

type cursorACPEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newCursorACPClient(cwd string, handle func(cursorACPEnvelope)) (*cursorACPClient, error) {
	binary, err := exec.LookPath("agent")
	if err != nil {
		return nil, fmt.Errorf("Cursor Agent CLI is not installed: %w", err)
	}
	cmd := exec.Command(binary, "acp")
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Cursor ACP: %w", err)
	}
	c := &cursorACPClient{cmd: cmd, stdin: stdin, pending: make(map[int]chan cursorACPEnvelope), handle: handle, closed: make(chan struct{})}
	go c.read(stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]string{"name": "agentman", "version": "0.1.0"},
	}, &init); err != nil {
		c.close()
		return nil, fmt.Errorf("initialize Cursor ACP: %w", err)
	}
	if init.ProtocolVersion != 1 || !init.AgentCapabilities.LoadSession {
		c.close()
		return nil, errors.New("Cursor ACP does not support resumable version 1 sessions")
	}
	if err := c.call(ctx, "authenticate", map[string]string{"methodId": "cursor_login"}, nil); err != nil {
		c.close()
		return nil, fmt.Errorf("authenticate Cursor ACP: %w", err)
	}
	return c, nil
}

func (c *cursorACPClient) read(stdout io.Reader) {
	defer close(c.closed)
	defer func() { _ = c.cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var envelope cursorACPEnvelope
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil {
			continue
		}
		if envelope.Method != "" {
			if c.handle != nil {
				c.handle(envelope)
			}
			continue
		}
		var id int
		if json.Unmarshal(envelope.ID, &id) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- envelope
		}
	}
	c.mu.Lock()
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- cursorACPEnvelope{Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Message: "Cursor ACP process exited"}}
	}
	c.mu.Unlock()
}

func (c *cursorACPClient) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(data)
	return err
}

func (c *cursorACPClient) call(ctx context.Context, method string, params any, dest any) error {
	id, ch, err := c.startCall(method, params)
	if err != nil {
		return err
	}
	return c.await(ctx, id, ch, dest)
}

func (c *cursorACPClient) startCall(method string, params any) (int, chan cursorACPEnvelope, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan cursorACPEnvelope, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return 0, nil, err
	}
	return id, ch, nil
}

func (c *cursorACPClient) await(ctx context.Context, id int, ch chan cursorACPEnvelope, dest any) error {
	select {
	case response := <-ch:
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		if dest != nil {
			return json.Unmarshal(response.Result, dest)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.closed:
		return errors.New("Cursor ACP process exited")
	}
}

func (c *cursorACPClient) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *cursorACPClient) respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *cursorACPClient) respondError(id json.RawMessage, message string) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": message}})
}

func (c *cursorACPClient) close() {
	c.once.Do(func() {
		_ = c.stdin.Close()
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}
