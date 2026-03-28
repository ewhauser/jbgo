package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash"
)

var (
	errInvalidArgument = errors.New("invalid argument")
	errSessionNotFound = errors.New("session not found")
	errSessionBusy     = errors.New("session is busy")
)

type helloResult struct {
	ServerName    string            `json:"server_name"`
	ServerVersion string            `json:"server_version"`
	Protocol      string            `json:"protocol"`
	Capabilities  helloCapabilities `json:"capabilities"`
}

type helloCapabilities struct {
	Binary             string `json:"binary"`
	Transport          string `json:"transport"`
	PersistentSessions bool   `json:"persistent_sessions"`
	SessionExec        bool   `json:"session_exec"`
	FileSystemRPC      bool   `json:"filesystem_rpc"`
	InteractiveShell   bool   `json:"interactive_shell"`
}

type sessionSummary struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type sessionCreateResult struct {
	Session sessionSummary `json:"session"`
}

type sessionGetResult struct {
	Session sessionSummary `json:"session"`
}

type sessionListResult struct {
	Sessions []sessionSummary `json:"sessions"`
}

type sessionIDParams struct {
	SessionID string `json:"session_id"`
}

type sessionExecParams struct {
	SessionID      string             `json:"session_id"`
	Name           string             `json:"name"`
	Script         string             `json:"script"`
	ShellVariant   gbash.ShellVariant `json:"shell_variant"`
	Args           []string           `json:"args"`
	StartupOptions []string           `json:"startup_options"`
	Env            map[string]string  `json:"env"`
	WorkDir        string             `json:"work_dir"`
	ReplaceEnv     bool               `json:"replace_env"`
	TimeoutMs      int64              `json:"timeout_ms"`
}

type sessionExecResult struct {
	SessionID       string            `json:"session_id"`
	ExitCode        int               `json:"exit_code"`
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
	StdoutTruncated bool              `json:"stdout_truncated"`
	StderrTruncated bool              `json:"stderr_truncated"`
	FinalEnv        map[string]string `json:"final_env,omitempty"`
	ShellExited     bool              `json:"shell_exited"`
	ControlStderr   string            `json:"control_stderr,omitempty"`
	StartedAt       string            `json:"started_at,omitempty"`
	FinishedAt      string            `json:"finished_at,omitempty"`
	DurationMs      float64           `json:"duration_ms"`
	Session         sessionSummary    `json:"session"`
}

type serverSession struct {
	server *serverState
	id     string
	shell  *gbash.Session

	createdAt time.Time
	updatedAt time.Time
	idleSince time.Time

	mu   sync.Mutex
	busy bool
}

func (s *serverState) createSession() (*serverSession, error) {
	s.reapExpiredSessions(time.Now().UTC())

	sessionShell, err := s.cfg.Runtime.NewSession(s.ctx)
	if err != nil {
		return nil, fmt.Errorf("create gbash session: %w", err)
	}

	now := time.Now().UTC()
	session := &serverSession{
		server:    s,
		id:        s.nextIDValue("sess"),
		shell:     sessionShell,
		createdAt: now,
		updatedAt: now,
		idleSince: now,
	}

	s.sessionsMu.Lock()
	s.sessions[session.id] = session
	s.sessionsMu.Unlock()
	return session, nil
}

func (s *serverState) lookupSession(id string) (*serverSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: session_id must not be empty", errInvalidArgument)
	}

	s.reapExpiredSessions(time.Now().UTC())

	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	return session, nil
}

func (s *serverState) listSessions() []sessionSummary {
	s.sessionsMu.Lock()
	sessions := make([]*serverSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}
	s.sessionsMu.Unlock()

	out := make([]sessionSummary, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, session.summary())
	}
	slices.SortFunc(out, func(a, b sessionSummary) int {
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return out
}

func (s *serverState) destroySession(id string) (*serverSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("%w: session_id must not be empty", errInvalidArgument)
	}

	s.sessionsMu.Lock()
	session, ok := s.sessions[id]
	if !ok {
		s.sessionsMu.Unlock()
		return nil, fmt.Errorf("%w: %s", errSessionNotFound, id)
	}
	if session.isBusy() {
		s.sessionsMu.Unlock()
		return nil, fmt.Errorf("%w: %s", errSessionBusy, id)
	}
	delete(s.sessions, id)
	s.sessionsMu.Unlock()

	return session, nil
}

func (s *serverState) reapExpiredSessions(now time.Time) {
	s.sessionsMu.Lock()
	for id, session := range s.sessions {
		if session.expired(now) {
			delete(s.sessions, id)
		}
	}
	s.sessionsMu.Unlock()
}

func (s *serverSession) summary() sessionSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryLocked()
}

func (s *serverSession) summaryLocked() sessionSummary {
	summary := sessionSummary{
		SessionID: s.id,
		State:     "idle",
		CreatedAt: formatTime(s.createdAt),
		UpdatedAt: formatTime(s.updatedAt),
	}
	if s.busy {
		summary.State = "running"
		return summary
	}
	if !s.idleSince.IsZero() {
		summary.ExpiresAt = formatTime(s.idleSince.Add(s.server.cfg.SessionTTL))
	}
	return summary
}

func (s *serverSession) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

func (s *serverSession) expired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy || s.idleSince.IsZero() || s.server.cfg.SessionTTL <= 0 {
		return false
	}
	return !now.Before(s.idleSince.Add(s.server.cfg.SessionTTL))
}

func (s *serverSession) exec(ctx context.Context, params *sessionExecParams) (*sessionExecResult, error) {
	if params == nil {
		params = &sessionExecParams{}
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", errSessionBusy, s.id)
	}
	started := time.Now().UTC()
	s.busy = true
	s.updatedAt = started
	s.idleSince = time.Time{}
	s.mu.Unlock()

	result, err := s.shell.Exec(ctx, &gbash.ExecutionRequest{
		Name:           params.Name,
		Script:         params.Script,
		ShellVariant:   params.ShellVariant,
		Args:           append([]string(nil), params.Args...),
		StartupOptions: append([]string(nil), params.StartupOptions...),
		Env:            copyStringMap(params.Env),
		WorkDir:        params.WorkDir,
		Timeout:        durationFromMillis(params.TimeoutMs),
		ReplaceEnv:     params.ReplaceEnv,
	})

	finished := time.Now().UTC()

	s.mu.Lock()
	s.busy = false
	s.updatedAt = finished
	s.idleSince = finished
	summary := s.summaryLocked()
	s.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("execute session script: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("execute session script: runtime returned no result")
	}

	return &sessionExecResult{
		SessionID:       s.id,
		ExitCode:        result.ExitCode,
		Stdout:          result.Stdout,
		Stderr:          result.Stderr,
		StdoutTruncated: result.StdoutTruncated,
		StderrTruncated: result.StderrTruncated,
		FinalEnv:        copyStringMap(result.FinalEnv),
		ShellExited:     result.ShellExited,
		ControlStderr:   strings.TrimSpace(result.ControlStderr),
		StartedAt:       formatTime(result.StartedAt),
		FinishedAt:      formatTime(result.FinishedAt),
		DurationMs:      durationMilliseconds(result.Duration),
		Session:         summary,
	}, nil
}

func durationFromMillis(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func durationMilliseconds(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func formatTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
}
