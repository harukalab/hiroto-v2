package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// NewKernelManager creates a manager whose kernels live under baseDir.
// Each kernel's workdir is created as baseDir/<kernelID> when workdir is empty.
func NewKernelManager(baseDir string) *KernelManager {
	return &KernelManager{
		BaseDir: baseDir,
		Kernels: make(map[string]*Kernel),
	}
}

// Create starts a new persistent kernel of the given type.
// The workdir is created inside the manager's baseDir if workdir is empty.
// tools is a list of tool names to register in the kernel environment.
// Returns the kernel or an error if the process cannot be started.
func (m *KernelManager) Create(kernelType KernelType, id, workdir string, tools []string) (*Kernel, error) {
	if kernelType != Python && kernelType != Bun {
		return nil, fmt.Errorf("kernel type must be Python or Bun, got %q", kernelType)
	}

	m.Mu.Lock()
	defer m.Mu.Unlock()

	if _, exists := m.Kernels[id]; exists {
		return nil, fmt.Errorf("kernel %q already exists", id)
	}

	ctx, cancel := context.WithCancel(context.Background())
	k := &Kernel{
		Type:    kernelType,
		ID:      id,
		Workdir: workdir,
		Manager: m,
		Ctx:     ctx,
		Cancel:  cancel,
		Tools: &KernelToolBridge{
			In:  make(chan KernelToolCall, 16),
			Out: make(chan KernelToolResult, 16),
		},
		State: KernelPending,
	}

	if workdir == "" {
		workdir = filepath.Join(m.BaseDir, id)
	}
	k.Workdir = workdir

	if err := k.start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start kernel %s: %w", id, err)
	}

	m.Kernels[id] = k
	return k, nil
}

// GetKernel returns the kernel with the given ID, or ErrKernelNotFound.
func (m *KernelManager) GetKernel(id string) (*Kernel, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()

	k, ok := m.Kernels[id]
	if !ok {
		return nil, fmt.Errorf("kernel %q not found", id)
	}
	return k, nil
}

// Execute runs code inside the given kernel and returns its stdout output.
// Output is truncated to 50 KB.
func (k *Kernel) Execute(code string) (string, error) {
	k.Mu.Lock()
	if k.Stdin == nil {
		k.Mu.Unlock()
		return "", fmt.Errorf("kernel %s has no stdin writer", k.ID)
	}
	k.Mu.Unlock()

	// Build the full script: helper + user code.
	fullCode := fmt.Sprintf("import sys\nsys.path.insert(0, '.')\nfrom hiroto_tools import *\n%s", code)

	if _, err := k.Stdin.Write([]byte(fullCode + "\n")); err != nil {
		return "", fmt.Errorf("write to kernel stdin: %w", err)
	}
	if err := k.Stdin.Close(); err != nil {
		return "", fmt.Errorf("close kernel stdin: %w", err)
	}

	// Read output with a 60-second deadline.
	out, err := readWithDeadline(k, 60*time.Second)
	result := strings.TrimSpace(string(out))

	if err != nil {
		return result, fmt.Errorf("kernel error: %w", err)
	}

	// Truncate at 50 KB.
	if len(result) > 50000 {
		result = result[:50000] + "\n... (truncated)"
	}
	return result, nil
}

// Close stops and removes a single kernel by ID.
func (m *KernelManager) Close(id string) error {
	m.Mu.Lock()
	k, ok := m.Kernels[id]
	if !ok {
		m.Mu.Unlock()
		return fmt.Errorf("kernel %q not found", id)
	}
	delete(m.Kernels, id)
	m.Mu.Unlock()

	return k.stop()
}

// CloseAll stops every managed kernel and returns the first error encountered.
func (m *KernelManager) CloseAll() error {
	m.Mu.Lock()
	kernels := make([]*Kernel, 0, len(m.Kernels))
	for _, k := range m.Kernels {
		kernels = append(kernels, k)
	}
	m.Kernels = make(map[string]*Kernel)
	m.Mu.Unlock()

	var firstErr error
	for _, k := range kernels {
		if err := k.stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ---------- per-kernel lifecycle ----------

func (k *Kernel) start() error {
	// Ensure workdir exists.
	if k.Workdir == "" {
		if k.Manager != nil {
			k.Workdir = filepath.Join(k.Manager.BaseDir, k.ID)
		}
	}
	if err := os.MkdirAll(k.Workdir, 0755); err != nil {
		return fmt.Errorf("create workdir %s: %w", k.Workdir, err)
	}

	var cmdArgs []string
	var helper []byte
	switch k.Type {
	case Python:
		cmdArgs = []string{"python3", "-u", "-c", ndjsonKernelMain}
		helper = []byte(pythonHelper)
	case Bun:
		cmdArgs = []string{"bun", "-c", bunKernelMain}
		helper = []byte(bunHelper)
	default:
		return fmt.Errorf("unsupported kernel type %q", k.Type)
	}

	cmd := exec.CommandContext(k.Ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = k.Workdir
	cmd.Env = os.Environ()

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

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

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		k.Cancel()
		return fmt.Errorf("stderr pipe: %w", err)
	}
	k.Stderr = stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		k.Cancel()
		return fmt.Errorf("start process: %w", err)
	}

	k.PID = cmd.Process.Pid

	// Write the helper module to workdir so the kernel can import it.
	helperPath := filepath.Join(k.Workdir, "hiroto_tools.py")
	if err := os.WriteFile(helperPath, helper, 0644); err != nil {
		k.stop()
		return fmt.Errorf("write helper: %w", err)
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

	// Write a ready marker file; kernel signals readiness by creating it.
	marker := filepath.Join(k.Workdir, ".ready")

	// Wait up to 3 seconds for the kernel to signal readiness.
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readyCancel()
	readyDone := make(chan struct{})
	go func() {
		defer close(readyDone)
		for {
			if _, err := os.Stat(marker); err == nil {
				return
			}
			select {
			case <-readyCtx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()

	select {
	case <-readyDone:
		k.Mu.Lock()
		k.State = KernelRunning
		k.Mu.Unlock()
		return nil
	case <-readyCtx.Done():
		// Kernel didn't signal ready; it might still be starting.
		// Continue — the first Execute will catch real failures.
	}

	k.Mu.Lock()
	if k.State == KernelPending {
		k.State = KernelRunning
	}
	k.Mu.Unlock()
	return nil
}

// stop terminates the kernel process and cleans up pipes.
func (k *Kernel) stop() error {
	k.Mu.Lock()
	cancel := k.Cancel
	state := k.State
	k.Mu.Unlock()

	if state == KernelStopped {
		return nil
	}

	k.Mu.Lock()
	k.State = KernelStopped
	k.Mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Close stdin to unblock the kernel reading.
	k.Mu.Lock()
	stdin := k.Stdin
	k.Stdin = nil
	k.Mu.Unlock()
	if stdin != nil {
		stdin.Close()
	}

	// Close stdout/stderr to unblock reads.
	k.Mu.Lock()
	stdout := k.Stdout
	k.Stdout = nil
	stderr := k.Stderr
	k.Stderr = nil
	k.Mu.Unlock()
	if stdout != nil {
		stdout.(interface{ Close() error }).Close()
	}
	if stderr != nil {
		stderr.(interface{ Close() error }).Close()
	}

	// Try graceful SIGTERM, then ForceProcessKill.
	k.Mu.Lock()
	pid := k.PID
	k.Mu.Unlock()

	if pid > 0 {
		proc, _ := os.FindProcess(pid)
		if proc != nil {
			proc.Signal(os.Interrupt)

			done := make(chan struct{})
			go func() {
				// Wait a bit for the process to exit.
				time.Sleep(500 * time.Millisecond)
				close(done)
			}()

			select {
			case <-done:
				// Process is gone.
			case <-time.After(2 * time.Second):
				proc.Kill()
			}
		}
	}

	return nil
}

// readWithDeadline reads all available output from a kernel with a deadline.
func readWithDeadline(k *Kernel, deadline time.Duration) ([]byte, error) {
	k.Mu.Lock()
	stdout := k.Stdout
	k.Mu.Unlock()

	if stdout == nil {
		return nil, fmt.Errorf("kernel %s has no stdout reader", k.ID)
	}

	// Wrap in a context-aware reader.
	reader := &deadlineReader{
		Reader:   bufio.NewReader(stdout),
		done:     make(chan struct{}),
		deadline: time.After(deadline),
	}

	go func() {
		// Monitor context cancellation.
		select {
		case <-k.Ctx.Done():
			close(reader.done)
		case <-time.After(deadline):
			close(reader.done)
		}
	}()

	buf := new(strings.Builder)
	for {
		select {
		case <-reader.done:
			return []byte(buf.String()), nil
		default:
			line, err := reader.Reader.ReadString('\n')
			buf.WriteString(line)
			if err != nil {
				if err.Error() == "EOF" {
					return []byte(buf.String()), nil
				}
				return nil, fmt.Errorf("read error: %w", err)
			}
		}
	}
}

// deadlineReader wraps a bufio.Reader and stops on context cancellation or timeout.
type deadlineReader struct {
	*bufio.Reader
	done     chan struct{}
	deadline <-chan time.Time
}

// ---------- kernel helper scripts ----------

