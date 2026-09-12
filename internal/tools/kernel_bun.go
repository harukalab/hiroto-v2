package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NewBunKernel creates a persistent Bun kernel process that communicates
// via NDJSON over stdin/stdout. Tool calls are intercepted by the host Go
// process, executed through the provided registry, and results are sent back
// to Bun as it runs.
func NewBunKernel(ctx context.Context, id string, workdir string, tools []string) (*Kernel, error) {
	cmd := exec.CommandContext(ctx, "bun", "-u", "-c", bunKernelMain)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), "BUN_DEBUG=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	k := &Kernel{
		ID:     id,
		Type:   Bun,
		Stdin:  stdin,
		Stdout: stdout,
		Tools: &KernelToolBridge{
			In:  make(chan KernelToolCall, 64),
			Out: make(chan KernelToolResult, 64),
		},
		Ctx:    ctx,
		Cancel: func() {},
		State:  KernelPending,
	}
	k.Cancel = func() {
		cmd.Process.Kill()
		stdin.Close()
		stdout.Close()
	}

	// Send bootstrap: announce available tools, start main loop.
	var toolsJSON string
	if len(tools) > 0 {
		b, _ := json.Marshal(tools)
		toolsJSON = string(b)
	}
	boot := bunMsg{
		Type: "boot",
		Payload: bunBoot{
			Tools:   toolsJSON,
			Workdir: workdir,
		},
	}
	if err := writeMsg(stdin, boot); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("bootstrap: %w", err)
	}

	// Drain bootstrap responses.
	bootResp, err := readMsg(stdout)
	if err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("bootstrap response: %w", err)
	}
	if bootResp.Type == "error" {
		cmd.Process.Kill()
		return nil, fmt.Errorf("bootstrap: %s", bootResp.Error)
	}

	// Transition to running state.
	k.State = KernelRunning
	k.PID = cmd.Process.Pid

	go k.readLoop()

	return k, nil
}

// RunCodeBun sends code to the Bun kernel, executes it, and returns the final output.
func (k *Kernel) RunCodeBun(ctx context.Context, code string) (string, error) {
	if k.State != KernelRunning {
		return "", fmt.Errorf("kernel %s is not running (state=%s)", k.ID, k.State)
	}

	if err := writeMsg(k.Stdin, bunMsg{
		Type:    "code",
		Payload: bunCode{Code: code},
	}); err != nil {
		return "", fmt.Errorf("send code: %w", err)
	}

	// Wait for done or error.
	for {
		m, err := readMsg(k.Stdout)
		if err != nil {
			return "", fmt.Errorf("read response: %w", err)
		}
		switch m.Type {
		case "done":
			if pd, ok := m.Payload.(bunDone); ok {
				return pd.Result, nil
			}
		case "error":
			return "", fmt.Errorf("kernel error: %s", m.Error)
		}
	}
}

// CloseBun terminates the Bun kernel and cleans up the subprocess.
func (k *Kernel) CloseBun() error {
	if k.Cancel != nil {
		k.Cancel()
	}
	k.State = KernelStopped
	return nil
}

// ---- Bun-specific message types ----

// bunMsg is an NDJSON message from the Bun kernel.
type bunMsg struct {
	Type    string      `json:"type"`
	ID      int         `json:"id,omitempty"`
	Error   string      `json:"error,omitempty"`
	Payload interface{} `json:"payload,omitempty"`
}

// bunBoot is the bootstrap payload sent to and echoed by the Bun kernel.
type bunBoot struct {
	Tools   string `json:"tools"`
	Workdir string `json:"workdir"`
}

// bunCode is a code execution request.
type bunCode struct {
	Code string `json:"code"`
}

// bunToolCall is an incoming tool call from the Bun kernel.
type bunToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// bunToolResult is the response to a tool call.
type bunToolResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

// bunDone is the completion response from the Bun kernel.
type bunDone struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}

func readMsg(r io.Reader) (bunMsg, error) {
	var m bunMsg
	if err := json.NewDecoder(r).Decode(&m); err != nil {
		return bunMsg{}, err
	}
	return m, nil
}

func writeMsg(w io.Writer, msg bunMsg) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ---- read loop ----

func (k *Kernel) readLoop() {
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
			k.State = KernelStopped
			return
		case "tool_call":
			k.handleBunToolCall(m)
		case "tool_result":
			// Consume tool results on the channel.
			var tr bunMsg
			json.Unmarshal(raw, &tr)
			idStr := fmt.Sprintf("%d", tr.ID)
			payload, _ := tr.Payload.(map[string]interface{})
			result, _ := payload["result"].(string)
			isError, _ := payload["is_error"].(bool)
			select {
			case k.Tools.Out <- KernelToolResult{
				ID:      idStr,
				Output:  result,
				IsError: isError,
			}:
			default:
			}
		case "error":
			k.State = KernelErrored
			return
		}
	}
}

func (k *Kernel) handleBunToolCall(m bunMsg) {
	// Extract tool call from payload.
	var call bunMsg
	b, _ := json.Marshal(m.Payload)
	json.Unmarshal(b, &call)

	// Parse the payload as a bunToolCall.
	toolCall, _ := m.Payload.(map[string]interface{})
	name, _ := toolCall["name"].(string)
	args, _ := toolCall["arguments"].(string)

	result := ""
	var isErr bool

	// Execute via the tool bridge.
	switch name {
	case "terminal":
		var argsMap map[string]interface{}
		if err := json.Unmarshal([]byte(args), &argsMap); err == nil {
			if cmd, ok := argsMap["command"].(string); ok {
				result = bunExecuteTerminal(cmd)
			}
		}
	case "read_file":
		var argsMap map[string]interface{}
		if err := json.Unmarshal([]byte(args), &argsMap); err == nil {
			if path, ok := argsMap["path"].(string); ok {
				content, err := bunExecuteReadFile(path)
				if err != nil {
					result = fmt.Sprintf(`{"Output": "%s", "IsError": true}`, bunEscapeJSON(err.Error()))
					isErr = true
				} else {
					result = fmt.Sprintf(`{"Output": %s}`, bunEscapeJSON(content))
				}
			}
		}
	case "write_file":
		var argsMap map[string]interface{}
		if err := json.Unmarshal([]byte(args), &argsMap); err == nil {
			if path, ok := argsMap["path"].(string); ok {
				if content, ok := argsMap["content"].(string); ok {
					err := bunExecuteWriteFile(path, content)
					if err != nil {
						result = fmt.Sprintf(`{"Output": "%s", "IsError": true}`, bunEscapeJSON(err.Error()))
						isErr = true
					} else {
						result = `{"Output": "ok"}`
					}
				}
			}
		}
	default:
		result = fmt.Sprintf(`{"Output": "unknown tool: %s", "IsError": true}`, name)
		isErr = true
	}

	resp := bunMsg{
		Type: "tool_result",
		ID:   call.ID,
		Payload: bunToolResult{
			Result:  result,
			IsError: isErr,
		},
	}
	writeMsg(k.Stdin, resp)
}

// ---- tool executors ----

func bunExecuteTerminal(cmd string) string {
	out, err := exec.CommandContext(context.Background(), "sh", "-c", cmd).CombinedOutput()
	result := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Sprintf(`{"output": %s, "isError": true}`, bunEscapeJSON(result+"\n"+err.Error()))
	}
	return fmt.Sprintf(`{"output": %s}`, bunEscapeJSON(result))
}

func bunExecuteReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func bunExecuteWriteFile(path, content string) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// bunEscapeJSON returns a JSON-escaped version of s, suitable for embedding
// in a JSON string literal without additional quotes.
func bunEscapeJSON(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal wraps in double quotes; strip them.
	return string(b[1 : len(b)-1])
}

// ---- Bun helper script ----

const bunKernelMain = `
import { runBunKernel } from "./hiroto_tools.js";

runBunKernel().catch(e => { console.error(e); process.exit(1); });
`

const bunHelper = `
import { writeFileSync, readFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createInterface } from "node:readline";

const __dirname = fileURLToPath(new URL(".", import.meta.url));

async function sendMsg(msg) {
    const b = JSON.stringify(msg) + "\\n";
    process.stdout.write(b);
}

function tool(name, args) {
    args = args || {};
    return new Promise((resolve, reject) => {
        const inp = JSON.stringify(args);
        const child = spawn("hiroto", ["tool", name], {
            stdio: ["pipe", "pipe", "pipe"],
            timeout: 55000,
        });
        let out = "";
        child.stdout.on("data", d => out += d);
        child.stderr.on("data", () => {});
        child.on("error", () => reject(new Error("tool spawn failed")));
        child.on("close", (code) => {
            if (code !== 0 && code !== null) {
                return reject(new Error("tool exit " + code));
            }
            try { resolve(JSON.parse(out)); }
            catch(e) { reject(new Error("parse error")); }
        });
        child.stdin.write(inp);
        child.stdin.end();
    });
}

// Convenience wrappers
async function terminal(cmd) { return tool("terminal", {command: cmd}); }
async function read_file(path) { return tool("read_file", {path}); }
async function write_file(path, content) { return tool("write_file", {path, content}); }
async function web_search(query, limit) { return tool("web_search", {query, limit}); }
async function web_fetch(url) { return tool("web_fetch", {url}); }
async function browser_start() { return tool("browser_start", {}); }
async function browser_navigate(url) { return tool("browser_navigate", {url}); }
async function browser_click(sel) { return tool("browser_click", {selector: sel}); }
async function browser_exec(code) { return tool("browser_exec", {code}); }
async function browser_stop() { return tool("browser_stop", {}); }

// Ready signal
writeFileSync(join(__dirname, ".ready"), "ready");

async function runBunKernel() {
    // Bootstrap: wait for boot message, echo back ready.
    const rl = createInterface({ input: process.stdin, crlfDelay: Infinity });
    let bootMsg = "";
    for await (const line of rl) {
        bootMsg = line;
        break;
    }

    try {
        const boot = JSON.parse(bootMsg);
        // Echo back bootstrap success.
        await sendMsg({ type: "boot", payload: { ok: true } });
    } catch(e) {
        await sendMsg({ type: "error", error: "bootstrap parse: " + e.message });
        process.exit(1);
        return;
    }

    // Main loop: read code messages, execute, send done.
    for await (const line of rl) {
        try {
            const msg = JSON.parse(line);
            if (msg.type !== "code") continue;

            const code = msg.payload.code;
            let result = "";
            let err = "";

            try {
                // Execute the code in the Bun runtime.
                // Available globals: Bun.file, Bun.write, Bun.$, fetch, Buffer
                const evalResult = await eval(code);
                if (evalResult !== undefined) {
                    result = String(evalResult);
                }
            } catch (e) {
                err = e.stack || String(e);
            }

            await sendMsg({
                type: "done",
                payload: { result, error: err, stderr: "" },
            });
        } catch (e) {
            // Parse error on input — ignore silently.
        }
    }
}
`
