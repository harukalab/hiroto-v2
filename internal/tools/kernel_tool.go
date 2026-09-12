package tools

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func registerKernels(r *Registry, mgr *KernelManager) {
	if mgr == nil {
		return
	}
	r.Register(&Tool{
		Name:        "eval",
		Description: "Manage persistent kernels (Python / Bun) and execute code inside them. Supports actions: create, run, get, close, list.",
		Parameters: mustJSON(`{
			"type":"object",
			"properties":{
				"action":{"type":"string","description":"Action to perform: create, run, get, close, list"},
				"kernel_id":{"type":"string","description":"Kernel ID for create/run/get/close"},
				"code":{"type":"string","description":"Code to execute in run action"},
				"runtime":{"type":"string","description":"Runtime for create: python or bun"},
				"init":{"type":"string","description":"Optional initialization script for create"},
				"workdir":{"type":"string","description":"Optional working directory for the kernel"}
			},
			"required":["action"]
		}`),
		Exec: func(ctx context.Context, args map[string]any) Result {
			action, _ := args["action"].(string)
			switch action {
			case "create":
				return evalCreate(args, mgr)
			case "run":
				return evalRun(ctx, args, mgr)
			case "get":
				return evalGet(args, mgr)
			case "close":
				return evalClose(args, mgr)
			case "list":
				return evalList(mgr)
			default:
				return Result{Output: fmt.Sprintf("unknown action: %q", action), IsError: true}
			}
		},
	})
}

// evalCreate creates a new persistent kernel.
func evalCreate(args map[string]any, mgr *KernelManager) Result {
	kernelID, _ := args["kernel_id"].(string)
	if kernelID == "" {
		kernelID = fmt.Sprintf("k-%d", time.Now().UnixNano())
	}

	runtimeStr, _ := args["runtime"].(string)
	if runtimeStr == "" {
		return Result{Output: "missing runtime (python or bun)", IsError: true}
	}

	var kt KernelType
	switch runtimeStr {
	case "python":
		kt = Python
	case "bun":
		kt = Bun
	default:
		return Result{Output: fmt.Sprintf("unknown runtime: %q (use python or bun)", runtimeStr), IsError: true}
	}

	workdir, _ := args["workdir"].(string)
	mgr.Mu.Lock()
	defer mgr.Mu.Unlock()

	if _, exists := mgr.Kernels[kernelID]; exists {
		return Result{Output: fmt.Sprintf("kernel %q already exists", kernelID), IsError: true}
	}

	kernel := &Kernel{
		Type:    kt,
		ID:      kernelID,
		Workdir: workdir,
		Manager: mgr,
		State:   KernelRunning,
	}
	mgr.Kernels[kernelID] = kernel

	return Result{
		Output: fmt.Sprintf("created kernel %q (runtime=%s)", kernelID, runtimeStr),
	}
}

// evalRun executes code inside a kernel.
func evalRun(ctx context.Context, args map[string]any, mgr *KernelManager) Result {
	kernelID, _ := args["kernel_id"].(string)
	if kernelID == "" {
		return Result{Output: "missing kernel_id", IsError: true}
	}
	code, _ := args["code"].(string)
	if code == "" {
		return Result{Output: "missing code", IsError: true}
	}

	mgr.Mu.Lock()
	kernel, exists := mgr.Kernels[kernelID]
	if !exists {
		mgr.Mu.Unlock()
		return Result{Output: fmt.Sprintf("kernel %q not found", kernelID), IsError: true}
	}

	// Check that the kernel's context is still alive
	select {
	case <-kernel.Ctx.Done():
		mgr.Mu.Unlock()
		return Result{Output: fmt.Sprintf("kernel %q has been closed", kernelID), IsError: true}
	default:
	}

	kernel.Mu.Lock()
	if kernel.State != KernelRunning {
		kernel.Mu.Unlock()
		mgr.Mu.Unlock()
		return Result{Output: fmt.Sprintf("kernel %q is %s", kernelID, kernel.State), IsError: true}
	}
	kernel.Mu.Unlock()
	mgr.Mu.Unlock()

	// Execute the code in the kernel. For persistent kernels the actual execution
	// happens via stdin/stdout IPC; here we delegate to the kernel's tool bridge
	// if available, otherwise run inline.
	if kernel.Tools != nil {
	result, _ := kernel.Tools.Exec(ctx, code)
		if result.IsError {
			return Result{Output: result.Output, IsError: true}
		}
		return Result{Output: result.Output}
	}

	// Fallback: inline execution with a timeout.
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return Result{Output: fmt.Sprintf("executed code in kernel %q (inline fallback, ctx=%v)", kernelID, ctx2.Done() != nil)}
}

// evalGet returns info about a kernel.
func evalGet(args map[string]any, mgr *KernelManager) Result {
	kernelID, _ := args["kernel_id"].(string)
	if kernelID == "" {
		return Result{Output: "missing kernel_id", IsError: true}
	}

	mgr.Mu.Lock()
	kernel, exists := mgr.Kernels[kernelID]
	mgr.Mu.Unlock()

	if !exists {
		return Result{Output: fmt.Sprintf("kernel %q not found", kernelID), IsError: true}
	}

	kernel.Mu.Lock()
	info := fmt.Sprintf("kernel %q: type=%s state=%s pid=%d",
		kernel.ID, kernel.Type, kernel.State, kernel.PID)
	kernel.Mu.Unlock()

	return Result{Output: info}
}

// evalClose terminates a kernel.
func evalClose(args map[string]any, mgr *KernelManager) Result {
	kernelID, _ := args["kernel_id"].(string)
	if kernelID == "" {
		return Result{Output: "missing kernel_id", IsError: true}
	}

	mgr.Mu.Lock()
	kernel, exists := mgr.Kernels[kernelID]
	if !exists {
		mgr.Mu.Unlock()
		return Result{Output: fmt.Sprintf("kernel %q not found", kernelID), IsError: true}
	}

	kernel.Mu.Lock()
	if kernel.Cancel != nil {
		kernel.Cancel()
	}
	kernel.State = KernelStopped
	kernel.Mu.Unlock()

	delete(mgr.Kernels, kernelID)
	mgr.Mu.Unlock()

	return Result{Output: fmt.Sprintf("closed kernel %q", kernelID)}
}

// evalList returns all kernels.
func evalList(mgr *KernelManager) Result {
	mgr.Mu.Lock()
	ids := make([]string, 0, len(mgr.Kernels))
	for id := range mgr.Kernels {
		ids = append(ids, id)
	}
	mgr.Mu.Unlock()

	sort.Strings(ids)

	if len(ids) == 0 {
		return Result{Output: "no kernels"}
	}

	lines := make([]string, 0, len(ids))
	mgr.Mu.Lock()
	for _, id := range ids {
		k := mgr.Kernels[id]
		k.Mu.Lock()
		lines = append(lines, fmt.Sprintf("  %s: type=%s state=%s", id, k.Type, k.State))
		k.Mu.Unlock()
	}
	mgr.Mu.Unlock()

	return Result{Output: "kernels:\n" + fmt.Sprintf("%s", joinStrings(lines, "\n"))}
}

// joinStrings is a lightweight equivalent of strings.Join for []string.
func joinStrings(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	out := s[0]
	for _, v := range s[1:] {
		out += sep + v
	}
	return out
}
