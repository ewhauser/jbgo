package trace

import (
	"sync"
	"time"
)

type Kind string

const SchemaVersion = "gbash.trace.v1"

const (
	EventCallExpanded Kind = "call.expanded"
	EventCommandStart Kind = "command.start"
	EventCommandExit  Kind = "command.exit"
	EventFileAccess   Kind = "file.access"
	EventFileMutation Kind = "file.mutation"
	EventPolicyDenied Kind = "policy.denied"
)

type Event struct {
	Schema      string
	SessionID   string
	ExecutionID string
	Kind        Kind
	At          time.Time
	Command     *CommandEvent
	File        *FileEvent
	Policy      *PolicyEvent
	Message     string
	Error       string
}

type CommandEvent struct {
	Name             string
	Argv             []string
	Dir              string
	ExitCode         int
	Builtin          bool
	Position         string
	Duration         time.Duration
	ResolvedName     string
	ResolvedPath     string
	ResolutionSource string
}

type FileEvent struct {
	Action   string
	Path     string
	FromPath string
	ToPath   string
}

type PolicyEvent struct {
	Subject          string
	Reason           string
	Action           string
	Path             string
	Command          string
	ExitCode         int
	ResolutionSource string
}

type Recorder interface {
	Record(*Event)
	Snapshot() []Event
}

type Option func(*Buffer)

type Buffer struct {
	mu     sync.Mutex
	events []Event
	schema string
	meta   Event
}

func NewBuffer(opts ...Option) *Buffer {
	buffer := &Buffer{
		schema: SchemaVersion,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(buffer)
		}
	}
	return buffer
}

func WithSchema(schema string) Option {
	return func(buffer *Buffer) {
		if schema != "" {
			buffer.schema = schema
		}
	}
}

func WithSessionID(sessionID string) Option {
	return func(buffer *Buffer) {
		buffer.meta.SessionID = sessionID
	}
}

func WithExecutionID(executionID string) Option {
	return func(buffer *Buffer) {
		buffer.meta.ExecutionID = executionID
	}
}

func (b *Buffer) Record(event *Event) {
	if event == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	recorded := *event
	if recorded.Schema == "" {
		recorded.Schema = b.schema
	}
	if recorded.SessionID == "" {
		recorded.SessionID = b.meta.SessionID
	}
	if recorded.ExecutionID == "" {
		recorded.ExecutionID = b.meta.ExecutionID
	}

	b.events = append(b.events, recorded)
}

func (b *Buffer) Snapshot() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]Event, len(b.events))
	copy(out, b.events)
	return out
}

type NopRecorder struct{}

func (NopRecorder) Record(*Event) {}

func (NopRecorder) Snapshot() []Event { return nil }
