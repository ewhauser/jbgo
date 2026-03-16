package runtime

import (
	"context"
	"net/url"
	"strings"

	"github.com/ewhauser/gbash/trace"
)

const redactedValue = "[REDACTED]"

var sensitiveQueryKeys = map[string]struct{}{
	"access_token":         {},
	"api_key":              {},
	"apikey":               {},
	"auth":                 {},
	"authorization":        {},
	"bearer":               {},
	"credential":           {},
	"id_token":             {},
	"key":                  {},
	"password":             {},
	"passwd":               {},
	"refresh_token":        {},
	"secret":               {},
	"sig":                  {},
	"signature":            {},
	"token":                {},
	"x-amz-credential":     {},
	"x-amz-security-token": {},
	"x-amz-signature":      {},
	"x-goog-credential":    {},
	"x-goog-signature":     {},
	"x-ms-signature":       {},
}

func tracingEnabled(cfg TraceConfig) bool {
	return cfg.Mode != TraceOff
}

func newExecutionTraceRecorder(ctx context.Context, sessionID, executionID string, cfg TraceConfig, capture bool) (trace.Recorder, *trace.Buffer) {
	if !tracingEnabled(cfg) {
		return trace.NopRecorder{}, nil
	}

	recorders := make([]trace.Recorder, 0, 2)
	var buffer *trace.Buffer
	if capture {
		buffer = trace.NewBuffer()
		recorders = append(recorders, buffer)
	}
	if cfg.OnEvent != nil {
		recorders = append(recorders, traceCallbackRecorder{
			ctx: ctx,
			fn:  cfg.OnEvent,
		})
	}
	if len(recorders) == 0 {
		return trace.NopRecorder{}, nil
	}

	var next trace.Recorder
	if len(recorders) == 1 {
		next = recorders[0]
	} else {
		next = trace.NewFanout(recorders...)
	}

	return observabilityTraceRecorder{
		next:        next,
		mode:        cfg.Mode,
		sessionID:   sessionID,
		executionID: executionID,
	}, buffer
}

type traceCallbackRecorder struct {
	ctx context.Context //nolint:containedctx // intentional: recorder passes ctx to callback
	fn  func(context.Context, trace.Event)
}

func (r traceCallbackRecorder) Record(event *trace.Event) {
	if event == nil || r.fn == nil {
		return
	}
	cloned := cloneTraceEvent(event)
	safeTraceCallback(r.ctx, r.fn, &cloned)
}

func (traceCallbackRecorder) Snapshot() []trace.Event { return nil }

type observabilityTraceRecorder struct {
	next        trace.Recorder
	mode        TraceMode
	sessionID   string
	executionID string
}

func (r observabilityTraceRecorder) Record(event *trace.Event) {
	if event == nil || r.next == nil {
		return
	}

	prepared := cloneTraceEvent(event)
	if prepared.Schema == "" {
		prepared.Schema = trace.SchemaVersion
	}
	if prepared.SessionID == "" {
		prepared.SessionID = r.sessionID
	}
	if prepared.ExecutionID == "" {
		prepared.ExecutionID = r.executionID
	}
	if r.mode == TraceRedacted {
		prepared.Redacted = redactTraceEvent(&prepared) || prepared.Redacted
	}

	r.next.Record(&prepared)
}

func (r observabilityTraceRecorder) Snapshot() []trace.Event {
	if r.next == nil {
		return nil
	}
	return r.next.Snapshot()
}

func safeTraceCallback(ctx context.Context, fn func(context.Context, trace.Event), event *trace.Event) {
	if event == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(ctx, *event)
}

func safeLogCallback(ctx context.Context, fn LogCallback, event *LogEvent) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	if event != nil {
		fn(ctx, *event)
	}
}

func logExecutionEvent(ctx context.Context, fn LogCallback, event *LogEvent) {
	if fn == nil {
		return
	}
	safeLogCallback(ctx, fn, event)
}

func logExecutionOutputs(ctx context.Context, fn LogCallback, base *LogEvent, result *ExecutionResult) {
	if fn == nil || base == nil || result == nil {
		return
	}

	if result.Stdout != "" {
		event := *base
		event.Kind = LogStdout
		event.Output = result.Stdout
		event.Truncated = result.StdoutTruncated
		logExecutionEvent(ctx, fn, &event)
	}

	if result.Stderr != "" {
		event := *base
		event.Kind = LogStderr
		event.Output = result.Stderr
		event.Truncated = result.StderrTruncated
		logExecutionEvent(ctx, fn, &event)
	}
}

func logExecutionCompletion(ctx context.Context, fn LogCallback, base *LogEvent, result *ExecutionResult, err error, unexpected bool) {
	if fn == nil || base == nil || result == nil {
		return
	}

	event := *base
	event.ExitCode = result.ExitCode
	event.Duration = result.Duration
	event.ShellExited = result.ShellExited
	if unexpected && err != nil {
		event.Kind = LogExecError
		event.Error = err.Error()
		logExecutionEvent(ctx, fn, &event)
		return
	}

	event.Kind = LogExecFinish
	logExecutionEvent(ctx, fn, &event)
}

type layoutMutationRecorder struct {
	layout *sandboxLayoutState
}

func (r layoutMutationRecorder) Record(event *trace.Event) {
	if event == nil || event.Kind != trace.EventFileMutation || event.File == nil || r.layout == nil {
		return
	}
	r.layout.observeFileMutation(event.File)
}

func (layoutMutationRecorder) Snapshot() []trace.Event { return nil }

func redactTraceEvent(event *trace.Event) bool {
	if event == nil || event.Command == nil || len(event.Command.Argv) == 0 {
		return false
	}

	argv, changed := redactArgv(event.Command.Argv)
	if changed {
		event.Command.Argv = argv
	}
	return changed
}

func redactArgv(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}

	out := cloneStrings(argv)
	changed := false
	for i := range out {
		arg := out[i]

		if i > 0 {
			if redacted, ok := redactFlagValue(out[i-1], arg); ok {
				if redacted != arg {
					out[i] = redacted
					changed = true
				}
				continue
			}
		}

		if redacted, ok := redactInlineFlagValue(arg); ok {
			if redacted != arg {
				out[i] = redacted
				changed = true
			}
			continue
		}

		if redacted, ok := redactStandaloneArg(arg); ok {
			if redacted != arg {
				out[i] = redacted
				changed = true
			}
		}
	}

	return out, changed
}

func redactFlagValue(flag, value string) (string, bool) {
	switch flag {
	case "-H", "--header", "--proxy-header":
		return redactHeaderValue(value)
	case "-u", "--user", "--oauth2-bearer", "--password", "--passwd", "--token", "--access-token":
		return redactedValue, true
	default:
		return redactStandaloneArg(value)
	}
}

func redactInlineFlagValue(arg string) (string, bool) {
	prefix, value, ok := strings.Cut(arg, "=")
	if !ok || !strings.HasPrefix(prefix, "--") {
		return "", false
	}

	if redacted, changed := redactFlagValue(prefix, value); changed {
		return prefix + "=" + redacted, true
	}
	return "", false
}

func redactStandaloneArg(arg string) (string, bool) {
	if redacted, changed := redactHeaderValue(arg); changed {
		return redacted, true
	}
	if redacted, changed := redactURLValue(arg); changed {
		return redacted, true
	}
	if redacted, changed := redactBearerValue(arg); changed {
		return redacted, true
	}
	return "", false
}

func redactHeaderValue(value string) (string, bool) {
	name, headerValue, ok := strings.Cut(value, ":")
	if !ok {
		return "", false
	}

	normalizedName := strings.ToLower(strings.TrimSpace(name))
	if !isSensitiveHeaderName(normalizedName) && !looksSensitiveHeaderValue(headerValue) {
		return "", false
	}
	return strings.TrimSpace(name) + ": " + redactedValue, true
}

func isSensitiveHeaderName(name string) bool {
	switch name {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-auth-token":
		return true
	default:
		return false
	}
}

func looksSensitiveHeaderValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "bearer ") ||
		strings.HasPrefix(normalized, "basic ") ||
		strings.HasPrefix(normalized, "digest ") ||
		strings.HasPrefix(normalized, "token ")
}

func redactBearerValue(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(strings.ToLower(trimmed), "bearer "):
		return "Bearer " + redactedValue, true
	case strings.HasPrefix(strings.ToLower(trimmed), "basic "):
		return "Basic " + redactedValue, true
	default:
		return "", false
	}
}

func redactURLValue(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		return "", false
	}
	if parsed.RawQuery == "" {
		return "", false
	}

	redactedQuery, changed := redactRawQuery(parsed.RawQuery)
	if !changed {
		return "", false
	}

	parsed.RawQuery = redactedQuery
	return parsed.String(), true
}

func redactRawQuery(raw string) (string, bool) {
	parts := strings.Split(raw, "&")
	changed := false
	for i, part := range parts {
		if part == "" {
			continue
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		if !isSensitiveQueryKey(decodedKey) {
			continue
		}

		if value != redactedValue {
			parts[i] = key + "=" + redactedValue
			changed = true
		}
	}

	return strings.Join(parts, "&"), changed
}

func isSensitiveQueryKey(key string) bool {
	_, ok := sensitiveQueryKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}
