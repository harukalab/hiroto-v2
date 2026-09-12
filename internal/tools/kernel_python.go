package tools

import (
	"strings"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// NewPythonKernel creates a persistent Python kernel process that communicates
// via NDJSON over stdin/stdout. Tool calls are intercepted by the host Go
// process, executed through the provided registry, and results are sent back
// to Python as it runs.
func NewPythonKernel(ctx context.Context, id string, workdir string, tools []string) (*Kernel, error) {
	ctx, cancel := context.WithCancel(ctx)

	k := &Kernel{
		ID:     id,
		Type:   Python,
		Stdin:  nil, // set by start
		Stdout: nil, // set by start
		Tools: &KernelToolBridge{
			In:  make(chan KernelToolCall, 64),
			Out: make(chan KernelToolResult, 64),
		},
		Ctx:    ctx,
		Cancel: cancel,
		State:  KernelPending,
	}

	if workdir == "" {
		workdir = k.Workdir
	}

	if err := k.startNDJSON(tools); err != nil {
		cancel()
		return nil, fmt.Errorf("start kernel %s: %w", id, err)
	}

	return k, nil
}

// startNDJSON spawns the Python subprocess with NDJSON protocol.
func (k *Kernel) startNDJSON(toolList []string) error {
	if k.Workdir == "" {
		if k.Manager != nil {
			k.Workdir = k.Manager.BaseDir
		}
	}
	if k.Workdir == "" {
		k.Workdir = ""
	}
	if err := os.MkdirAll(k.Workdir, 0755); err != nil {
		return fmt.Errorf("create workdir %s: %w", k.Workdir, err)
	}

	cmd := exec.CommandContext(k.Ctx, "python3", "-u", "-c", ndjsonKernelMain)
	cmd.Dir = k.Workdir
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	k.Stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		k.Cancel()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	k.Stdout = stdout

	k.PID = cmd.Process.Pid

	// Write the helper module to workdir so the kernel can import it.
	helperPath := filepath.Join(k.Workdir, "hiroto_tools.py")
	if err := os.WriteFile(helperPath, []byte(pythonHelper), 0644); err != nil {
		k.Cancel()
		return fmt.Errorf("write helper: %w", err)
	}

	// Send bootstrap: announce available tools, start main loop.
	var toolsJSON string
	if len(toolList) > 0 {
		b, _ := json.Marshal(toolList)
		toolsJSON = string(b)
	}
	boot := bunMsg{
		Type: "boot",
		Payload: bunBoot{
			Tools:   toolsJSON,
			Workdir: k.Workdir,
		},
	}
	if err := writeMsg(stdin, boot); err != nil {
		k.Cancel()
		return fmt.Errorf("bootstrap: %w", err)
	}

	// Drain bootstrap responses.
	bootResp, err := readMsg(stdout)
	if err != nil {
		k.Cancel()
		return fmt.Errorf("bootstrap response: %w", err)
	}
	if bootResp.Type == "error" {
		k.Cancel()
		return fmt.Errorf("bootstrap: %s", bootResp.Error)
	}

	// Monitor process exit.
	go func() {
		cmd.Wait()
		k.Mu.Lock()
		if k.State != KernelStopped {
			k.State = KernelErrored
		}
		k.Mu.Unlock()
		k.Cancel()
	}()

	// Transition to running state.
	k.Mu.Lock()
	k.State = KernelRunning
	k.Mu.Unlock()

	go k.pyReadLoop()

	return nil
}

// ---- Python-specific read loop (uses bunMsg/bunBoot types but Python executors) ----

func (k *Kernel) pyReadLoop() {
	dec := json.NewDecoder(k.Stdout)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return
		}

		var m bunMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}

		switch m.Type {
		case "done":
			k.Mu.Lock()
			k.State = KernelStopped
			k.Mu.Unlock()
			k.Cancel()
			return
		case "tool_call":
			k.pyHandleToolCall(m)
		case "tool_result":
			// Consume tool results on the channel.
			var tr bunMsg
			json.Unmarshal(raw, &tr)
			payload := tr.Payload.(map[string]interface{})
			k.Tools.Out <- KernelToolResult{
				ID:      fmt.Sprintf("%d", tr.ID),
				Output:  payload["result"].(string),
				IsError: payload["is_error"].(bool),
			}
		case "error":
			k.Mu.Lock()
			k.State = KernelErrored
			k.Mu.Unlock()
			k.Cancel()
			return
		}
	}
}

func (k *Kernel) pyHandleToolCall(m bunMsg) {
	// Extract tool call from payload.
	var call bunMsg
	b, _ := json.Marshal(m.Payload)
	json.Unmarshal(b, &call)

	name := call.Payload.(map[string]interface{})["name"].(string)
	args := call.Payload.(map[string]interface{})["arguments"].(string)

	result := ""
	var isErr bool
	k.Mu.Lock()
	if fn, ok := pyToolExecutors[name]; ok {
		result = fn(args)
	} else {
		result = fmt.Sprintf(`{"output": "unknown tool: %s", "isError": true}`, name)
		isErr = true
	}
	k.Mu.Unlock()

	resp := bunMsg{
		Type: "tool_result",
		ID:   call.ID,
		Payload: pyDone{
			Result:  result,
			IsError: isErr,
		},
	}
	writeMsg(k.Stdin, resp)
}

// pyToolExecutors is a map of tool name -> executor function for this kernel.
var pyToolExecutors = map[string]func(string) string{
	"terminal": func(args string) string {
		return executeTerminal(args)
	},
	"read_file": func(args string) string {
		return executeReadFile(args)
	},
	"write_file": func(args string) string {
		return executeWriteFile(args)
	},
}

// executeTerminal runs a shell command and returns the result as JSON.
func executeTerminal(args string) string {
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Sprintf(`{"output": "parse error: %s", "isError": true}`, err.Error())
	}
	cmd, _ := p["command"].(string)
	if cmd == "" {
		return `{"output": "missing command", "isError": true}`
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Sprintf(`{"output": %s, "isError": true}`, mustJSON(result+"\n"+err.Error()))
	}
	return fmt.Sprintf(`{"output": %s}`, mustJSON(result))
}

// executeReadFile reads a file and returns its content as JSON.
func executeReadFile(args string) string {
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Sprintf(`{"output": "parse error: %s", "isError": true}`, err.Error())
	}
	path, _ := p["path"].(string)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf(`{"output": %s, "isError": true}`, mustJSON(err.Error()))
	}
	return fmt.Sprintf(`{"output": %s}`, mustJSON(string(data)))
}

// executeWriteFile writes content to a file and returns result as JSON.
func executeWriteFile(args string) string {
	var p map[string]interface{}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return fmt.Sprintf(`{"output": "parse error: %s", "isError": true}`, err.Error())
	}
	path, _ := p["path"].(string)
	content, _ := p["content"].(string)
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Sprintf(`{"output": %s, "isError": true}`, mustJSONStr(err.Error()))
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf(`{"output": "write error: %s", "isError": true}`, mustJSONStr(err.Error()))
	}
	return `{"output": "ok", "isError": false}`
}

// mustJSONStr returns a properly JSON-escaped version of s.
func mustJSONStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---- RunCode for Python kernel ----

// RunCode sends code to the kernel, executes it, and returns the final output.
func (k *Kernel) RunCode(ctx context.Context, code string) (string, error) {
	if k.State != KernelRunning {
		return "", fmt.Errorf("kernel %s is not running (state=%s)", k.ID, k.State)
	}

	// Send code message.
	codeMsg := bunMsg{
		Type:    "code",
		Payload: bunCode{Code: code},
	}
	if err := writeMsg(k.Stdin, codeMsg); err != nil {
		return "", fmt.Errorf("send code: %w", err)
	}

	// Read responses until done.
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		m, err := readMsg(k.Stdout)
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}

		switch m.Type {
		case "done":
			if pd, ok := m.Payload.(pyDone); ok {
				return pd.Result, nil
			}
			return m.Payload.(map[string]interface{})["result"].(string), nil
		case "error":
			return "", fmt.Errorf("kernel error: %s", m.Error)
		}
	}
}

// Close terminates the kernel and cleans up the subprocess.
func (k *Kernel) Close() error {
	if k.Cancel != nil {
		k.Cancel()
	}

	k.Mu.Lock()
	stdin := k.Stdin
	stdout := k.Stdout
	k.Stdin = nil
	k.Stdout = nil
	k.Mu.Unlock()

	if stdin != nil {
		if c, ok := stdin.(interface{ Close() error }); ok {
			c.Close()
		}
	}
	if stdout != nil {
		if c, ok := stdout.(interface{ Close() error }); ok {
			c.Close()
		}
	}

	// Wait a bit for the process to exit.
	time.Sleep(100 * time.Millisecond)

	k.Mu.Lock()
	pid := k.PID
	k.Mu.Unlock()

	if pid > 0 {
		proc, _ := os.FindProcess(pid)
		if proc != nil {
			proc.Signal(os.Interrupt)
			time.Sleep(200 * time.Millisecond)
			proc.Kill()
		}
	}

	return nil
}

// ---- NDJSON Python kernel bootstrap script ----

// ndjsonKernelMain is the Python NDJSON protocol server. It bootstraps with
// available tool names, then reads code blocks from stdin, executes them,
// and communicates tool calls via stdout in NDJSON format.
const ndjsonKernelMain = `
import sys, os, json, subprocess

# Register available tools from the bootstrap message.
boot = json.loads(sys.stdin.readline())
tools = json.loads(boot["tools"])
os.chdir(boot["workdir"])

def tool(name, args=None):
    if args is None:
        args = {}
    inp = json.dumps(args)
    try:
        r = subprocess.run(['hiroto', 'tool', name],
            input=inp, capture_output=True, text=True, timeout=55)
        return json.loads(r.stdout)
    except Exception as e:
        return {"output": "tool error: " + str(e), "isError": True}

def terminal(cmd):
    return tool('terminal', {'command': cmd})

def read_file(path):
    return tool('read_file', {'path': path})

def write_file(path, content):
    return tool('write_file', {'path': path, 'content': content})

def glob(pattern, path='.'):
    return tool('glob', {'pattern': pattern, 'path': path})

def write_tool(path, content):
    return tool('write_file', {'path': path, 'content': content})

def web_search(query, limit=5):
    return tool('web_search', {'query': query, 'limit': limit})

def web_fetch(url):
    return tool('web_fetch', {'url': url})

# Bootstrap response.
print(json.dumps({"type": "ok"}))
sys.stdout.flush()

# Signal readiness.
with open('.ready', 'w') as f:
    f.write('ready')

# Main loop: read code blocks, execute, communicate via NDJSON.
counter = 0
while True:
    line = sys.stdin.readline()
    if not line:
        break
    try:
        msg = json.loads(line)
        if msg["type"] == "code":
            code = msg["payload"]["code"]
            full_code = "import sys\nsys.path.insert(0, '.')\nfrom hiroto_tools import *\n" + code
            try:
                stdout_buf = ""

                class _StdoutCapture:
                    def __init__(self):
                        self._buf = ""
                    def write(self, s):
                        self._buf += s
                    def flush(self):
                        pass
                    def getvalue(self):
                        return self._buf

                old_stdout = sys.stdout
                sys.stdout = _StdoutCapture()
                try:
                    exec(full_code, {"__name__": "__kernel__", "tool": tool, "terminal": terminal, "read_file": read_file, "write_file": write_file, "glob": glob, "write_tool": write_tool, "web_search": web_search, "web_fetch": web_fetch})
                finally:
                    sys.stdout = old_stdout
                stdout_buf = stdout_buf.getvalue()

                id_ = counter
                counter += 1

                print(json.dumps({
                    "type": "done",
                    "id": id_,
                    "payload": {"result": stdout_buf}
                }))
                sys.stdout.flush()
            except Exception as e:
                import traceback
                err_text = traceback.format_exc()
                print(json.dumps({
                    "type": "done",
                    "id": id_,
                    "payload": {"result": err_text, "error": str(e)}
                }))
                sys.stdout.flush()
    except json.JSONDecodeError:
        continue
`

// ---- pyDone message type (Python-specific, same structure as bunDone) ----

type pyDone struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// ---- Tool registration for Python kernel tool ----

func registerPythonKernelTool(r *Registry) {
	r.Register(&Tool{
		Name:        "execute_python_kernel",
		Description: "Execute Python code in a persistent kernel process. Creates a kernel if kernel_id is omitted, or runs code in an existing kernel. State persists between calls.",
		Parameters: mustJSON(`{"type":"object","properties":{"code":{"type":"string","description":"Python code to execute"}, "kernel_id":{"type":"string","description":"Kernel ID to use. Omit to auto-create."}},"required":["code"]}`),
		Exec:        pythonKernelExec,
	})
}

func pythonKernelExec(ctx context.Context, args map[string]any) Result {
	code, _ := args["code"].(string)
	if code == "" {
		return Result{Output: "missing code", IsError: true}
	}

	kid, _ := args["kernel_id"].(string)
	var k *Kernel
	var err error
	if kid == "" {
		kid = fmt.Sprintf("py-%d", time.Now().UnixNano())
		k, err = NewPythonKernel(ctx, kid, "", nil)
		if err != nil {
			return Result{Output: fmt.Sprintf("kernel create: %v", err), IsError: true}
		}
		kernelsGlobal[kid] = k
	} else {
		k, err = getKernel(kid)
		if err != nil {
			return Result{Output: fmt.Sprintf("kernel %q not found", kid), IsError: true}
		}
	}

	// Execute code via RunCode.
	out, err := k.RunCode(ctx, code)
	if err != nil {
		if len(out) > 50000 {
			out = out[:50000] + "\n... (truncated)"
		}
		return Result{Output: out, IsError: true}
	}
	if len(out) > 50000 {
		out = out[:50000] + "\n... (truncated)"
	}
	return Result{Output: out}
}

// ---- Simple global kernel registry for the tool ----

var (
	kernelsGlobal map[string]*Kernel
	kernelsMu     sync.Mutex
)

func init() {
	kernelsGlobal = make(map[string]*Kernel)
}

func getKernel(id string) (*Kernel, error) {
	kernelsMu.Lock()
	defer kernelsMu.Unlock()
	k, ok := kernelsGlobal[id]
	if !ok {
		return nil, fmt.Errorf("kernel %q not found", id)
	}
	return k, nil
}
