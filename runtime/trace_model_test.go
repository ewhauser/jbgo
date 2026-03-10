package runtime

import (
	"context"
	"testing"

	"github.com/cadencerpm/just-bash-go/policy"
	"github.com/cadencerpm/just-bash-go/trace"
)

func TestTraceEventsIncludeSchemaSessionAndExecutionIDs(t *testing.T) {
	session := newSession(t, &Config{})

	first := mustExecSession(t, session, "echo first\n")
	second := mustExecSession(t, session, "echo second\n")

	assertTraceMetadata(t, first.Events, trace.SchemaVersion, "")
	assertTraceMetadata(t, second.Events, trace.SchemaVersion, first.Events[0].SessionID)

	if first.Events[0].ExecutionID == second.Events[0].ExecutionID {
		t.Fatalf("ExecutionID should differ across execs: %q", first.Events[0].ExecutionID)
	}
}

func TestTraceRecordsCommandResolutionSources(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("payload\n"))

	result := mustExecSession(t, session, "echo hi\ncat note.txt\n/bin/cat note.txt\n")

	echoEvent := findCommandEvent(result.Events, trace.EventCallExpanded, "echo")
	if echoEvent == nil || echoEvent.Command == nil {
		t.Fatalf("missing echo call event: %#v", result.Events)
	}
	if got, want := echoEvent.Command.ResolutionSource, "builtin"; got != want {
		t.Fatalf("echo ResolutionSource = %q, want %q", got, want)
	}

	startEvents := findCommandEvents(result.Events, trace.EventCommandStart, "cat")
	if len(startEvents) != 2 {
		t.Fatalf("command.start cat events = %d, want 2", len(startEvents))
	}

	if got, want := startEvents[0].Command.ResolutionSource, "path-search"; got != want {
		t.Fatalf("bare cat ResolutionSource = %q, want %q", got, want)
	}
	if got, want := startEvents[0].Command.ResolvedPath, "/usr/bin/cat"; got != want {
		t.Fatalf("bare cat ResolvedPath = %q, want %q", got, want)
	}
	if got, want := startEvents[1].Command.ResolutionSource, "path"; got != want {
		t.Fatalf("path cat ResolutionSource = %q, want %q", got, want)
	}
	if got, want := startEvents[1].Command.ResolvedPath, "/bin/cat"; got != want {
		t.Fatalf("path cat ResolvedPath = %q, want %q", got, want)
	}
}

func TestTraceRecordsCommandAndPathPolicyDenials(t *testing.T) {
	commandDenied := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"echo"},
			ReadRoots:       []string{"/"},
			WriteRoots:      []string{"/"},
			Limits: policy.Limits{
				MaxStdoutBytes: 1 << 20,
				MaxStderrBytes: 1 << 20,
				MaxFileBytes:   8 << 20,
			},
			NetworkMode: policy.NetworkDisabled,
			SymlinkMode: policy.SymlinkDeny,
		}),
	})

	commandResult, err := commandDenied.Run(context.Background(), &ExecutionRequest{
		Script: "cat missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run(command denial) error = %v", err)
	}
	commandEvent := findPolicyEvent(commandResult.Events, "cat", "")
	if commandEvent == nil || commandEvent.Policy == nil {
		t.Fatalf("missing command policy denial event: %#v", commandResult.Events)
	}
	if got, want := commandEvent.Policy.ResolutionSource, "path-search"; got != want {
		t.Fatalf("command ResolutionSource = %q, want %q", got, want)
	}

	pathDenied := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			AllowedCommands: []string{"cat"},
			ReadRoots:       []string{"/allowed", "/usr/bin", "/bin"},
			WriteRoots:      []string{"/"},
			Limits: policy.Limits{
				MaxStdoutBytes: 1 << 20,
				MaxStderrBytes: 1 << 20,
				MaxFileBytes:   8 << 20,
			},
			NetworkMode: policy.NetworkDisabled,
			SymlinkMode: policy.SymlinkDeny,
		}),
	})

	pathSession, err := pathDenied.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession(path denial) error = %v", err)
	}
	writeSessionFile(t, pathSession, "/denied.txt", []byte("secret\n"))

	pathResult := mustExecSession(t, pathSession, "cat /denied.txt\n")
	pathEvent := findPolicyEvent(pathResult.Events, "", "/denied.txt")
	if pathEvent == nil || pathEvent.Policy == nil {
		t.Fatalf("missing path policy denial event: %#v", pathResult.Events)
	}
	if got, want := pathEvent.Policy.Action, string(policy.FileActionRead); got != want {
		t.Fatalf("path Action = %q, want %q", got, want)
	}
}

func TestTraceRecordsFileMutationEvents(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"mkdir -p work\n"+
		"echo hi > work/source.txt\n"+
		"cp work/source.txt work/copy.txt\n"+
		"mv work/copy.txt work/final.txt\n"+
		"rm work/source.txt\n")

	assertFileMutation(t, result.Events, "mkdir", "/home/agent/work", "", "")
	assertFileMutation(t, result.Events, "write", "/home/agent/work/source.txt", "", "")
	assertFileMutation(t, result.Events, "copy", "/home/agent/work/copy.txt", "/home/agent/work/source.txt", "/home/agent/work/copy.txt")
	assertFileMutation(t, result.Events, "rename", "/home/agent/work/final.txt", "/home/agent/work/copy.txt", "/home/agent/work/final.txt")
	assertFileMutation(t, result.Events, "remove", "/home/agent/work/source.txt", "/home/agent/work/source.txt", "")
}

func assertTraceMetadata(t *testing.T, events []trace.Event, schema, sessionID string) {
	t.Helper()

	if len(events) == 0 {
		t.Fatal("expected trace events")
	}
	for i := range events {
		event := events[i]
		if got, want := event.Schema, schema; got != want {
			t.Fatalf("event schema = %q, want %q", got, want)
		}
		if event.SessionID == "" {
			t.Fatal("event SessionID should not be empty")
		}
		if event.ExecutionID == "" {
			t.Fatal("event ExecutionID should not be empty")
		}
		if sessionID != "" && event.SessionID != sessionID {
			t.Fatalf("event SessionID = %q, want %q", event.SessionID, sessionID)
		}
	}
}

func findCommandEvent(events []trace.Event, kind trace.Kind, name string) *trace.Event {
	for i := range events {
		if events[i].Kind == kind && events[i].Command != nil && events[i].Command.Name == name {
			return &events[i]
		}
	}
	return nil
}

func findCommandEvents(events []trace.Event, kind trace.Kind, resolvedName string) []trace.Event {
	out := make([]trace.Event, 0, 2)
	for i := range events {
		event := events[i]
		if event.Kind != kind || event.Command == nil {
			continue
		}
		if event.Command.ResolvedName == resolvedName {
			out = append(out, event)
		}
	}
	return out
}

func findPolicyEvent(events []trace.Event, command, path string) *trace.Event {
	for i := range events {
		if events[i].Kind != trace.EventPolicyDenied || events[i].Policy == nil {
			continue
		}
		if command != "" && events[i].Policy.Command != command {
			continue
		}
		if path != "" && events[i].Policy.Path != path {
			continue
		}
		return &events[i]
	}
	return nil
}

func assertFileMutation(t *testing.T, events []trace.Event, action, path, fromPath, toPath string) {
	t.Helper()

	for i := range events {
		event := events[i]
		if event.Kind != trace.EventFileMutation || event.File == nil {
			continue
		}
		if event.File.Action == action && event.File.Path == path && event.File.FromPath == fromPath && event.File.ToPath == toPath {
			return
		}
	}

	t.Fatalf("missing file mutation action=%q path=%q from=%q to=%q in %#v", action, path, fromPath, toPath, events)
}
