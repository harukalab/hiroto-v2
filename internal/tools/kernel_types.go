package tools

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// KernelType identifies the runtime engine of a persistent kernel.
type KernelType string

const (
	Python KernelType = "python"
	Bun    KernelType = "bun"
)

// StdinWriter writes commands to a kernel process.
type StdinWriter interface {
	Write(p []byte) (int, error)
	Close() error
}

// StdoutReader reads output from a kernel process.
type StdoutReader interface {
	Read(p []byte) (int, error)
}

// StderrReader reads error output from a kernel process.
type StderrReader interface {
	Read(p []byte) (int, error)
}

// KernelToolBridge handles serialization/deserialization of tool calls between the host and kernel.
type KernelToolBridge struct {
	Mu    sync.Mutex
	In    chan KernelToolCall
	Out   chan KernelToolResult
}

// Kernel represents a persistent runtime instance (Python interpreter or Bun process).
type Kernel struct {
	Type    KernelType
	ID      string
	PID     int
	Workdir string
	Manager *KernelManager
	Stdin   StdinWriter
	Stdout  StdoutReader
	Stderr  StderrReader
	Tools   *KernelToolBridge
	Ctx     context.Context
	Cancel  context.CancelFunc
	Mu      sync.Mutex
	State   KernelState
}

// KernelState describes the lifecycle phase of a kernel.
type KernelState string

const (
	KernelPending KernelState = "pending"
	KernelRunning KernelState = "running"
	KernelStopped KernelState = "stopped"
	KernelErrored KernelState = "errored"
)

// Exec executes a code string through the tool bridge and returns the result.
func (k *KernelToolBridge) Exec(ctx context.Context, code string) (*KernelToolResult, error) {
	k.Mu.Lock()
	defer k.Mu.Unlock()

	call := KernelToolCall{
		ID:   "exec-" + strconv.Itoa(int(time.Now().UnixNano())),
		Tool: "code",
		Args: code,
	}
	select {
	case k.In <- call:
	default:
		return nil, fmt.Errorf("tool bridge input channel full")
	}

	select {
	case result := <-k.Out:
		return &result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// KernelManager tracks and persists kernel instances across tool calls.
type KernelManager struct {
	Mu      sync.Mutex
	Kernels map[string]*Kernel
	BaseDir string
}

// KernelToolCall represents an incoming tool invocation from a kernel.
type KernelToolCall struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Args string `json:"args"`
}

// KernelToolResult is the response sent back to a kernel after tool execution.
type KernelToolResult struct {
	ID      string `json:"id"`
	Output  string `json:"output"`
	IsError bool   `json:"isError"`
}
