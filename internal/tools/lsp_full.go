package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// goplsClient manages communication with gopls over LSP stdio.
type goplsClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int
}

// jsonRPCMessage is the JSON-RPC 2.0 wire format.
type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// newGoplsClient starts gopls and returns an initialized client.
func newGoplsClient(ctx context.Context, workdir string) (*goplsClient, error) {
	goplsPath := findGopls()
	cmd := exec.CommandContext(ctx, goplsPath, "serve")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "GO111MODULE=on")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("gopls start: %w", err)
	}

	client := &goplsClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	if err := client.initialize(ctx); err != nil {
		client.Close()
		return nil, fmt.Errorf("gopls initialize: %w", err)
	}

	// Enable go workspace settings.
	if err := client.setGoWorkspace(ctx); err != nil {
		// Non-fatal; some projects don't use modules.
		_ = err
	}

	return client, nil
}

// initialize sends the LSP initialize request and waits for the response.
func (c *goplsClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"processId":    os.Getpid(),
		"clientInfo":   map[string]any{"name": "hiroto"},
		"rootUri":      fileURI(""),
		"capabilities": map[string]any{},
		"workspaceFolders": []any{map[string]any{
			"uri":  fileURI(""),
			"name": ".",
		}},
	}

	resp, err := c.request(ctx, "initialize", params)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize response error: %s", resp.Error.Message)
	}
	return nil
}

// setGoWorkspace sends a workspace/configuration request for gopls settings.
func (c *goplsClient) setGoWorkspace(ctx context.Context) error {
	params := []any{map[string]any{"goUseGopls": true}}
	resp, err := c.request(ctx, "workspace/configuration", params)
	_ = resp
	return err
}

// Close shuts down gopls.
func (c *goplsClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil {
		c.cmd.Process.Signal(os.Interrupt)
		c.cmd.Wait()
	}
}

// request sends a JSON-RPC request and returns the response.
func (c *goplsClient) request(ctx context.Context, method string, params any) (*jsonRPCMessage, error) {
	id := func() int {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.nextID++
		return c.nextID
	}()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      id,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	c.mu.Lock()
	w := c.stdin
	c.mu.Unlock()

	if w == nil {
		return nil, fmt.Errorf("client closed")
	}

	// LSP framing: Content-Length header + double CRLF + body
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := w.Write([]byte(header)); err != nil {
		return nil, fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return nil, fmt.Errorf("write body: %w", err)
	}

	// Wait for response
	c.mu.Lock()
	reader := c.stdout
	c.mu.Unlock()
	if reader == nil {
		return nil, fmt.Errorf("client closed")
	}

	return c.readResponse(ctx, reader, id)
}

// readResponse reads a JSON-RPC response from the LSP framing.
func (c *goplsClient) readResponse(ctx context.Context, reader *bufio.Reader, expectedID int) (*jsonRPCMessage, error) {
	for {
		msg, err := c.readNextMessage(reader)
		if err != nil {
			return nil, err
		}

		// If it's a notification (no id) or the right response, return.
		if msg.ID == nil {
			continue
		}
		if *msg.ID == expectedID {
			return msg, nil
		}
	}
}

// readNextMessage reads a single LSP-framed JSON-RPC message.
func (c *goplsClient) readNextMessage(reader *bufio.Reader) (*jsonRPCMessage, error) {
	var buf bytes.Buffer
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read header line: %w", err)
		}
		if bytes.Equal(bytes.TrimSpace(line), []byte{}) {
			// End of headers — next bytes are the JSON body.
			break
		}
		buf.Write(line)
	}

	// Parse Content-Length
	contentLen := 0
	for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("Content-Length:")) {
			val := strings.TrimSpace(string(trimmed[len("Content-Length:"):]))
			fmt.Sscanf(val, "%d", &contentLen)
		}
	}
	if contentLen == 0 {
		return nil, fmt.Errorf("missing or zero Content-Length")
	}

	body := make([]byte, contentLen)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var msg jsonRPCMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, fmt.Errorf("parse json-rpc: %s", string(body))
	}
	return &msg, nil
}

// fileURI converts a file path to a file:// URI.
func fileURI(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return "file://" + filepath.ToSlash(abs)
}

// textDocumentPosition builds LSP textDocument + position.
func textDocumentPosition(uri string, line, character int) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
	}
}

// lspReferences finds all references to the symbol at a position.
func (c *goplsClient) lspReferences(ctx context.Context, uri string, line, character int) ([]lspLocation, error) {
	resp, err := c.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
		"context":      map[string]any{"includeDeclaration": false},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("references: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		return []lspLocation{}, nil
	}

	var locations []lspLocation
	if err := json.Unmarshal(resp.Result, &locations); err != nil {
		return nil, fmt.Errorf("parse references: %w", err)
	}
	return locations, nil
}

// lspRename renames a symbol at a position to newName across the workspace.
func (c *goplsClient) lspRename(ctx context.Context, uri string, line, character int, newName string) (string, error) {
	resp, err := c.request(ctx, "textDocument/rename", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
		"newName":      newName,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("rename: %s", resp.Error.Message)
	}

	if resp.Result == nil {
		return "no changes needed", nil
	}

	var edit map[string]any
	if err := json.Unmarshal(resp.Result, &edit); err != nil {
		return "", fmt.Errorf("parse rename edit: %w", err)
	}

	var changedFiles []string
	if changes, ok := edit["documentChanges"].([]any); ok {
		for _, dc := range changes {
			dcMap, ok := dc.(map[string]any)
			if !ok {
				continue
			}
			// Structured document change: { "textDocument": { "uri": ..., "version": ... }, "edits": [...] }
			if _, hasTE := dcMap["textEdits"]; hasTE {
				if td, hasTD := dcMap["textDocument"].(map[string]any); hasTD {
					if uri, hasURI := td["uri"]; hasURI {
						changedFiles = append(changedFiles, fmt.Sprintf("%s", uri))
					}
				}
			} else if uri, hasURI := dcMap["uri"]; hasURI {
				// Legacy format: { "uri": ..., "newText": ... }
				changedFiles = append(changedFiles, fmt.Sprintf("%s", uri))
			}
		}
	}

	return fmt.Sprintf("renamed to %s across %d file(s)", newName, len(changedFiles)), nil
}

// lspCodeActions lists code actions at a position, or applies the one at the given index.
func (c *goplsClient) lspCodeActions(ctx context.Context, uri string, line, character int, actionIndex *int) (string, error) {
	resp, err := c.request(ctx, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": line, "character": character},
		"range": map[string]any{
			"start": map[string]any{"line": line, "character": character},
			"end":   map[string]any{"line": line, "character": character},
		},
		"context": map[string]any{"diagnostics": []any{}},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("code actions: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		return "no code actions available", nil
	}

	var actions []map[string]any
	if err := json.Unmarshal(resp.Result, &actions); err != nil {
		return "", fmt.Errorf("parse code actions: %w", err)
	}

	if len(actions) == 0 {
		return "no code actions available", nil
	}

	if actionIndex != nil && *actionIndex >= 0 && *actionIndex < len(actions) {
		action := actions[*actionIndex]
		// Apply the selected code action
		if edit, hasEdit := action["edit"]; hasEdit {
			resp, err := c.request(ctx, "workspace/applyEdit", map[string]any{
				"edit": edit,
			})
			if err != nil {
				return "", fmt.Errorf("apply edit: %w", err)
			}
			if resp.Error != nil {
				return "", fmt.Errorf("apply edit response: %s", resp.Error.Message)
			}
			return fmt.Sprintf("applied code action: %s", action["title"]), nil
		}
		return "", fmt.Errorf("code action %d has no edit", *actionIndex)
	}

	// List all actions
	var sb strings.Builder
	sb.WriteString("Available code actions:\n")
	for i, a := range actions {
		title, _ := a["title"].(string)
		sb.WriteString(fmt.Sprintf("  %d: %s\n", i, title))
	}
	return sb.String(), nil
}

// lspWorkspaceSymbols searches workspace symbols matching query.
func (c *goplsClient) lspWorkspaceSymbols(ctx context.Context, query string, limit int, scope string) ([]lspSymbolInformation, error) {
	resp, err := c.request(ctx, "workspace/symbol", map[string]any{
		"query": query,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("workspace symbols: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		return []lspSymbolInformation{}, nil
	}

	var symbols []lspSymbolInformation
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		return nil, fmt.Errorf("parse workspace symbols: %w", err)
	}

	if scope != "" {
		scopeURI := fileURI(scope)
		var filtered []lspSymbolInformation
		for _, s := range symbols {
			if s.Location.URI == scopeURI || strings.HasSuffix(s.Location.URI, scope) {
				filtered = append(filtered, s)
			}
		}
		symbols = filtered
	}

	if limit > 0 && len(symbols) > limit {
		symbols = symbols[:limit]
	}

	return symbols, nil
}

// goplsExecuteCommand runs a gopls-specific command (e.g. "source.organizeImports").
func (c *goplsClient) goplsExecuteCommand(ctx context.Context, command string, args ...any) (string, error) {
	resp, err := c.request(ctx, "workspace/executeCommand", map[string]any{
		"command":   command,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("execute command %s: %s", command, resp.Error.Message)
	}
	if resp.Result != nil {
		return string(resp.Result), nil
	}
	return fmt.Sprintf("command %s succeeded", command), nil
}

// --- Tool exec functions ---

func lspRenameExec(ctx context.Context, args map[string]any) Result {
	path, _ := args["path"].(string)
	if path == "" {
		return Result{Output: "missing path", IsError: true}
	}

	positionJSON, _ := args["position"].(string)
	line, character := 0, 0
	if positionJSON != "" {
		var pos map[string]any
		if err := json.Unmarshal([]byte(positionJSON), &pos); err == nil {
			if l, ok := pos["line"].(float64); ok {
				line = int(l)
			}
			if c, ok := pos["character"].(float64); ok {
				character = int(c)
			}
		}
	}

	newName, _ := args["newName"].(string)
	if newName == "" {
		return Result{Output: "missing newName", IsError: true}
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Result{Output: fmt.Sprintf("file not found: %s", path), IsError: true}
	}

	client, err := newGoplsClient(ctx, ".")
	if err != nil {
		return Result{Output: fmt.Sprintf("gopls unavailable: %v", err), IsError: true}
	}
	defer client.Close()

	uri := fileURI(path)
	result, err := client.lspRename(ctx, uri, line, character, newName)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return Result{Output: result}
}

func lspCodeActionsExec(ctx context.Context, args map[string]any) Result {
	path, _ := args["path"].(string)
	if path == "" {
		return Result{Output: "missing path", IsError: true}
	}

	positionJSON, _ := args["position"].(string)
	line, character := 0, 0
	if positionJSON != "" {
		var pos map[string]any
		if err := json.Unmarshal([]byte(positionJSON), &pos); err == nil {
			if l, ok := pos["line"].(float64); ok {
				line = int(l)
			}
			if c, ok := pos["character"].(float64); ok {
				character = int(c)
			}
		}
	}

	actionJSON, _ := args["action"].(string)
	actionIndex := -1
	if actionJSON != "" {
		if _, err := fmt.Sscanf(actionJSON, "%d", &actionIndex); err != nil {
			// Try parsing as JSON number
			var idx float64
			if err2 := json.Unmarshal([]byte(actionJSON), &idx); err2 == nil {
				actionIndex = int(idx)
			}
		}
	}

	client, err := newGoplsClient(ctx, ".")
	if err != nil {
		return Result{Output: fmt.Sprintf("gopls unavailable: %v", err), IsError: true}
	}
	defer client.Close()

	result, err := client.lspCodeActions(ctx, fileURI(path), line, character, &actionIndex)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}
	return Result{Output: result}
}

func lspSymbolsExec(ctx context.Context, args map[string]any) Result {
	query, _ := args["query"].(string)
	if query == "" {
		return Result{Output: "missing query", IsError: true}
	}

	limit := 50
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > 200 {
		limit = 200
	}

	scope, _ := args["path"].(string)

	client, err := newGoplsClient(ctx, ".")
	if err != nil {
		return Result{Output: fmt.Sprintf("gopls unavailable: %v", err), IsError: true}
	}
	defer client.Close()

	symbols, err := client.lspWorkspaceSymbols(ctx, query, limit, scope)
	if err != nil {
		return Result{Output: err.Error(), IsError: true}
	}

	if len(symbols) == 0 {
		return Result{Output: fmt.Sprintf("no symbols matching '%s'", query)}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d symbol(s) matching '%s':\n\n", len(symbols), query))
	for _, s := range symbols {
		kind := symbolKindName(s.Kind)
		sb.WriteString(fmt.Sprintf("  %s %q (%s:%d)\n", kind, s.Name, s.Location.URI, s.Location.Range.Start.Line+1))
	}
	return Result{Output: sb.String()}
}

func symbolKindName(kind float64) string {
	kindNames := []string{
		"", "File", "Module", "Namespace", "Package",
		"Class", "Method", "Property", "Field", "Constructor",
		"Enum", "Interface", "Function", "Variable", "Constant",
		"String", "Number", "Boolean", "Array", "Object",
		"Key", "Null", "EnumMember", "Struct", "Event",
		"Operator", "TypeParameter",
	}
	idx := int(kind)
	if idx < 0 || idx >= len(kindNames) {
		return "Unknown"
	}
	if kindNames[idx] == "" {
		return "Unknown"
	}
	return kindNames[idx]
}

func findGopls() string {
	// Check common locations
	paths := []string{
		"/usr/local/go/bin/gopls",
		"$HOME/go/bin/gopls",
		"$GOPATH/bin/gopls",
	}
	for _, p := range paths {
		p = os.ExpandEnv(p)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fall back to PATH lookup
	if p, err := exec.LookPath("gopls"); err == nil {
		return p
	}
	return "gopls"
}

// --- LSP types ---

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspSymbolInformation struct {
	Name       string      `json:"name"`
	Kind       float64     `json:"kind"`
	Location   lspLocation `json:"location"`
	Deprecated *bool       `json:"deprecated,omitempty"`
}
