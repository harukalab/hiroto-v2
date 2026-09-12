package tools

import (
	"encoding/json"
	"sync/atomic"

	"golang.org/x/exp/jsonrpc2"
)

// --- DAP protocol types ---

// ID tracks request IDs for the JSON-RPC protocol.
type jsonrpcID struct {
	seq atomic.Int64
}

func (s *jsonrpcID) next() jsonrpc2.ID {
	return jsonrpc2.Int64ID(s.seq.Add(1))
}

var idSeq jsonrpcID

// Request is a JSON-RPC request sent from client to server.
type Request struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"`

	Method string `json:"method"`
	// Arguments are the request arguments; nil for no-arg requests.
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Response is a JSON-RPC response from server to client.
type Response struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	RequestMsg *json.RawMessage `json:"request_msg,omitempty"`

	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Result   string          `json:"result,omitempty"`
}

// Event is a JSON-RPC event (no request_msg, no id).
// DAPEvent is a DAP event envelope.
type DAPEvent struct {
	Seq  int    `json:"seq"`
	Type string `json:"type"`

	Event string          `json:"event"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// --- DAP request types ---

// InitializeRequestArguments are passed to the initialize request.
type InitializeRequestArguments struct {
	ProtocolVersion int    `json:"protocolVersion"`
	AdapterID       string `json:"adapterID"`
	Locale          string `json:"locale,omitempty"`
	SupportsVariablePresentationInfo bool `json:"supportsVariablePresentationInfo,omitempty"`
}

// SetBreakpointsArguments passed to setBreakpoints.
type SetBreakpointsArguments struct {
	Sources    []Source            `json:"sources"`
	Breakpoints []BreakpointRequest `json:"breakpoints,omitempty"`
	SourceModified bool             `json:"sourceModified,omitempty"`
}

// BreakpointRequest is a breakpoint sent in setBreakpoints.
type BreakpointRequest struct {
	Line    int  `json:"line"`
	Column  int  `json:"column,omitempty"`
	Enabled bool `json:"enabled,omitempty"`
}

// ContinueArguments passed to continue request.
type ContinueArguments struct {
	ThreadID int `json:"threadId"`
}

// StepInArguments passed to stepIn request.
type StepInArguments struct {
	ThreadID   int  `json:"threadId"`
	TargetID   int  `json:"targetId,omitempty"`
	Granularity string `json:"granularity,omitempty"`
}

// StepOutArguments passed to stepOut request.
type StepOutArguments struct {
	ThreadID   int  `json:"threadId"`
	Granularity string `json:"granularity,omitempty"`
}

// StepOverArguments passed to stepOver request.
type StepOverArguments struct {
	ThreadID   int  `json:"threadId"`
	Granularity string `json:"granularity,omitempty"`
	TargetID   int  `json:"targetId,omitempty"`
}

// PauseArguments passed to pause request.
type PauseArguments struct {
	Reason   string `json:"reason,omitempty"`
	ThreadID int    `json:"threadId,omitempty"`
}

// StackTraceArguments passed to stackTrace request.
type StackTraceArguments struct {
	ThreadID int `json:"threadId"`
	StartFrame int `json:"startFrame,omitempty"`
	Levels   int   `json:"levels,omitempty"`
}

// ThreadsRequest has no arguments.

// ScopesArguments passed to scopes request.
type ScopesArguments struct {
	FrameID int `json:"frameId"`
}

// VariablesArguments passed to variables request.
type VariablesArguments struct {
	VariableID   int    `json:"variableId"`
	Filter       string `json:"filter,omitempty"`
	NamedVariables int  `json:"namedVariables,omitempty"`
	IndexedVariables int `json:"indexedVariables,omitempty"`
}

// EvaluateArguments passed to evaluate request.
type EvaluateArguments struct {
	Expression string `json:"expression"`
	FrameID    int    `json:"frameId,omitempty"`
	Context    string `json:"context,omitempty"`
}

// SetVariableArguments passed to setVariable request.
type SetVariableArguments struct {
	VariableID int    `json:"variableId"`
	Name       string `json:"name"`
	Value      string `json:"value"`
}

// SourceArguments passed to source request.
type SourceArguments struct {
	Source      Source `json:"source"`
	SourceReference int  `json:"sourceReference,omitempty"`
}

// LoadedSourcesArguments passed to loadedSources request.
type LoadedSourcesArguments struct {
	Filter string `json:"filter,omitempty"`
}

// ModulesArguments passed to modules request.
type ModulesArguments struct {
	StartModule int `json:"startModule,omitempty"`
	ModulesCount int `json:"modulesCount,omitempty"`
}

// RestartArguments passed to restart request.
type RestartArguments struct {
	Args []json.RawMessage `json:"args,omitempty"`
}

// --- DAP response types ---

// InitializeResponseResult is the result of the initialize request.
type InitializeResponseResult struct {
}

// Capabilities is the result of the initialize request (capabilities object).
type Capabilities struct {
	SupportsConfigurationDoneRequest       *bool `json:"supportsConfigurationDoneRequest,omitempty"`
	SupportsFunctionBreakpoints            *bool `json:"supportsFunctionBreakpoints,omitempty"`
	SupportsConditionalBreakpoints         *bool `json:"supportsConditionalBreakpoints,omitempty"`
	SupportsHitConditionalBreakpoints      *bool `json:"supportsHitConditionalBreakpoints,omitempty"`
	SupportsEvaluateForHovers              *bool `json:"supportsEvaluateForHovers,omitempty"`
	SupportsExceptionOptions             *bool `json:"supportsExceptionOptions,omitempty"`
	SupportsValueFormattingOptions       *bool `json:"supportsValueFormattingOptions,omitempty"`
	SupportsExceptionInfoRequest         *bool `json:"supportsExceptionInfoRequest,omitempty"`
	SupportSupportsDelayingExceptionBreakpoints *bool `json:"supportsDelayingExceptionBreakpoints,omitempty"`
	SupportsSetVariable                  *bool `json:"supportsSetVariable,omitempty"`
	SupportsTerminateThreadsRequest      *bool `json:"supportsTerminateThreadsRequest,omitempty"`
	SupportedFormats                       []SupportedFormat `json:"supportedFormats,omitempty"`
	SupportsRestartFrame                 *bool `json:"supportsRestartFrame,omitempty"`
	SupportsStepBack                     *bool `json:"supportsStepBack,omitempty"`
	SupportsSetExpression                *bool `json:"supportsSetExpression,omitempty"`
	SupportsTerminateRequest             *bool `json:"supportsTerminateRequest,omitempty"`
	SupportsDataBreakpoints              *bool `json:"supportsDataBreakpoints,omitempty"`
	SupportsLogPoints                    *bool `json:"supportsLogPoints,omitempty"`
	SupportsPausedBreakpoints            *bool `json:"supportsPausedBreakpoints,omitempty"`
	SupportsReadMemoryRequest            *bool `json:"supportsReadMemoryRequest,omitempty"`
	SupportsWriteMemoryRequest           *bool `json:"supportsWriteMemoryRequest,omitempty"`
	SupportsDisassembleRequest           *bool `json:"supportsDisassembleRequest,omitempty"`
	SupportsBreakpointLocationsRequest   *bool `json:"supportsBreakpointLocationsRequest,omitempty"`
	Response  `json:"-"`
}

// SetBreakpointsResponseResult is the result of setBreakpoints.
type SetBreakpointsResponseResult struct {
	Breakpoints []Breakpoint `json:"breakpoints"`
}

// StackFramesResponseResult is the result of stackTrace.
type StackFramesResponseResult struct {
	StackFrames []StackFrame `json:"stackFrames"`
}

// ThreadsResponseResult is the result of threads.
type ThreadsResponseResult struct {
	Threads []Thread `json:"threads"`
}

// ScopesResponseResult is the result of scopes.
type ScopesResponseResult struct {
	Scopes []Scope `json:"scopes"`
}

// VariablesResponseResult is the result of variables.
type VariablesResponseResult struct {
	Variables []Variable `json:"variables"`
}

// EvaluateResponseResult is the result of evaluate.
type EvaluateResponseResult struct {
	Result             string `json:"result"`
	VariablesReference int    `json:"variablesReference,omitempty"`
	NamedVariables     int    `json:"namedVariables,omitempty"`
	IndexedVariables   int    `json:"indexedVariables,omitempty"`
	Type               string `json:"type,omitempty"`
}

// LoadedSourcesResponseResult is the result of loadedSources.
type LoadedSourcesResponseResult struct {
	Sources []Source `json:"sources"`
}

// ModulesResponseResult is the result of modules.
type ModulesResponseResult struct {
	Modules []Module `json:"modules"`
	StartModule int `json:"startModule,omitempty"`
	TotalModules int `json:"totalModules,omitempty"`
}

// --- DAP domain types ---

// Source identifies a source location.
type Source struct {
	Name           string `json:"name,omitempty"`
	Path           string `json:"path,omitempty"`
	SourceReference int   `json:"sourceReference,omitempty"`
	PresentationHint string `json:"presentationHint,omitempty"`
	Origin         string `json:"origin,omitempty"`
	StartLine      int    `json:"startLine,omitempty"`
	StartColumn    int    `json:"startColumn,omitempty"`
	EndLine        int    `json:"endLine,omitempty"`
	EndColumn      int    `json:"endColumn,omitempty"`
	Inline         *bool  `json:"inline,omitempty"`
	InlineOnly     *bool  `json:"inlineOnly,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	AdaptorID      string `json:"adapterId,omitempty"`
}

// Breakpoint is the server's confirmation of a breakpoint.
type Breakpoint struct {
	ID           int    `json:"id,omitempty"`
	Verified     bool   `json:"verified"`
	Message      string `json:"message,omitempty"`
	Source       *Source `json:"source,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
	EndLine      int    `json:"endLine,omitempty"`
	EndColumn    int    `json:"endColumn,omitempty"`
	InstructionReference string `json:"instructionReference,omitempty"`
	Modifiable   bool   `json:"modifiable,omitempty"`
	Info         string `json:"info,omitempty"`
}

// StackFrame represents a frame in the call stack.
type StackFrame struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Source         *Source `json:"source,omitempty"`
	Line           int    `json:"line"`
	Column         int    `json:"column"`
	EndLine        int    `json:"endLine,omitempty"`
	EndColumn      int    `json:"endColumn,omitempty"`
	CanRestart     bool   `json:"canRestart,omitempty"`
	InstructionRef string `json:"instructionReference,omitempty"`
	PresentationHint string `json:"presentationHint,omitempty"`
}

// Thread represents a debug thread.
type Thread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Scope represents a variable scope (e.g., locals, arguments).
type Scope struct {
	Name            string `json:"name"`
	PresentationHint string `json:"presentationHint,omitempty"`
	Type            string `json:"type,omitempty"`
	VariablesRequest string `json:"variablesRequest,omitempty"`
	NamedVariables    int    `json:"namedVariables,omitempty"`
	IndexedVariables  int    `json:"indexedVariables,omitempty"`
	VariablesReference int   `json:"variablesReference"`
}

// Variable represents a variable in the debuggee.
type Variable struct {
	Name             string          `json:"name"`
	Value            string          `json:"value"`
	Type             string          `json:"type,omitempty"`
	VariablesReference int           `json:"variablesReference,omitempty"`
	NamedVariables   int             `json:"namedVariables,omitempty"`
	IndexedVariables int             `json:"indexedVariables,omitempty"`
	EvaluationID     string          `json:"evaluationId,omitempty"`
	PresentationHint *VariablePresentationHint `json:"presentationHint,omitempty"`
}

// VariablePresentationHint provides visual formatting info for a variable.
type VariablePresentationHint struct {
	Kind       string `json:"kind,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
	Type       string `json:"type,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// Module represents a loaded module/library.
type Module struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Path                string `json:"path,omitempty"`
	IsOptimized         bool   `json:"isOptimized,omitempty"`
	Version             string `json:"version,omitempty"`
	Info                string `json:"info,omitempty"`
	DebugSessionID      string `json:"debugSessionId,omitempty"`
}

// SupportedFormat describes a supported value formatting option.
type SupportedFormat struct {
	Format       string `json:"format"`
	Label        string `json:"label,omitempty"`
	Description  string `json:"description,omitempty"`
	Type         string `json:"type,omitempty"`
	Extensions   []SupportedFormat `json:"extensions,omitempty"`
}

// --- Stopped event body ---

// StoppedEventBody is the body of the stopped event.
type StoppedEventBody struct {
	Reason            string `json:"reason"`
	ThreadID          int    `json:"threadId,omitempty"`
	PreserveFocusHint   bool  `json:"preserveFocusHint,omitempty"`
	Text              string `json:"text,omitempty"`
	AllThreadsStopped bool   `json:"allThreadsStopped,omitempty"`
	HitBreakpointIDs  []int  `json:"hitBreakpointIds,omitempty"`
}

// ContinuedEventBody is the body of the continued event.
type ContinuedEventBody struct {
	ThreadID int `json:"threadId"`
	AllThreadsContinued bool `json:"allThreadsContinued,omitempty"`
}

