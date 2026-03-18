package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

func TestTraceOffDoesNotAllocateRecorder(t *testing.T) {
	t.Parallel()
	recorder, buffer := newExecutionTraceRecorder(context.Background(), "sess-1", "exec-1", TraceConfig{}, true)
	if buffer != nil {
		t.Fatalf("buffer = %#v, want nil", buffer)
	}
	if _, ok := recorder.(trace.NopRecorder); !ok {
		t.Fatalf("recorder = %T, want trace.NopRecorder", recorder)
	}
}

func TestExecutionResultsOmitTraceEventsByDefault(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "echo hi\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want empty", result.Events)
	}
}

func TestTraceOffDoesNotInvokeCallbacks(t *testing.T) {
	t.Parallel()
	callbackCalled := false
	rt := newRuntime(t, &Config{
		Tracing: TraceConfig{
			OnEvent: func(context.Context, trace.Event) {
				callbackCalled = true
			},
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "echo hi\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want empty", result.Events)
	}
	if callbackCalled {
		t.Fatal("trace callback should not run when tracing is off")
	}
}

func TestTraceRedactedPopulatesEventsAndCallbacks(t *testing.T) {
	t.Parallel()
	var callbackEvents []trace.Event
	session := newSession(t, &Config{
		Tracing: TraceConfig{
			Mode: TraceRedacted,
			OnEvent: func(_ context.Context, event trace.Event) {
				callbackEvents = append(callbackEvents, event)
			},
		},
	})

	result := mustExecSession(t, session, "echo -H 'Authorization: Bearer secret-token' '--header=Authorization: Bearer inline-secret' 'https://example.test/download?token=query-secret&ok=1' 'Bearer literal-secret'\n")
	if len(result.Events) == 0 {
		t.Fatal("expected trace events in result")
	}
	if got, want := len(callbackEvents), len(result.Events); got != want {
		t.Fatalf("callback event count = %d, want %d", got, want)
	}

	event := findCommandEvent(result.Events)
	if event == nil || event.Command == nil {
		t.Fatalf("missing redacted command event: %#v", result.Events)
	}
	if !event.Redacted {
		t.Fatalf("event.Redacted = %t, want true", event.Redacted)
	}
	if got, want := event.Command.Argv, []string{
		"echo",
		"-H",
		"Authorization: [REDACTED]",
		"--header=Authorization: [REDACTED]",
		"https://example.test/download?token=[REDACTED]&ok=1",
		"Bearer [REDACTED]",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted argv = %#v, want %#v", got, want)
	}

	callbackEvent := findCommandEvent(callbackEvents)
	if callbackEvent == nil || callbackEvent.Command == nil {
		t.Fatalf("missing callback command event: %#v", callbackEvents)
	}
	if !callbackEvent.Redacted {
		t.Fatalf("callback event.Redacted = %t, want true", callbackEvent.Redacted)
	}
	if !reflect.DeepEqual(callbackEvent.Command.Argv, event.Command.Argv) {
		t.Fatalf("callback argv = %#v, want %#v", callbackEvent.Command.Argv, event.Command.Argv)
	}
}

func TestTraceRawPreservesSensitiveArgv(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Tracing: TraceConfig{Mode: TraceRaw},
	})

	result := mustExecSession(t, session, "echo -H 'Authorization: Bearer secret-token' '--header=Authorization: Bearer inline-secret' 'https://example.test/download?token=query-secret&ok=1' 'Bearer literal-secret'\n")
	event := findCommandEvent(result.Events)
	if event == nil || event.Command == nil {
		t.Fatalf("missing raw command event: %#v", result.Events)
	}
	if event.Redacted {
		t.Fatalf("event.Redacted = %t, want false", event.Redacted)
	}
	if got, want := event.Command.Argv, []string{
		"echo",
		"-H",
		"Authorization: Bearer secret-token",
		"--header=Authorization: Bearer inline-secret",
		"https://example.test/download?token=query-secret&ok=1",
		"Bearer literal-secret",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("raw argv = %#v, want %#v", got, want)
	}
}

func TestTraceRedactedLeavesResultsFinalEnvAndLoggerOutputsRaw(t *testing.T) {
	t.Parallel()
	var (
		callbackEvents []trace.Event
		logEvents      []LogEvent
	)
	rt := newRuntime(t, &Config{
		Tracing: TraceConfig{
			Mode: TraceRedacted,
			OnEvent: func(_ context.Context, event trace.Event) {
				callbackEvents = append(callbackEvents, event)
			},
		},
		Logger: func(_ context.Context, event LogEvent) {
			logEvents = append(logEvents, event)
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"export API_TOKEN=env-secret\n" +
			"printf 'stdout:%s\\n' \"$API_TOKEN\"\n" +
			"printf 'stderr:%s\\n' \"$API_TOKEN\" >&2\n" +
			"echo -H 'Authorization: Bearer argv-secret' >/dev/null\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := result.Stdout, "stdout:env-secret\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "stderr:env-secret\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
	if got, want := result.FinalEnv["API_TOKEN"], "env-secret"; got != want {
		t.Fatalf("FinalEnv[API_TOKEN] = %q, want %q", got, want)
	}

	event := findCommandEvent(callbackEvents)
	if event == nil || event.Command == nil {
		t.Fatalf("missing callback echo event: %#v", callbackEvents)
	}
	if !event.Redacted {
		t.Fatalf("event.Redacted = %t, want true", event.Redacted)
	}
	if got, want := event.Command.Argv, []string{"echo", "-H", "Authorization: [REDACTED]"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted argv = %#v, want %#v", got, want)
	}

	stdoutEvent := findLogEvent(logEvents, LogStdout)
	if stdoutEvent == nil {
		t.Fatalf("missing stdout log event: %#v", logEvents)
	}
	if got, want := stdoutEvent.Output, "stdout:env-secret\n"; got != want {
		t.Fatalf("stdout log output = %q, want %q", got, want)
	}

	stderrEvent := findLogEvent(logEvents, LogStderr)
	if stderrEvent == nil {
		t.Fatalf("missing stderr log event: %#v", logEvents)
	}
	if got, want := stderrEvent.Output, "stderr:env-secret\n"; got != want {
		t.Fatalf("stderr log output = %q, want %q", got, want)
	}
}

func TestNestedExecTracingInvokesInheritedCallbacks(t *testing.T) {
	t.Parallel()
	var executionIDs []string
	rt := newRuntime(t, &Config{
		Registry: registryWithSubexecProbe(t),
		Tracing: TraceConfig{
			Mode: TraceRaw,
			OnEvent: func(_ context.Context, event trace.Event) {
				executionIDs = append(executionIDs, event.ExecutionID)
			},
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p work\n echo note > work/note.txt\n cd work\n FOO=bar subexecprobe inherit\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Events) == 0 {
		t.Fatal("expected outer execution events")
	}

	outerIDs := collectExecutionIDs(nil, result.Events)
	if got, want := len(outerIDs), 1; got != want {
		t.Fatalf("outer execution IDs = %v, want %d ID", outerIDs, want)
	}

	callbackIDs := collectExecutionIDs(executionIDs, nil)
	if len(callbackIDs) < 2 {
		t.Fatalf("callback execution IDs = %v, want nested execution IDs", callbackIDs)
	}
}

func TestInteractiveTracingUsesCallbacksOnly(t *testing.T) {
	t.Parallel()
	callbackCount := 0
	session := newSession(t, &Config{
		Tracing: TraceConfig{
			Mode: TraceRaw,
			OnEvent: func(context.Context, trace.Event) {
				callbackCount++
			},
		},
	})

	var stdout strings.Builder
	result, err := session.Interact(context.Background(), &InteractiveRequest{
		Stdin:  strings.NewReader("echo hi\nexit 3\n"),
		Stdout: &stdout,
	})
	if err != nil {
		t.Fatalf("Interact() error = %v", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", result.ExitCode)
	}
	if callbackCount == 0 {
		t.Fatal("expected interactive trace callbacks")
	}
}

func TestExecutionLoggerReportsLifecycleAndOutput(t *testing.T) {
	t.Parallel()
	var events []LogEvent
	rt := newRuntime(t, &Config{
		Logger: func(_ context.Context, event LogEvent) {
			events = append(events, event)
		},
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
			Limits: policy.Limits{
				MaxStdoutBytes: 4,
				MaxStderrBytes: 2,
				MaxFileBytes:   8 << 20,
			},
		}),
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf hello\nprintf err >&2\nexit 7\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}

	if got, want := logKinds(events), []LogKind{LogExecStart, LogStdout, LogStderr, LogExecFinish}; !reflect.DeepEqual(got, want) {
		t.Fatalf("log kinds = %#v, want %#v", got, want)
	}
	if events[0].ExecutionID == "" || events[0].SessionID == "" {
		t.Fatalf("start event missing IDs: %#v", events[0])
	}
	if got, want := events[1].Output, result.Stdout; got != want {
		t.Fatalf("stdout log output = %q, want %q", got, want)
	}
	if got, want := events[1].Truncated, true; got != want {
		t.Fatalf("stdout truncated = %t, want %t", got, want)
	}
	if got, want := events[2].Output, result.Stderr; got != want {
		t.Fatalf("stderr log output = %q, want %q", got, want)
	}
	if got, want := events[2].Truncated, true; got != want {
		t.Fatalf("stderr truncated = %t, want %t", got, want)
	}
	if got, want := events[3].ExitCode, result.ExitCode; got != want {
		t.Fatalf("finish exit code = %d, want %d", got, want)
	}
}

func TestExecutionLoggerReportsUnexpectedErrors(t *testing.T) {
	t.Parallel()
	var events []LogEvent
	rt := newRuntime(t, &Config{
		Registry: registryWithCommands(t, commands.DefineCommand("boom", func(context.Context, *commands.Invocation) error {
			return errors.New("engine boom")
		})),
		Logger: func(_ context.Context, event LogEvent) {
			events = append(events, event)
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "boom\n"})
	if err == nil {
		t.Fatal("Run() error = nil, want engine error")
	}
	if result == nil {
		t.Fatal("Run() result = nil")
	}
	if got, want := logKinds(events), []LogKind{LogExecStart, LogExecError}; !reflect.DeepEqual(got, want) {
		t.Fatalf("log kinds = %#v, want %#v", got, want)
	}
	if got, want := events[1].Error, "engine boom"; got != want {
		t.Fatalf("error log message = %q, want %q", got, want)
	}
}

func TestObservabilityCallbackPanicsAreRecovered(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{
		Tracing: TraceConfig{
			Mode: TraceRaw,
			OnEvent: func(context.Context, trace.Event) {
				panic("trace panic")
			},
		},
		Logger: func(context.Context, LogEvent) {
			panic("log panic")
		},
	})

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "echo hi\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if len(result.Events) == 0 {
		t.Fatal("expected trace events despite callback panic")
	}
}

func collectExecutionIDs(ids []string, events []trace.Event) []string {
	seen := make(map[string]struct{}, len(ids)+len(events))
	out := make([]string, 0, len(ids)+len(events))
	for _, id := range ids {
		if _, ok := seen[id]; ok || id == "" {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for i := range events {
		id := events[i].ExecutionID
		if _, ok := seen[id]; ok || id == "" {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func logKinds(events []LogEvent) []LogKind {
	out := make([]LogKind, 0, len(events))
	for i := range events {
		out = append(out, events[i].Kind)
	}
	return out
}

func findLogEvent(events []LogEvent, kind LogKind) *LogEvent {
	for i := range events {
		if events[i].Kind == kind {
			return &events[i]
		}
	}
	return nil
}
