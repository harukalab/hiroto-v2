package tools

import (
	"strings"
	"sync"
	"context"
	"encoding/json"
	"fmt"
	
)

// registerDAP registers the debug tool with the registry.
func registerDAP(r *Registry) {
	r.Register(&Tool{
		Name:        "debug",
		Description: "DAP debugger client for Go programs. Launch or attach to a dlv-dap session, set breakpoints, step through code, and inspect variables.",
		Parameters: mustJSON(`{
			"type": "object",
			"properties": {
				"action": {
					"type": "string",
					"description": "Action to perform: launch, attach, set_breakpoint, remove_breakpoint, continue, step_over, step_in, step_out, pause, evaluate, stack_trace, threads, scopes, variables, terminate, modules",
					"enum": ["launch", "attach", "set_breakpoint", "remove_breakpoint", "continue", "step_over", "step_in", "step_out", "pause", "evaluate", "stack_trace", "threads", "scopes", "variables", "terminate", "modules"]
				},
				"program": {
					"type": "string",
					"description": "Path to the Go binary to launch (required for launch action)"
				},
				"args": {
					"type": "string",
					"description": "Command-line arguments to pass to the program (required for launch action)"
				},
				"cwd": {
					"type": "string",
					"description": "Working directory for the program (required for launch action)"
				},
				"port": {
					"type": "integer",
					"description": "Port for dlv-dap server. Auto-assigned for launch, required for attach"
				},
				"breakpoints": {
					"type": "string",
					"description": "Initial breakpoints as JSON array of {file, line} objects (optional, launch only)"
				},
				"file": {
					"type": "string",
					"description": "File path for set_breakpoint action"
				},
				"line": {
					"type": "integer",
					"description": "Line number for set_breakpoint action"
				},
				"breakpoint_id": {
					"type": "string",
					"description": "Breakpoint ID for remove_breakpoint action"
				},
				"expr": {
					"type": "string",
					"description": "Expression to evaluate (required for evaluate action)"
				},
				"frame": {
					"type": "integer",
					"description": "Stack frame index (used by evaluate, scopes actions)"
				},
				"scope": {
					"type": "string",
					"description": "Evaluation context for evaluate action: variables, locals, arguments, watch (default: variables)"
				},
				"depth": {
					"type": "integer",
					"description": "Maximum number of stack frames to return (stack_trace action, default: 20)"
				},
				"var_ref": {
					"type": "integer",
					"description": "Variable reference ID (variables action)"
				}
			},
			"required": ["action"]
		}`),
		Exec: debugExec,
	})
}

// debugExec dispatches debug actions to the DAP client.
func debugExec(ctx context.Context, args map[string]any) Result {
	action, _ := args["action"].(string)
	if action == "" {
		return Result{Output: "missing required parameter: action", IsError: true}
	}

	switch action {
	case "launch":
		return debugLaunch(ctx, args)
	case "attach":
		return debugAttach(ctx, args)
	case "set_breakpoint":
		return debugSetBreakpoint(ctx, args)
	case "remove_breakpoint":
		return debugRemoveBreakpoint(ctx, args)
	case "continue":
		return debugContinue(ctx, args)
	case "step_over":
		return debugStepOver(ctx, args)
	case "step_in":
		return debugStepIn(ctx, args)
	case "step_out":
		return debugStepOut(ctx, args)
	case "pause":
		return debugPause(ctx, args)
	case "evaluate":
		return debugEvaluate(ctx, args)
	case "stack_trace":
		return debugStackTrace(ctx, args)
	case "threads":
		return debugThreads(ctx, args)
	case "scopes":
		return debugScopes(ctx, args)
	case "variables":
		return debugVariables(ctx, args)
	case "terminate":
		return debugTerminate(ctx, args)
	case "modules":
		return debugModules(ctx, args)
	default:
		return Result{Output: fmt.Sprintf("unknown debug action: %q", action), IsError: true}
	}
}

// state holds the active DAP client for this agent session.
var (
	dapStateMu sync.Mutex
	dapState   *activeDAP
)

type activeDAP struct {
	client *DAPClient
}

func debugLaunch(ctx context.Context, args map[string]any) Result {
	program, _ := args["program"].(string)
	if program == "" {
		return Result{Output: "launch action requires 'program' parameter", IsError: true}
	}

	port := 0
	if p, ok := toInt(args["port"]); ok && p > 0 {
		port = p
	}

	cwd, _ := args["cwd"].(string)

	// Parse initial breakpoints
	var bpArgs []string
	if bpJSON, ok := args["breakpoints"].(string); ok && bpJSON != "" {
		bpArgs = []string{bpJSON}
	}

	// Parse program arguments
	argsStr, _ := args["args"].(string)

	// Parse breakpoints
	var bps []any
	if bpArgs != nil {
		if err := json.Unmarshal([]byte(bpArgs[0]), &bps); err != nil {
			return Result{Output: fmt.Sprintf("invalid breakpoints JSON: %v", err), IsError: true}
		}
	}

	// Create launch args for dlv
	dlvArgs := []string{"--headless", "--api-version=2", "--accept-multiclient"}
	if cwd != "" {
		dlvArgs = append(dlvArgs, "--wd", cwd)
	}
	dlvArgs = append(dlvArgs, "exec")
	if program != "" {
		dlvArgs = append(dlvArgs, program)
	}
	if argsStr != "" {
		dlvArgs = append(dlvArgs, "--", argsStr)
	}

	client, err := launch(dlvArgs[0], dlvArgs[1:], cwd, port)
	if err != nil {
		return Result{Output: fmt.Sprintf("failed to launch dlv-dap: %v", err), IsError: true}
	}

	dapStateMu.Lock()
	dapState = &activeDAP{client: client}
	dapStateMu.Unlock()

	// Set initial breakpoints if provided
	var bpsSet []string
	for _, bp := range bps {
		bpMap, ok := bp.(map[string]any)
		if !ok {
			continue
		}
		file, _ := bpMap["file"].(string)
		line, _ := toInt(bpMap["line"])
		if file != "" && line > 0 {
			bp, err := client.SetBreakpoint(file, line)
			if err != nil {
				return Result{Output: fmt.Sprintf("failed to set breakpoint %s:%d: %v", file, line, err), IsError: true}
			}
			bpsSet = append(bpsSet, fmt.Sprintf("%s:%d (ID: %d)", file, line, bp.ID))
		}
	}

	output := fmt.Sprintf("launched dlv-dap on port %d", port)
	if len(bpsSet) > 0 {
		output += fmt.Sprintf("\nbreakpoints set: %s", strings.Join(bpsSet, "; "))
	}

	return Result{Output: output}
}

func debugAttach(ctx context.Context, args map[string]any) Result {
	port := 0
	if p, ok := toInt(args["port"]); ok && p > 0 {
		port = p
	}

	client, err := attach(port)
	if err != nil {
		return Result{Output: fmt.Sprintf("failed to attach to dlv-dap on port %d: %v", port, err), IsError: true}
	}

	dapStateMu.Lock()
	dapState = &activeDAP{client: client}
	dapStateMu.Unlock()

	return Result{Output: fmt.Sprintf("attached to dlv-dap on port %d", port)}
}

func debugSetBreakpoint(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	file, _ := args["file"].(string)
	if file == "" {
		return Result{Output: "set_breakpoint requires 'file' parameter", IsError: true}
	}

	line, _ := toInt(args["line"])
	if line <= 0 {
		return Result{Output: "set_breakpoint requires positive 'line' parameter", IsError: true}
	}

	bp, err := client.SetBreakpoint(file, line)
	if err != nil {
		return Result{Output: fmt.Sprintf("failed to set breakpoint at %s:%d: %v", file, line, err), IsError: true}
	}

	output := fmt.Sprintf("breakpoint set: %s:%d (ID: %d, verified: %v)", file, line, bp.ID, bp.Verified)
	if bp.Message != "" {
		output += fmt.Sprintf("\nmessage: %s", bp.Message)
	}

	return Result{Output: output}
}

func debugRemoveBreakpoint(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	id, _ := args["breakpoint_id"].(string)
	if id == "" {
		return Result{Output: "remove_breakpoint requires 'breakpoint_id' parameter", IsError: true}
	}

	err := client.RemoveBreakpoint(id)
	if err != nil {
		return Result{Output: fmt.Sprintf("failed to remove breakpoint %s: %v", id, err), IsError: true}
	}

	return Result{Output: fmt.Sprintf("breakpoint %s removed", id)}
}

func debugContinue(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	stopped, err := client.Continue()
	if err != nil {
		return Result{Output: fmt.Sprintf("continue: %v", err), IsError: true}
	}

	return Result{Output: fmt.Sprintf("resumed, stopped: reason=%s, thread=%d, text=%s",
		stopped.Reason(), stopped.ThreadID(), stopped.Text())}
}

func debugStepOver(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	stopped, err := client.StepOver()
	if err != nil {
		return Result{Output: fmt.Sprintf("step_over: %v", err), IsError: true}
	}

	return Result{Output: fmt.Sprintf("stepped over, stopped: reason=%s, thread=%d, text=%s",
		stopped.Reason(), stopped.ThreadID(), stopped.Text())}
}

func debugStepIn(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	stopped, err := client.StepIn()
	if err != nil {
		return Result{Output: fmt.Sprintf("step_in: %v", err), IsError: true}
	}

	return Result{Output: fmt.Sprintf("stepped in, stopped: reason=%s, thread=%d, text=%s",
		stopped.Reason(), stopped.ThreadID(), stopped.Text())}
}

func debugStepOut(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	stopped, err := client.StepOut()
	if err != nil {
		return Result{Output: fmt.Sprintf("step_out: %v", err), IsError: true}
	}

	return Result{Output: fmt.Sprintf("stepped out, stopped: reason=%s, thread=%d, text=%s",
		stopped.Reason(), stopped.ThreadID(), stopped.Text())}
}

func debugPause(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	if err := client.Pause(); err != nil {
		return Result{Output: fmt.Sprintf("pause: %v", err), IsError: true}
	}

	return Result{Output: "paused execution"}
}

func debugEvaluate(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	expr, _ := args["expr"].(string)
	if expr == "" {
		return Result{Output: "evaluate requires 'expr' parameter", IsError: true}
	}

	frame := 0
	if f, ok := toInt(args["frame"]); ok {
		frame = f
	}

	scope := "variables"
	if s, ok := args["scope"].(string); ok {
		scope = s
	}

	result, err := client.Evaluate(expr, frame, scope)
	if err != nil {
		return Result{Output: fmt.Sprintf("evaluate '%s': %v", expr, err), IsError: true}
	}

	output := fmt.Sprintf("evaluate '%s': %s", expr, result.Result)
	if result.Type != "" {
		output += fmt.Sprintf(" (type: %s)", result.Type)
	}
	if result.VariablesReference > 0 {
		output += fmt.Sprintf (" (has %d child variables)", result.VariablesReference)
	}

	return Result{Output: output}
}

func debugStackTrace(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	depth := 20
	if d, ok := toInt(args["depth"]); ok && d > 0 {
		depth = d
	}

	result, err := client.StackTrace(depth)
	if err != nil {
		return Result{Output: fmt.Sprintf("stack_trace: %v", err), IsError: true}
	}

	if len(result.StackFrames) == 0 {
		return Result{Output: "no stack frames"}
	}

	var sb strings.Builder
	sb.WriteString("stack trace:\n")
	for i, frame := range result.StackFrames {
		name := frame.Name
		if name == "" {
			name = "<unknown>"
		}
		path := ""
		if frame.Source != nil && frame.Source.Path != "" {
			path = frame.Source.Path
		} else if frame.Source != nil && frame.Source.Name != "" {
			path = frame.Source.Name
		}
		sb.WriteString(fmt.Sprintf("  #%d: %s at %s:%d\n", i, name, path, frame.Line))
	}

	return Result{Output: sb.String()}
}

func debugThreads(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	threads, err := client.Threads()
	if err != nil {
		return Result{Output: fmt.Sprintf("threads: %v", err), IsError: true}
	}

	if len(threads) == 0 {
		return Result{Output: "no threads"}
	}

	var sb strings.Builder
	sb.WriteString("threads:\n")
	for _, t := range threads {
		sb.WriteString(fmt.Sprintf("  %d: %s\n", t.ID, t.Name))
	}

	return Result{Output: sb.String()}
}

func debugScopes(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	frame := 0
	if f, ok := toInt(args["frame"]); ok {
		frame = f
	}

	scopes, err := client.Scopes(frame)
	if err != nil {
		return Result{Output: fmt.Sprintf("scopes: %v", err), IsError: true}
	}

	if len(scopes) == 0 {
		return Result{Output: "no scopes"}
	}

	var sb strings.Builder
	sb.WriteString("scopes:\n")
	for _, s := range scopes {
		sb.WriteString(fmt.Sprintf("  %s (ref: %d)\n", s.Name, s.VariablesReference))
	}

	return Result{Output: sb.String()}
}

func debugVariables(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	varRef := 0
	if v, ok := toInt(args["var_ref"]); ok {
		varRef = v
	}

	vars, err := client.Variables(varRef)
	if err != nil {
		return Result{Output: fmt.Sprintf("variables: %v", err), IsError: true}
	}

	if len(vars) == 0 {
		return Result{Output: "no variables"}
	}

	var sb strings.Builder
	sb.WriteString("variables:\n")
	for _, v := range vars {
		sb.WriteString(fmt.Sprintf("  %s = %s", v.Name, v.Value))
		if v.Type != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", v.Type))
		}
		if v.VariablesReference > 0 {
			sb.WriteString(fmt.Sprintf(" [%d children]", v.VariablesReference))
		}
		sb.WriteString("\n")
	}

	return Result{Output: sb.String()}
}

func debugTerminate(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session", IsError: true}
	}

	err := client.Terminate()
	dapStateMu.Lock()
	dapState = nil
	dapStateMu.Unlock()

	if err != nil {
		return Result{Output: fmt.Sprintf("terminate: %v", err), IsError: true}
	}

	return Result{Output: "terminated debug session"}
}

func debugModules(ctx context.Context, args map[string]any) Result {
	dapStateMu.Lock()
	client := dapState.client
	dapStateMu.Unlock()

	if client == nil {
		return Result{Output: "no active debug session (launch or attach first)", IsError: true}
	}

	modules, err := client.Modules()
	if err != nil {
		return Result{Output: fmt.Sprintf("modules: %v", err), IsError: true}
	}

	if len(modules) == 0 {
		return Result{Output: "no modules"}
	}

	var sb strings.Builder
	sb.WriteString("loaded modules:\n")
	for _, m := range modules {
		opt := ""
		if !m.IsOptimized {
			opt = " [unoptimized]"
		}
		sb.WriteString(fmt.Sprintf("  %s%s\n", m.Name, opt))
	}

	return Result{Output: sb.String()}
}
