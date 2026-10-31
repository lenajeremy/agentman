package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// An Agent Client Protocol (ACP) agent speaks newline-delimited JSON-RPC on
// a private child process's stdio: Cursor's `agent acp`, `kiro-cli acp`,
// `opencode acp`. acpClient is one such child, started by dialACP.
type acpClient struct {
	// name is the agent's, for errors: "Cursor".
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan acpEnvelope
	handle  func(acpEnvelope)
	closed  chan struct{}
	once    sync.Once
}

type acpEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *acpClient) read(stdout io.Reader) {
	defer close(c.closed)
	defer func() {
		if c.cmd != nil {
			_ = c.cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var envelope acpEnvelope
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
		ch <- acpEnvelope{Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Message: c.name + " ACP process exited"}}
	}
	c.mu.Unlock()
}

func (c *acpClient) write(value any) error {
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

func (c *acpClient) call(ctx context.Context, method string, params any, dest any) error {
	id, ch, err := c.startCall(method, params)
	if err != nil {
		return err
	}
	return c.await(ctx, id, ch, dest)
}

func (c *acpClient) startCall(method string, params any) (int, chan acpEnvelope, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan acpEnvelope, 1)
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

func (c *acpClient) await(ctx context.Context, id int, ch chan acpEnvelope, dest any) error {
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
		return errors.New(c.name + " ACP process exited")
	}
}

func (c *acpClient) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *acpClient) respond(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *acpClient) respondError(id json.RawMessage, message string) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": -32601, "message": message}})
}

func (c *acpClient) close() {
	c.once.Do(func() {
		_ = c.stdin.Close()
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	})
}

// acpSpec says how to start one agent's ACP server.
type acpSpec struct {
	// name is how errors name the agent: "Cursor", "Kiro".
	name string
	// binary and args start it: "agent" "acp".
	binary string
	args   []string
	// missing is the error when binary is not on PATH.
	missing string
	// meta is the clientCapabilities._meta to initialize with, if any.
	meta map[string]any
	// auth is the method to authenticate with after initialize, if any.
	auth string
	// env adds to the child's environment.
	env []string
}

// acpInit is what an agent's initialize answers that Agentman reads.
type acpInit struct {
	ProtocolVersion   int `json:"protocolVersion"`
	AgentCapabilities struct {
		LoadSession bool `json:"loadSession"`
	} `json:"agentCapabilities"`
}

// dialACP starts an agent's ACP server in cwd and initializes it. handle
// receives every notification and request the agent sends.
func dialACP(spec acpSpec, cwd string, handle func(acpEnvelope)) (*acpClient, error) {
	binary, err := exec.LookPath(spec.binary)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec.missing, err)
	}
	cmd := exec.Command(binary, spec.args...)
	cmd.Dir = cwd
	cmd.Stderr = io.Discard
	if len(spec.env) > 0 {
		cmd.Env = append(os.Environ(), spec.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s ACP: %w", spec.name, err)
	}
	c := &acpClient{name: spec.name, cmd: cmd, stdin: stdin, pending: make(map[int]chan acpEnvelope), handle: handle, closed: make(chan struct{})}
	go c.read(stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	capabilities := map[string]any{
		"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false,
	}
	if spec.meta != nil {
		capabilities["_meta"] = spec.meta
	}
	var init acpInit
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": capabilities,
		"clientInfo":         map[string]string{"name": "agentman", "version": "0.1.0"},
	}, &init); err != nil {
		c.close()
		return nil, fmt.Errorf("initialize %s ACP: %w", spec.name, err)
	}
	if init.ProtocolVersion != 1 || !init.AgentCapabilities.LoadSession {
		c.close()
		return nil, fmt.Errorf("%s ACP does not support resumable version 1 sessions", spec.name)
	}
	if spec.auth != "" {
		if err := c.call(ctx, "authenticate", map[string]string{"methodId": spec.auth}, nil); err != nil {
			c.close()
			return nil, fmt.Errorf("authenticate %s ACP: %w", spec.name, err)
		}
	}
	return c, nil
}
