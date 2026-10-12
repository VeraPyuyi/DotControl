package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
)

// TransportError deliberately omits upstream/private content. If a request may
// have reached the server, its effect is unknown and it must not be auto-replayed.
type TransportError struct {
	Code           string
	OutcomeUnknown bool
}

func (e *TransportError) Error() string { return e.Code }

type RPCError struct{ Code int; LocalTakeover bool `json:"-"` }

var activeWriterError=regexp.MustCompile(`^thread [A-Za-z0-9-]+ already has an active writer$`)
func (e *RPCError) UnmarshalJSON(data []byte) error {
 var wire struct {Code int `json:"code"`;Message string `json:"message"`}
 if err:=json.Unmarshal(data,&wire);err!=nil{return err}
 e.Code=wire.Code;e.LocalTakeover=wire.Code==-32600&&activeWriterError.MatchString(wire.Message)
 return nil // raw upstream text is deliberately discarded
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex_rpc_error_%d", e.Code) }

type ServerRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type Notification struct {
	Method string
	Params json.RawMessage
}
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}
type response struct {
	result json.RawMessage
	err    error
}
type Client struct {
	reader         io.ReadCloser
	writer         io.WriteCloser
	stop           func() error
	mu             sync.Mutex
	writeMu        sync.Mutex
	next           uint64
	pending        map[string]chan response
	serverRequests map[string]ServerRequest
	notifications  chan Notification
	done           chan struct{}
	once           sync.Once
	terminal       error
}

// Start launches only the explicitly selected installed official executable.
// No TCP/WebSocket listener, auth file parsing, helper launch, or config override
// is used. Cancellation closes this dedicated process; it never attaches to a
// user's existing app-server daemon.
func Start(ctx context.Context, binary, workingDir string) (*Client, error) {
	if !filepath.IsAbs(binary) || !filepath.IsAbs(workingDir) {
		return nil, failure("invalid_codex_config")
	}
	if ctx.Err() != nil {
		return nil, failure("cancelled")
	}
	cmd := exec.CommandContext(ctx, binary, "app-server", "--stdio")
	cmd.Dir, cmd.Stderr = workingDir, io.Discard
	hideProcess(cmd)
	// Own the stdout reader so cmd.Wait cannot close it before the final RPC
	// response has been drained after a normally exiting server.
	input, childOutput, err := os.Pipe()
	if err != nil {
		return nil, failure("codex_start_failed")
	}
	cmd.Stdout = childOutput
	output, err := cmd.StdinPipe()
	if err != nil {
		input.Close()
		childOutput.Close()
		return nil, failure("codex_start_failed")
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		childOutput.Close()
		output.Close()
		return nil, failure("codex_start_failed")
	}
	childOutput.Close()
	wait := make(chan struct{})
	go func() { cmd.Wait(); close(wait) }()
	return newClient(input, output, func() error { _ = cmd.Process.Kill(); <-wait; return nil }), nil
}
func newClient(reader io.ReadCloser, writer io.WriteCloser, stop func() error) *Client {
	c := &Client{reader: reader, writer: writer, stop: stop, pending: map[string]chan response{}, serverRequests: map[string]ServerRequest{}, notifications: make(chan Notification, 256), done: make(chan struct{})}
	go c.read()
	return c
}
func (c *Client) read() {
	s := bufio.NewScanner(c.reader)
	s.Buffer(make([]byte, 4096), 32*1024*1024)
	for s.Scan() {
		var m rpcMessage
		if json.Unmarshal(s.Bytes(), &m) != nil {
			c.shutdown(&TransportError{Code: "codex_protocol_error", OutcomeUnknown: true})
			return
		}
		if m.Method != "" {
			if len(m.ID) > 0 && string(m.ID) != "null" {
				// Requests stay pending until an explicitly bound human response.
				c.mu.Lock()
				if len(c.serverRequests) >= 64 {
					c.mu.Unlock()
					c.shutdown(&TransportError{Code: "codex_pending_requests_full", OutcomeUnknown: true})
					return
				}
				c.serverRequests[string(m.ID)] = ServerRequest{ID: m.ID, Method: m.Method, Params: m.Params}
				c.mu.Unlock()
			} else {
				if m.Method == "serverRequest/resolved" {
					var resolved struct {
						RequestID json.RawMessage `json:"requestId"`
					}
					if json.Unmarshal(m.Params, &resolved) == nil {
						c.mu.Lock()
						delete(c.serverRequests, string(resolved.RequestID))
						c.mu.Unlock()
					}
				}
				select {
				case c.notifications <- Notification{Method: m.Method, Params: m.Params}:
				default:
					c.shutdown(&TransportError{Code: "codex_event_overflow", OutcomeUnknown: true})
					return
				}
			}
			continue
		}
		if len(m.ID) == 0 || string(m.ID) == "null" {
			c.shutdown(&TransportError{Code: "codex_protocol_error", OutcomeUnknown: true})
			return
		}
		if (len(m.Result) == 0) == (m.Error == nil) {
			c.shutdown(&TransportError{Code: "codex_protocol_error", OutcomeUnknown: true})
			return
		}
		c.mu.Lock()
		ch := c.pending[string(m.ID)]
		if ch != nil {
			delete(c.pending, string(m.ID))
		}
		c.mu.Unlock()
		if ch != nil {
			var err error
			if m.Error != nil {
				err = m.Error
			}
			ch <- response{result: m.Result, err: err}
		}
	}
	c.shutdown(&TransportError{Code: "codex_connection_lost", OutcomeUnknown: true})
}
func (c *Client) shutdown(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.terminal = err
		close(c.done)
		for key, ch := range c.pending {
			ch <- response{err: err}
			delete(c.pending, key)
		}
		c.mu.Unlock()
		c.reader.Close()
		c.writer.Close()
		if c.stop != nil {
			c.stop()
		}
	})
}
func (c *Client) write(ctx context.Context, data []byte) error {
	result := make(chan error, 1)
	go func() {
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		select {
		case <-c.done:
			result <- &TransportError{Code: "codex_connection_lost", OutcomeUnknown: true}
			return
		default:
		}
		n, err := c.writer.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		result <- err
	}()
	select {
	case err := <-result:
		if err != nil {
			e := &TransportError{Code: "codex_connection_lost", OutcomeUnknown: true}
			c.shutdown(e)
			return e
		}
		return nil
	case <-ctx.Done():
		e := &TransportError{Code: "codex_request_timeout", OutcomeUnknown: true}
		c.shutdown(e)
		return e
	case <-c.done:
		c.mu.Lock()
		err := c.terminal
		c.mu.Unlock()
		return err
	}
}

// Call sends exactly one public protocol request. It never retries. It is an
// internal transport primitive, not a remotely exposed arbitrary-RPC endpoint.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	if ctx.Err() != nil {
		return &TransportError{Code: "cancelled", OutcomeUnknown: false}
	}
	p, err := json.Marshal(params)
	if err != nil {
		return failure("invalid_codex_params")
	}
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return &TransportError{Code: "codex_connection_lost", OutcomeUnknown: false}
	default:
	}
	if len(c.pending) >= 256 {
		c.mu.Unlock()
		return failure("codex_requests_full")
	}
	c.next++
	id := fmt.Sprint(c.next)
	ch := make(chan response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	data, _ := json.Marshal(rpcMessage{ID: json.RawMessage(id), Method: method, Params: p})
	if err = c.write(ctx, append(data, '\n')); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && json.Unmarshal(r.result, out) != nil {
			return &TransportError{Code: "codex_invalid_result", OutcomeUnknown: true}
		}
		return nil
	case <-ctx.Done():
		return &TransportError{Code: "codex_request_timeout", OutcomeUnknown: true}
	}
}
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	if ctx.Err() != nil {
		return failure("cancelled")
	}
	p, err := json.Marshal(params)
	if err != nil {
		return failure("invalid_codex_params")
	}
	data, _ := json.Marshal(rpcMessage{Method: method, Params: p})
	return c.write(ctx, append(data, '\n'))
}
func (c *Client) Notifications() <-chan Notification { return c.notifications }
func (c *Client) Done() <-chan struct{}              { return c.done }
func (c *Client) PendingRequests() []ServerRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ServerRequest, 0, len(c.serverRequests))
	for _, r := range c.serverRequests {
		r.ID = append(json.RawMessage(nil), r.ID...)
		r.Params = append(json.RawMessage(nil), r.Params...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i].ID) < string(out[j].ID) })
	return out
}

// Respond is an internal transport primitive, never a model-callable approval
// tool. The coordinator must first bind a human answer to the live thread/turn.
// Consume before writing: a lost response must never be automatically replayed.
func (c *Client) Respond(ctx context.Context, id json.RawMessage, result any) error {
	if ctx.Err() != nil {
		return failure("cancelled")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return failure("invalid_codex_params")
	}
	c.mu.Lock()
	if _, ok := c.serverRequests[string(id)]; !ok {
		c.mu.Unlock()
		return failure("approval_not_pending")
	}
	delete(c.serverRequests, string(id))
	c.mu.Unlock()
	message, _ := json.Marshal(rpcMessage{ID: id, Result: data})
	return c.write(ctx, append(message, '\n'))
}
func (c *Client) Close() error {
	c.shutdown(&TransportError{Code: "codex_connection_closed", OutcomeUnknown: true})
	return nil
}
