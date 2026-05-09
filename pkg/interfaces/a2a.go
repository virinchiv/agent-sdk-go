package interfaces

import (
	"context"
	"encoding/json"
	"iter"

	"github.com/agenticenv/agent-sdk-go/internal/types"
)

//go:generate mockgen -destination=./mocks/mock_a2a.go -package=mocks github.com/agenticenv/agent-sdk-go/pkg/interfaces A2AClient,A2AStreamingClient,A2ATaskClient

// A2AClient is a client to one A2A agent server: discovery, skill invocation, and optional close.
// Implementations may wrap github.com/a2aproject/a2a-go/v2/a2aclient or other transports.
// A remote A2A agent is treated as a tool provider: ResolveCard/ListSkills correspond to
// ListTools in MCPClient, and SendMessage corresponds to CallTool.
type A2AClient interface {
	// Name identifies this connection for logging and tool prefixes (e.g. agent config key).
	Name() string
	// Ping checks that the agent server is reachable. The implementation should use a lightweight
	// check (e.g. fetching the agent card or a health endpoint) with a short-lived connection.
	Ping(ctx context.Context) error
	// ResolveCard fetches the AgentCard from the server's well-known endpoint.
	// It describes the agent's identity, capabilities, and supported input/output modes.
	ResolveCard(ctx context.Context) (A2AAgentCard, error)
	// ListSkills returns the skill specs derived from the agent's AgentCard.
	// Skills are the A2A equivalent of tools: each represents an invocable capability.
	ListSkills(ctx context.Context) ([]A2ASkillSpec, error)
	// SendMessage sends a message to the agent and returns the result.
	// The result is either a completed Message or a Task (when the agent handles the request asynchronously).
	SendMessage(ctx context.Context, req A2ASendMessageRequest) (A2ASendMessageResult, error)
	// Close releases the connection or session.
	Close() error
}

// A2AStreamingClient extends A2AClient with streaming message support.
// Use when the agent server supports the SendStreamingMessage A2A protocol method.
type A2AStreamingClient interface {
	A2AClient
	// SendStreamingMessage sends a message and returns an iterator over events streamed back by
	// the agent. Each event is either a message delta, a task status update, or an artifact update.
	// The caller must consume or break the iterator to release server-side resources.
	SendStreamingMessage(ctx context.Context, req A2ASendMessageRequest) (iter.Seq2[A2AStreamEvent, error], error)
}

// A2ATaskClient extends A2AClient with async task management.
// Use when the agent returns Task results that may need polling or cancellation.
type A2ATaskClient interface {
	A2AClient
	// GetTask retrieves the current state of an async task by its ID.
	GetTask(ctx context.Context, taskID string) (A2ATask, error)
	// CancelTask requests cancellation of an in-progress task.
	// The returned Task reflects the state after the cancellation request is acknowledged.
	CancelTask(ctx context.Context, taskID string) (A2ATask, error)
}

// ---------------------------------------------------------------------------
// Discovery types
// ---------------------------------------------------------------------------

// A2AAgentCard describes a remote A2A agent: its identity, reachability, and declared skills.
// Returned by ResolveCard; mirrors the agent card defined in the A2A protocol spec.
type A2AAgentCard struct {
	// Name is the human-readable agent name.
	Name string `json:"name"`
	// Description summarises what the agent does.
	Description string `json:"description,omitempty"`
	// Version is the agent's own version string.
	Version string `json:"version,omitempty"`
	// URL is the base endpoint of the agent server.
	URL string `json:"url"`
	// DocumentationURL is an optional link to agent documentation.
	DocumentationURL string `json:"documentationUrl,omitempty"`
	// Skills lists the capabilities the agent advertises.
	Skills []A2ASkillSpec `json:"skills,omitempty"`
	// InputModes lists MIME types or mode tokens the agent accepts (e.g. "text/plain", "application/json").
	InputModes []string `json:"defaultInputModes,omitempty"`
	// OutputModes lists MIME types or mode tokens the agent produces.
	OutputModes []string `json:"defaultOutputModes,omitempty"`
}

// A2ASkillSpec describes one invocable skill advertised by the agent.
// Used by the agent host to expose the remote skill as a Tool to the LLM.
// Canonical definition in [github.com/agenticenv/agent-sdk-go/internal/types].
type A2ASkillSpec = types.A2ASkillSpec

// ---------------------------------------------------------------------------
// Message types
// ---------------------------------------------------------------------------

// A2AMessage is a single turn message exchanged with the agent.
// Role is "user" when sending and "agent" (or "assistant") when receiving.
type A2AMessage struct {
	// MessageID is an optional client-assigned identifier for this message.
	MessageID string `json:"messageId,omitempty"`
	// Role is "user" or "agent".
	Role string `json:"role"`
	// Parts holds the content of the message as an ordered slice of heterogeneous parts.
	Parts []A2APart `json:"parts"`
	// Metadata holds optional protocol-level key/value annotations.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// A2APart is one content part of a message.
// Exactly one of Text, Data, or File will be populated depending on Kind.
type A2APart struct {
	// Kind discriminates the part type: "text", "data", or "file".
	Kind string `json:"kind"`
	// Text holds the content when Kind is "text".
	Text string `json:"text,omitempty"`
	// Data holds structured JSON content when Kind is "data".
	Data json.RawMessage `json:"data,omitempty"`
	// FileMIMEType is the MIME type hint when Kind is "file".
	FileMIMEType string `json:"fileMimeType,omitempty"`
	// FileBytes holds base64-encoded file content when Kind is "file".
	FileBytes string `json:"fileBytes,omitempty"`
	// FileURI holds a URI reference to file content when Kind is "file".
	FileURI string `json:"fileUri,omitempty"`
}

// ---------------------------------------------------------------------------
// Request / response types
// ---------------------------------------------------------------------------

// A2ASendMessageRequest is the input to SendMessage or SendStreamingMessage.
type A2ASendMessageRequest struct {
	// Message is the user-turn message to send.
	Message A2AMessage `json:"message"`
	// TaskID optionally resumes an existing async task.
	TaskID string `json:"taskId,omitempty"`
	// SessionID optionally groups related tasks into a logical session.
	SessionID string `json:"sessionId,omitempty"`
	// AcceptedOutputModes lists the output modes the caller can handle.
	// If empty, the agent uses its default output modes.
	AcceptedOutputModes []string `json:"acceptedOutputModes,omitempty"`
}

// A2ASendMessageResult is the output of a non-streaming SendMessage call.
// Either Message or Task is set depending on whether the agent handled the request
// synchronously (message response) or asynchronously (task).
type A2ASendMessageResult struct {
	// Message is set when the agent returned a complete message response inline.
	Message *A2AMessage `json:"message,omitempty"`
	// Task is set when the agent created or updated an async task.
	Task *A2ATask `json:"task,omitempty"`
}

// ---------------------------------------------------------------------------
// Task types
// ---------------------------------------------------------------------------

// A2ATask represents the state of a long-running async task on the agent server.
type A2ATask struct {
	// ID is the server-assigned task identifier.
	ID string `json:"id"`
	// SessionID is the optional session this task belongs to.
	SessionID string `json:"sessionId,omitempty"`
	// Status is the current lifecycle state of the task.
	Status A2ATaskStatus `json:"status"`
	// Message is the latest agent message associated with this task, if any.
	Message *A2AMessage `json:"message,omitempty"`
	// Artifacts holds any named outputs produced by the task.
	Artifacts []A2AArtifact `json:"artifacts,omitempty"`
	// Metadata holds optional server-supplied key/value annotations.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// A2ATaskStatus is the lifecycle state of an async task.
type A2ATaskStatus string

const (
	A2ATaskStatusSubmitted     A2ATaskStatus = "submitted"
	A2ATaskStatusWorking       A2ATaskStatus = "working"
	A2ATaskStatusInputRequired A2ATaskStatus = "input-required"
	A2ATaskStatusCompleted     A2ATaskStatus = "completed"
	A2ATaskStatusCanceled      A2ATaskStatus = "canceled"
	A2ATaskStatusFailed        A2ATaskStatus = "failed"
	A2ATaskStatusRejected      A2ATaskStatus = "rejected"
	A2ATaskStatusAuthRequired  A2ATaskStatus = "auth-required"
	A2ATaskStatusUnknown       A2ATaskStatus = "unknown"
)

// A2AArtifact is a named output produced by a task (e.g. a generated file or structured result).
type A2AArtifact struct {
	// ArtifactID is an optional server-assigned identifier.
	ArtifactID string `json:"artifactId,omitempty"`
	// Name is a human-readable label for the artifact.
	Name string `json:"name,omitempty"`
	// Parts holds the artifact content.
	Parts []A2APart `json:"parts,omitempty"`
	// Metadata holds optional key/value annotations.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ---------------------------------------------------------------------------
// Streaming event types
// ---------------------------------------------------------------------------

// A2AStreamEvent is one event yielded by SendStreamingMessage.
// Exactly one of Message, TaskStatus, or Artifact is populated depending on Kind.
type A2AStreamEvent struct {
	// Kind discriminates the event type: "message", "taskStatus", or "artifact".
	Kind string `json:"kind"`
	// TaskID is the server-assigned task ID; present on all event kinds.
	TaskID string `json:"taskId,omitempty"`
	// IsFinal is true on the last event for this stream.
	IsFinal bool `json:"final,omitempty"`
	// Message is set when Kind is "message".
	Message *A2AMessage `json:"message,omitempty"`
	// TaskStatus is set when Kind is "taskStatus".
	TaskStatus *A2ATaskStatusUpdate `json:"taskStatus,omitempty"`
	// Artifact is set when Kind is "artifact".
	Artifact *A2AArtifact `json:"artifact,omitempty"`
}

// A2ATaskStatusUpdate carries the new status and optional message in a task status event.
type A2ATaskStatusUpdate struct {
	// Status is the updated lifecycle state.
	Status A2ATaskStatus `json:"status"`
	// Message is an optional agent message accompanying the status change.
	Message *A2AMessage `json:"message,omitempty"`
}
