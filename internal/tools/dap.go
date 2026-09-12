package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/exp/jsonrpc2"
)

// --- DAP wire protocol framer ---

// dapFramer is a jsonrpc2.Framer that reads DAP Content-Length framed messages
// and converts them to jsonrpc2.Message types.
type dapFramer struct{}

func (dapFramer) Reader(rw io.Reader) jsonrpc2.Reader {
	return &dapReader{in: bufio.NewReader(rw)}
}

func (dapFramer) Writer(rw io.Writer) jsonrpc2.Writer {
	return &dapWriter{out: rw}
}

// Bind implements jsonrpc2.Binder to set up the DAP framer.
func (d dapFramer) Bind(ctx context.Context, c *jsonrpc2.Connection) (jsonrpc2.ConnectionOptions, error) {
	return jsonrpc2.ConnectionOptions{Framer: d}, nil
}

type dapReader struct {
	in *bufio.Reader
}

func (r *dapReader) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	// Read Content-Length header
	var length int
	for {
		line, err := r.in.ReadString('\n')
		if err != nil {
			return nil, 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		colon := strings.IndexRune(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if name == "Content-Length" {
			if n, err := strconv.Atoi(value); err == nil {
				length = n
			}
		}
	}
	if length <= 0 {
		return nil, 0, fmt.Errorf("missing or invalid Content-Length")
	}

	// Read the JSON body
	data := make([]byte, length)
	n, err := io.ReadFull(r.in, data)
	if err != nil {
		return nil, int64(n), err
	}

	// Parse as DAP envelope
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, int64(n), err
	}

	msgType, _ := raw["type"]
	msgTypeStr := string(msgType)

	switch msgTypeStr {
	case "response":
		var resp jsonrpc2.Response
		if reqMsgRaw, ok := raw["request_msg"]; ok {
			var reqMsg struct {
				ID int `json:"seq"`
			}
			if err := json.Unmarshal(reqMsgRaw, &reqMsg); err == nil {
				resp.ID = jsonrpc2.Int64ID(int64(reqMsg.ID))
			}
		}
		if resultRaw, ok := raw["result"]; ok {
			resp.Result = resultRaw
		}
		if successRaw, ok := raw["success"]; ok {
			var success bool
			if err := json.Unmarshal(successRaw, &success); err == nil && !success {
				if msgRaw, ok := raw["message"]; ok {
					var msgStr string
					json.Unmarshal(msgRaw, &msgStr)
					resp.Error = jsonrpc2.NewError(-1, msgStr)
				}
			}
		}
		return &resp, int64(n), nil

	case "event":
		req := &jsonrpc2.Request{}
		if bodyRaw, ok := raw["body"]; ok {
			req.Params = bodyRaw
		} else {
			data, _ := json.Marshal(raw)
			req.Params = data
		}
		if eventRaw, ok := raw["event"]; ok {
			req.Method = string(eventRaw)
		}
		return req, int64(n), nil

	default:
		req := &jsonrpc2.Request{}
		if cmdRaw, ok := raw["command"]; ok {
			req.Method = string(cmdRaw)
		} else if methodRaw, ok := raw["method"]; ok {
			req.Method = string(methodRaw)
		}
		if argsRaw, ok := raw["arguments"]; ok {
			req.Params = argsRaw
		}
		return req, int64(n), nil
	}
}

type dapWriter struct {
	out io.Writer
}

func (w *dapWriter) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	data, err := jsonrpc2.EncodeMessage(msg)
	if err != nil {
		return 0, err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	n1, err := w.out.Write([]byte(header))
	if err != nil {
		return int64(n1), err
	}
	n2, err := w.out.Write(data)
	return int64(n1) + int64(n2), err
}

// --- DAP client types ---

// dapClient wraps a dlv-dap connection.
type dapClient struct {
	conn    *jsonrpc2.Connection
	dlvCmd  *exec.Cmd
	dlvPort int
	stdin   *bufio.Writer
	stdout  *bufio.Reader
	mu      sync.Mutex

	// event subscriptions
	eventCh chan DAPEvent
	stopCh  chan struct{}
}

// launchDLV starts dlv-dap and returns a client connected to it.
func launchDLV(ctx context.Context, program string, args []string, cwd string, port int) (*dapClient, error) {
	dlvPath, err := exec.LookPath("dlv")
	if err != nil {
		return nil, fmt.Errorf("dlv-dap not found: install with 'go install github.com/go-delve/delve/cmd/dlv@latest': %w", err)
	}

	cmd := exec.Command(dlvPath, "dap", "--log", "--listen", fmt.Sprintf("127.0.0.1:%d", port))
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(cmd.Environ(), "DLV_EXIT_ON_CLOSE=1")

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create dlv stdout pipe: %w", err)
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create dlv stdin pipe: %w", err)
	}

	cmd.Start()

	// Read port from dlv stderr to confirm it started
	portFound := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if idx := strings.Index(line, "listening at:"); idx >= 0 {
				parts := strings.Split(strings.TrimSpace(line[idx+len("listening at:"):]), ":")
				if len(parts) >= 2 {
					portStr := parts[len(parts)-1]
					portFound <- portStr
					return
				}
			}
		}
	}()

	// Wait for dlv to report its port
	var actualPort int
	select {
	case pStr := <-portFound:
		p, err := strconv.Atoi(pStr)
		if err != nil {
			actualPort = port
		} else {
			actualPort = p
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return nil, fmt.Errorf("dlv-dap did not report listening port within 10s")
	}

	// Connect via TCP with custom DAP framer
	conn, err := jsonrpc2.Dial(ctx, jsonrpc2.NetDialer("tcp", fmt.Sprintf("127.0.0.1:%d", actualPort), net.Dialer{}), &jsonrpc2.ConnectionOptions{})
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("connect to dlv-dap on :%d: %w", actualPort, err)
	}

	if err := initializeSession(conn); err != nil {
		conn.Close()
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialize dlv-dap session: %w", err)
	}

	client := &dapClient{
		conn:    conn,
		dlvCmd:  cmd,
		dlvPort: actualPort,
		stdin:   bufio.NewWriter(stdinPipe),
		stdout:  bufio.NewReader(stdoutPipe),
		eventCh: make(chan DAPEvent, 64),
		stopCh:  make(chan struct{}),
	}

	go client.readEvents()

	return client, nil
}

// attachDLV connects to an existing dlv-dap server on the given port.
func attachDLV(ctx context.Context, port int) (*dapClient, error) {
	conn, err := jsonrpc2.Dial(ctx, jsonrpc2.NetDialer("tcp", fmt.Sprintf("127.0.0.1:%d", port), net.Dialer{}), &jsonrpc2.ConnectionOptions{})
	if err != nil {
		return nil, fmt.Errorf("connect to dlv-dap on :%d: %w", port, err)
	}

	if err := initializeSession(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initialize dlv-dap session: %w", err)
	}

	client := &dapClient{
		conn:    conn,
		dlvPort: port,
		eventCh: make(chan DAPEvent, 64),
		stopCh:  make(chan struct{}),
	}

	go client.readEvents()

	return client, nil
}

// initializeSession sends the initialize request and performs the launch/initialized handshake.
func initializeSession(conn *jsonrpc2.Connection) error {
	initArgs := &InitializeRequestArguments{
		ProtocolVersion:                1,
		AdapterID:                      "delve",
		Locale:                         "en",
		SupportsVariablePresentationInfo: true,
	}

	var initResult json.RawMessage
	if err := conn.Call(context.Background(), "initialize", initArgs).Await(context.Background(), &initResult); err != nil {
		return fmt.Errorf("initialize request: %w", err)
	}
	_ = initResult

	launchArgs := map[string]any{}
	var launchResult json.RawMessage
	if err := conn.Call(context.Background(), "launch", launchArgs).Await(context.Background(), &launchResult); err != nil {
		return fmt.Errorf("launch request: %w", err)
	}
	_ = launchResult

	if err := conn.Notify(context.Background(), "initialized", map[string]any{}); err != nil {
		return fmt.Errorf("initialized notification: %w", err)
	}

	return nil
}

// readEvents reads DAP events from the dapClient's reader.
// The dapFramer converts DAP events to jsonrpc2.Request notifications.
// The Connection routes these through its handler, and we capture them
// via a custom Handler that feeds events to our channel.
func (dc *dapClient) readEvents() {
	defer close(dc.eventCh)

	for {
		select {
		case <-dc.stopCh:
			return
		default:
		}

		// The dapFramer reads Content-Length framed messages from the TCP connection.
		// We read the raw DAP envelope from our stdout pipe (which is dlv's stdout).
		line, err := dc.stdout.ReadString('\n')
		if err != nil {
			return
		}
		if len(strings.TrimSpace(line)) == 0 {
			continue
		}

		// Parse DAP envelope: {"seq":N,"type":"event","event":"stopped","body":{...}}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		evtType, _ := raw["type"]
		if string(evtType) != "event" {
			continue
		}

		evt := DAPEvent{
			Seq: 0,
			Type: "event",
			Body: raw["body"],
		}
		if seqRaw, ok := raw["seq"]; ok {
			var n int
			if err := json.Unmarshal(seqRaw, &n); err == nil {
				evt.Seq = n
			}
		}
		if eventRaw, ok := raw["event"]; ok {
			evt.Event = string(eventRaw)
		}

		select {
		case dc.eventCh <- evt:
		default:
		}
	}
}

// launch launches dlv-dap and returns a DAPClient.
func launch(program string, args []string, cwd string, port int) (*DAPClient, error) {
	dc, err := launchDLV(context.Background(), program, args, cwd, port)
	if err != nil {
		return nil, err
	}
	return &DAPClient{inner: dc}, nil
}

// attach attaches to an existing dlv-dap debug session.
func attach(port int) (*DAPClient, error) {
	dc, err := attachDLV(context.Background(), port)
	if err != nil {
		return nil, err
	}
	return &DAPClient{inner: dc}, nil
}

// DAPClient is the public-facing DAP client.
type DAPClient struct {
	inner *dapClient
}

// SetBreakpoint sets breakpoints in the given file at the specified line.
func (dc *DAPClient) SetBreakpoint(file string, line int) (*Breakpoint, error) {
	args := &SetBreakpointsArguments{
		Sources:     []Source{{Path: file}},
		Breakpoints: []BreakpointRequest{{Line: line}},
	}

	var result SetBreakpointsResponseResult
	if err := dc.inner.conn.Call(context.Background(), "setBreakpoints", args).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("setBreakpoints: %w", err)
	}
	if len(result.Breakpoints) == 0 {
		return nil, fmt.Errorf("setBreakpoints returned no breakpoints")
	}
	return &result.Breakpoints[0], nil
}

// RemoveBreakpoint removes a breakpoint by ID.
func (dc *DAPClient) RemoveBreakpoint(id string) error {
	_, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("invalid breakpoint id %q: %w", id, err)
	}

	var result SetBreakpointsResponseResult
	if err := dc.inner.conn.Call(context.Background(), "setBreakpoints", map[string]any{
		"source": map[string]any{}, "breakpoints": []any{}, "sourceModified": false,
	}).Await(context.Background(), &result); err != nil {
		return err
	}
	return nil
}

// Continue resumes execution of the debugged program.
func (dc *DAPClient) Continue() (*StoppedEvent, error) {
	var result json.RawMessage
	if err := dc.inner.conn.Call(context.Background(), "continue", &ContinueArguments{}).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("continue: %w", err)
	}
	return dc.waitForStoppedEvent()
}

// StepIn steps into the current function.
func (dc *DAPClient) StepIn() (*StoppedEvent, error) {
	var result json.RawMessage
	if err := dc.inner.conn.Call(context.Background(), "stepIn", &StepInArguments{}).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("stepIn: %w", err)
	}
	return dc.waitForStoppedEvent()
}

// StepOut steps out of the current function.
func (dc *DAPClient) StepOut() (*StoppedEvent, error) {
	var result json.RawMessage
	if err := dc.inner.conn.Call(context.Background(), "stepOut", &StepOutArguments{}).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("stepOut: %w", err)
	}
	return dc.waitForStoppedEvent()
}

// StepOver steps over the current line.
func (dc *DAPClient) StepOver() (*StoppedEvent, error) {
	var result json.RawMessage
	if err := dc.inner.conn.Call(context.Background(), "stepOver", &StepOverArguments{}).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("stepOver: %w", err)
	}
	return dc.waitForStoppedEvent()
}

// Pause pauses the debugged program.
func (dc *DAPClient) Pause() error {
	var result json.RawMessage
	return dc.inner.conn.Call(context.Background(), "pause", &PauseArguments{Reason: "pause"}).Await(context.Background(), &result)
}

// Evaluate evaluates an expression in the given scope.
func (dc *DAPClient) Evaluate(expr string, frame int, scope string) (*EvaluateResponseResult, error) {
	args := &EvaluateArguments{
		Expression: expr,
		FrameID:    frame,
		Context:    scope,
	}

	var result EvaluateResponseResult
	if err := dc.inner.conn.Call(context.Background(), "evaluate", args).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("evaluate: %w", err)
	}
	return &result, nil
}

// StackTrace returns the call stack up to the given depth.
func (dc *DAPClient) StackTrace(depth int) (*StackFramesResponseResult, error) {
	args := &StackTraceArguments{
		ThreadID:   0,
		StartFrame: 0,
		Levels:     depth,
	}
	var result StackFramesResponseResult
	if err := dc.inner.conn.Call(context.Background(), "stackTrace", args).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("stackTrace: %w", err)
	}
	return &result, nil
}

// Threads returns the list of debug threads.
func (dc *DAPClient) Threads() ([]Thread, error) {
	var result ThreadsResponseResult
	if err := dc.inner.conn.Call(context.Background(), "threads", nil).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("threads: %w", err)
	}
	return result.Threads, nil
}

// Scopes returns the variable scopes for the given stack frame.
func (dc *DAPClient) Scopes(frame int) ([]Scope, error) {
	args := &ScopesArguments{FrameID: frame}
	var result ScopesResponseResult
	if err := dc.inner.conn.Call(context.Background(), "scopes", args).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("scopes: %w", err)
	}
	return result.Scopes, nil
}

// Variables returns the variables in the given scope (by variable reference).
func (dc *DAPClient) Variables(varRef int) ([]Variable, error) {
	args := &VariablesArguments{VariableID: varRef}
	var result VariablesResponseResult
	if err := dc.inner.conn.Call(context.Background(), "variables", args).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("variables: %w", err)
	}
	return result.Variables, nil
}


// Terminate ends the debug session.
func (dc *DAPClient) Terminate() error {
	var result json.RawMessage
	if err := dc.inner.conn.Call(context.Background(), "terminate", nil).Await(context.Background(), &result); err != nil {
		return fmt.Errorf("terminate: %w", err)
	}
	if err := dc.inner.conn.Close(); err != nil {
		return err
	}
	close(dc.inner.stopCh)
	return nil
}

// Modules returns the list of loaded modules.
func (dc *DAPClient) Modules() ([]Module, error) {
	var result ModulesResponseResult
	if err := dc.inner.conn.Call(context.Background(), "modules", nil).Await(context.Background(), &result); err != nil {
		return nil, fmt.Errorf("modules: %w", err)
	}
	return result.Modules, nil
}

// waitForStoppedEvent waits for a stopped event from the DAP server.
func (dc *DAPClient) waitForStoppedEvent() (*StoppedEvent, error) {
	select {
	case evt := <-dc.inner.eventCh:
		if evt.Event == "stopped" {
			var body StoppedEventBody
			if err := json.Unmarshal(evt.Body, &body); err != nil {
				return nil, fmt.Errorf("parse stopped event body: %w", err)
			}
			return &StoppedEvent{body: body}, nil
		}
		return nil, fmt.Errorf("expected stopped event, got %q", evt.Event)
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("timed out waiting for stopped event")
	}
}

// StoppedEvent wraps the stopped event body.
type StoppedEvent struct {
	body StoppedEventBody
}

// Reason returns the reason for stopping (e.g., "breakpoint", "step", "pause").
func (se *StoppedEvent) Reason() string {
	return se.body.Reason
}

// ThreadID returns the thread ID that stopped.
func (se *StoppedEvent) ThreadID() int {
	return se.body.ThreadID
}

// Text returns optional human-readable text explaining the stop.
func (se *StoppedEvent) Text() string {
	return se.body.Text
}

// AllThreadsStopped returns true if all threads are stopped.
func (se *StoppedEvent) AllThreadsStopped() bool {
	return se.body.AllThreadsStopped
}

// HitBreakpointIDs returns the IDs of breakpoints that were hit.
func (se *StoppedEvent) HitBreakpointIDs() []int {
	return se.body.HitBreakpointIDs
}
