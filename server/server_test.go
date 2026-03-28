package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/builtins"
	gbserver "github.com/ewhauser/gbash/server"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *rpcErrorData `json:"data"`
}

type rpcErrorData struct {
	Code    string `json:"code"`
	Details string `json:"details"`
}

type helloResult struct {
	ServerName    string `json:"server_name"`
	ServerVersion string `json:"server_version"`
	Protocol      string `json:"protocol"`
	Capabilities  struct {
		Binary             string `json:"binary"`
		Transport          string `json:"transport"`
		PersistentSessions bool   `json:"persistent_sessions"`
		SessionExec        bool   `json:"session_exec"`
		FileSystemRPC      bool   `json:"filesystem_rpc"`
		InteractiveShell   bool   `json:"interactive_shell"`
	} `json:"capabilities"`
}

type sessionResult struct {
	Session struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	} `json:"session"`
}

type sessionListResult struct {
	Sessions []struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	} `json:"sessions"`
}

type execResult struct {
	SessionID       string            `json:"session_id"`
	ExitCode        int               `json:"exit_code"`
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
	StdoutTruncated bool              `json:"stdout_truncated"`
	StderrTruncated bool              `json:"stderr_truncated"`
	FinalEnv        map[string]string `json:"final_env"`
	ShellExited     bool              `json:"shell_exited"`
	ControlStderr   string            `json:"control_stderr"`
	Session         struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	} `json:"session"`
}

type serverHandle struct {
	socket string
	cancel context.CancelFunc
	errCh  chan error
}

type testClient struct {
	t      *testing.T
	conn   net.Conn
	enc    *json.Encoder
	dec    *json.Decoder
	nextID atomic.Uint64
}

func newSocketPath(t testing.TB) string {
	t.Helper()

	file, err := os.CreateTemp("", "gbs-")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", path, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%q) error = %v", path, err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func registryWithCommands(t testing.TB, extras ...commands.Command) *commands.Registry {
	t.Helper()

	registry := builtins.DefaultRegistry()
	for _, cmd := range extras {
		if err := registry.Register(cmd); err != nil {
			t.Fatalf("Register(%q) error = %v", cmd.Name(), err)
		}
	}
	return registry
}

func newBlockingCommand(name, line string, started chan struct{}, release <-chan struct{}) commands.Command {
	var once sync.Once

	return commands.DefineCommand(name, func(ctx context.Context, inv *commands.Invocation) error {
		once.Do(func() { close(started) })

		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}

		if inv.Stdout == nil {
			return nil
		}
		_, err := fmt.Fprintln(inv.Stdout, line)
		return err
	})
}

func TestServerSessionLifecycleAndExec(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	})
	client := dialClient(t, srv.socket)
	defer client.Close()

	hello := client.call("system.hello", map[string]any{"client_name": "test"})
	mustOK(t, &hello)
	var helloPayload helloResult
	decodeResult(t, &hello, &helloPayload)
	if helloPayload.Protocol != "2.0" {
		t.Fatalf("protocol = %q, want 2.0", helloPayload.Protocol)
	}
	if helloPayload.Capabilities.Transport != "unix" || !helloPayload.Capabilities.PersistentSessions || !helloPayload.Capabilities.SessionExec {
		t.Fatalf("capabilities = %+v, want unix persistent session exec", helloPayload.Capabilities)
	}
	if helloPayload.Capabilities.FileSystemRPC || helloPayload.Capabilities.InteractiveShell {
		t.Fatalf("capabilities = %+v, want no fs rpc or interactive shell", helloPayload.Capabilities)
	}

	create := client.call("session.create", nil)
	mustOK(t, &create)
	var created sessionResult
	decodeResult(t, &create, &created)
	sessionID := created.Session.SessionID
	if sessionID == "" || created.Session.State != "idle" {
		t.Fatalf("session.create result = %+v, want idle session id", created)
	}

	list := client.call("session.list", nil)
	mustOK(t, &list)
	var listed sessionListResult
	decodeResult(t, &list, &listed)
	if len(listed.Sessions) != 1 || listed.Sessions[0].SessionID != sessionID {
		t.Fatalf("session.list result = %+v, want only %s", listed, sessionID)
	}

	exec := client.call("session.exec", map[string]any{
		"session_id": sessionID,
		"script":     "printf 'hello\\n'; printf 'warn\\n' >&2; export MODE=debug\n",
	})
	mustOK(t, &exec)
	var execPayload execResult
	decodeResult(t, &exec, &execPayload)
	if execPayload.ExitCode != 0 || execPayload.Stdout != "hello\n" || execPayload.Stderr != "warn\n" {
		t.Fatalf("session.exec result = %+v, want hello/warn exit 0", execPayload)
	}
	if execPayload.FinalEnv["MODE"] != "debug" {
		t.Fatalf("final env = %+v, want MODE=debug", execPayload.FinalEnv)
	}
	if execPayload.Session.State != "idle" {
		t.Fatalf("session.exec session state = %+v, want idle after completion", execPayload.Session)
	}

	get := client.call("session.get", map[string]any{"session_id": sessionID})
	mustOK(t, &get)
	var got sessionResult
	decodeResult(t, &get, &got)
	if got.Session.State != "idle" {
		t.Fatalf("session.get result = %+v, want idle", got)
	}

	destroy := client.call("session.destroy", map[string]any{"session_id": sessionID})
	mustOK(t, &destroy)
	decodeResult(t, &destroy, &got)
	if got.Session.SessionID != sessionID {
		t.Fatalf("session.destroy result = %+v, want destroyed session %s", got, sessionID)
	}

	list = client.call("session.list", nil)
	mustOK(t, &list)
	decodeResult(t, &list, &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("session.list result = %+v, want empty after destroy", listed)
	}
}

func TestServerSessionExecShellVariantParam(t *testing.T) {
	t.Parallel()

	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	})
	client := dialClient(t, srv.socket)
	defer client.Close()

	sessionID := mustCreateSession(t, client)
	exec := client.call("session.exec", map[string]any{
		"session_id":    sessionID,
		"shell_variant": "sh",
		"script":        "printf '%s\\n' {a,b}\n",
	})
	mustOK(t, &exec)

	var payload execResult
	decodeResult(t, &exec, &payload)
	if got, want := payload.Stdout, "{a,b}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestServerConcurrentSessionsAndBusy(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	}, gbash.WithRegistry(registryWithCommands(t, newBlockingCommand("blockone", "one", started, release))))
	client1 := dialClient(t, srv.socket)
	defer client1.Close()
	client2 := dialClient(t, srv.socket)
	defer client2.Close()

	session1 := mustCreateSession(t, client1)
	session2 := mustCreateSession(t, client1)

	done := make(chan rpcResponse, 1)
	go func() {
		done <- client1.call("session.exec", map[string]any{
			"session_id": session1,
			"script":     "blockone",
		})
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking session to start")
	}

	busy := client2.call("session.exec", map[string]any{
		"session_id": session1,
		"script":     "printf 'no\\n'",
	})
	if busy.Error == nil || busy.Error.Data == nil || busy.Error.Data.Code != "SESSION_BUSY" {
		t.Fatalf("busy response = %#v, want SESSION_BUSY", busy)
	}

	ok := client2.call("session.exec", map[string]any{
		"session_id": session2,
		"script":     "printf 'two\\n'",
	})
	mustOK(t, &ok)
	var okPayload execResult
	decodeResult(t, &ok, &okPayload)
	if okPayload.Stdout != "two\n" || okPayload.ExitCode != 0 {
		t.Fatalf("second session exec = %+v, want exit 0 and two", okPayload)
	}

	close(release)

	first := <-done
	mustOK(t, &first)
	var firstPayload execResult
	decodeResult(t, &first, &firstPayload)
	if firstPayload.Stdout != "one\n" || firstPayload.ExitCode != 0 {
		t.Fatalf("first session exec = %+v, want exit 0 and one", firstPayload)
	}
}

func TestServerExecReturnsRawOutputsAndFinalEnvWithTraceRedacted(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	}, gbash.WithTracing(gbash.TraceConfig{Mode: gbash.TraceRedacted}))
	client := dialClient(t, srv.socket)
	defer client.Close()

	sessionID := mustCreateSession(t, client)
	exec := client.call("session.exec", map[string]any{
		"session_id": sessionID,
		"script": "" +
			"export API_TOKEN=env-secret\n" +
			"printf 'stdout:%s\\n' \"$API_TOKEN\"\n" +
			"printf 'stderr:%s\\n' \"$API_TOKEN\" >&2\n" +
			"echo -H 'Authorization: Bearer argv-secret' >/dev/null\n",
	})
	mustOK(t, &exec)

	var payload execResult
	decodeResult(t, &exec, &payload)
	if got, want := payload.Stdout, "stdout:env-secret\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := payload.Stderr, "stderr:env-secret\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if got, want := payload.FinalEnv["API_TOKEN"], "env-secret"; got != want {
		t.Fatalf("FinalEnv[API_TOKEN] = %q, want %q", got, want)
	}
}

func TestServerSessionTTLExpiry(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: 250 * time.Millisecond,
	})
	client := dialClient(t, srv.socket)
	defer client.Close()

	sessionID := mustCreateSession(t, client)

	time.Sleep(500 * time.Millisecond)

	list := client.call("session.list", nil)
	mustOK(t, &list)
	var listed sessionListResult
	decodeResult(t, &list, &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("session.list result = %+v, want empty after ttl expiry", listed)
	}

	get := client.call("session.get", map[string]any{"session_id": sessionID})
	if get.Error == nil || get.Error.Data == nil || get.Error.Data.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("session.get result = %#v, want SESSION_NOT_FOUND", get)
	}
}
func TestServeTCPListenerReportsTCPTransport(t *testing.T) {
	t.Parallel()
	rt, err := gbash.New()
	if err != nil {
		t.Fatalf("gbash.New() error = %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test setup
	if err != nil {
		t.Fatalf("Listen(tcp) error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- gbserver.Serve(ctx, ln, gbserver.Config{
			Runtime:    rt,
			Name:       "gbash",
			Version:    "test",
			SessionTTL: time.Second,
		})
	}()

	client := &testClient{
		t:    t,
		conn: mustDialTCP(t, ln.Addr().String()),
	}
	client.enc = json.NewEncoder(client.conn)
	client.dec = json.NewDecoder(client.conn)
	defer client.Close()

	resp := client.call("system.hello", map[string]any{"client_name": "test"})
	mustOK(t, &resp)

	var payload helloResult
	decodeResult(t, &resp, &payload)
	if got, want := payload.Capabilities.Transport, "tcp"; got != want {
		t.Fatalf("transport = %q, want %q", got, want)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve(tcp) error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for tcp server shutdown")
	}
}

func startServer(t *testing.T, cfg gbserver.Config, opts ...gbash.Option) *serverHandle {
	t.Helper()

	rt, err := gbash.New(opts...)
	if err != nil {
		t.Fatalf("gbash.New() error = %v", err)
	}
	cfg.Runtime = rt

	ctx, cancel := context.WithCancel(context.Background())
	socket := newSocketPath(t)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		t.Fatalf("Chmod(%q) error = %v", socket, err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- gbserver.Serve(ctx, ln, cfg)
	}()

	handle := &serverHandle{
		socket: socket,
		cancel: cancel,
		errCh:  errCh,
	}
	t.Cleanup(func() {
		handle.cancel()
		select {
		case err := <-handle.errCh:
			if err != nil {
				t.Fatalf("server exited with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for server shutdown")
		}
	})
	return handle
}

func dialClient(t *testing.T, socket string) *testClient {
	t.Helper()

	conn, err := net.DialTimeout("unix", socket, 5*time.Second) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("DialTimeout(%q) error = %v", socket, err)
	}
	return &testClient{
		t:    t,
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(conn),
	}
}

func mustDialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second) //nolint:noctx // test helper
	if err != nil {
		t.Fatalf("DialTimeout(%q) error = %v", addr, err)
	}
	return conn
}

func (c *testClient) Close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.conn.Close()
}

func (c *testClient) call(method string, params any) rpcResponse {
	c.t.Helper()

	id := fmt.Sprintf("req-%d", c.nextID.Add(1))
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		request["params"] = params
	}
	if err := c.enc.Encode(request); err != nil {
		c.t.Fatalf("Encode(%s) error = %v", method, err)
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		c.t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		c.t.Fatalf("Decode(%s) error = %v", method, err)
	}
	return resp
}

func mustCreateSession(t *testing.T, client *testClient) string {
	t.Helper()

	resp := client.call("session.create", nil)
	mustOK(t, &resp)

	var payload sessionResult
	decodeResult(t, &resp, &payload)
	if payload.Session.SessionID == "" {
		t.Fatalf("session.create result = %+v, want session id", payload)
	}
	return payload.Session.SessionID
}

func mustOK(t *testing.T, resp *rpcResponse) {
	t.Helper()
	if resp == nil || resp.Error != nil {
		t.Fatalf("response = %#v, want success", resp)
	}
	if resp.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q, want 2.0", resp.JSONRPC)
	}
}

func decodeResult[T any](t *testing.T, resp *rpcResponse, out *T) {
	t.Helper()
	if resp == nil || len(resp.Result) == 0 {
		t.Fatalf("response = %#v, want result payload", resp)
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
		t.Fatalf("Unmarshal(result) error = %v; raw=%s", err, string(resp.Result))
	}
}

func TestServerParallelStress(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: 5 * time.Second,
	})

	const numClients = 8
	const opsPerClient = 10

	var wg sync.WaitGroup
	for i := range numClients {
		wg.Go(func() {
			client := dialClient(t, srv.socket)
			defer client.Close()

			sessionID := mustCreateSession(t, client)

			for j := range opsPerClient {
				script := fmt.Sprintf("printf 'c%d-j%d\\n'", i, j)
				resp := client.call("session.exec", map[string]any{
					"session_id": sessionID,
					"script":     script,
				})
				mustOK(t, &resp)
				var payload execResult
				decodeResult(t, &resp, &payload)
				want := fmt.Sprintf("c%d-j%d\n", i, j)
				if payload.Stdout != want {
					t.Errorf("client %d op %d: stdout = %q, want %q", i, j, payload.Stdout, want)
				}
				if payload.ExitCode != 0 {
					t.Errorf("client %d op %d: exit_code = %d, want 0", i, j, payload.ExitCode)
				}
			}

			resp := client.call("session.get", map[string]any{"session_id": sessionID})
			mustOK(t, &resp)

			resp = client.call("session.list", nil)
			mustOK(t, &resp)

			resp = client.call("session.destroy", map[string]any{"session_id": sessionID})
			mustOK(t, &resp)
		})
	}
	wg.Wait()

	verifier := dialClient(t, srv.socket)
	defer verifier.Close()
	list := verifier.call("session.list", nil)
	mustOK(t, &list)
	var listed sessionListResult
	decodeResult(t, &list, &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("session.list after stress = %+v, want empty", listed)
	}
}

func TestServerParallelExecOnSharedSession(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: 5 * time.Second,
	}, gbash.WithRegistry(registryWithCommands(t, newBlockingCommand("blockdone", "done", started, release))))

	setup := dialClient(t, srv.socket)
	defer setup.Close()
	sessionID := mustCreateSession(t, setup)

	blocker := dialClient(t, srv.socket)
	defer blocker.Close()

	blocked := make(chan rpcResponse, 1)
	go func() {
		blocked <- blocker.call("session.exec", map[string]any{
			"session_id": sessionID,
			"script":     "blockdone",
		})
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for blocking exec to start")
	}

	const numWorkers = 10
	type result struct {
		ok   bool
		busy bool
		err  string
	}

	results := make([]result, numWorkers)
	var wg sync.WaitGroup
	for i := range numWorkers {
		wg.Go(func() {
			c := dialClient(t, srv.socket)
			defer c.Close()
			resp := c.call("session.exec", map[string]any{
				"session_id": sessionID,
				"script":     "printf 'done\\n'",
			})
			if resp.Error != nil {
				if resp.Error.Data != nil && resp.Error.Data.Code == "SESSION_BUSY" {
					results[i] = result{busy: true}
				} else {
					results[i] = result{err: resp.Error.Message}
				}
			} else {
				results[i] = result{ok: true}
			}
		})
	}
	wg.Wait()
	close(release)

	var okCount, busyCount, errCount int
	for _, r := range results {
		switch {
		case r.ok:
			okCount++
		case r.busy:
			busyCount++
		default:
			errCount++
			t.Errorf("unexpected error: %s", r.err)
		}
	}

	if okCount != 0 {
		t.Fatalf("expected competing execs to be rejected, got ok=%d busy=%d err=%d", okCount, busyCount, errCount)
	}
	if errCount > 0 {
		t.Fatalf("unexpected errors: ok=%d busy=%d err=%d", okCount, busyCount, errCount)
	}
	if busyCount != numWorkers {
		t.Fatalf("expected all competing execs to be busy, got ok=%d busy=%d err=%d", okCount, busyCount, errCount)
	}

	resp := <-blocked
	mustOK(t, &resp)
	var payload execResult
	decodeResult(t, &resp, &payload)
	if got, want := payload.Stdout, "done\n"; got != want {
		t.Fatalf("blocking exec stdout = %q, want %q", got, want)
	}
	if payload.ExitCode != 0 {
		t.Fatalf("blocking exec exit_code = %d, want 0", payload.ExitCode)
	}
}

func TestServerParallelCreateDestroy(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: 5 * time.Second,
	})

	const rounds = 20
	var wg sync.WaitGroup
	for range rounds {
		wg.Go(func() {
			c := dialClient(t, srv.socket)
			defer c.Close()

			id := mustCreateSession(t, c)

			resp := c.call("session.exec", map[string]any{
				"session_id": id,
				"script":     "printf 'ok\\n'",
			})
			mustOK(t, &resp)

			resp = c.call("session.destroy", map[string]any{"session_id": id})
			mustOK(t, &resp)
		})
	}
	wg.Wait()

	verifier := dialClient(t, srv.socket)
	defer verifier.Close()
	list := verifier.call("session.list", nil)
	mustOK(t, &list)
	var listed sessionListResult
	decodeResult(t, &list, &listed)
	if len(listed.Sessions) != 0 {
		t.Fatalf("session.list after create/destroy = %+v, want empty", listed)
	}
}

func TestServerParallelFileIO(t *testing.T) {
	t.Parallel(
	// Seed a temp directory with files that every session can read through
	// the overlay FS, then hammer the server with concurrent sessions doing
	// file reads, writes, tails, and pipes.
	)

	hostDir := t.TempDir()
	for i := range 5 {
		name := filepath.Join(hostDir, fmt.Sprintf("data-%d.txt", i))
		var lines []string
		for j := range 20 {
			lines = append(lines, fmt.Sprintf("line-%d-%d", i, j))
		}
		if err := os.WriteFile(name, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: 10 * time.Second,
	}, gbash.WithWorkspace(hostDir))

	const numClients = 6
	var wg sync.WaitGroup
	for i := range numClients {
		wg.Go(func() {
			c := dialClient(t, srv.socket)
			defer c.Close()
			sid := mustCreateSession(t, c)

			// cat a host file
			resp := c.call("session.exec", map[string]any{
				"session_id": sid,
				"script":     fmt.Sprintf("cat data-%d.txt | wc -l", i%5),
			})
			mustOK(t, &resp)
			var payload execResult
			decodeResult(t, &resp, &payload)
			if got := strings.TrimSpace(payload.Stdout); got != "20" {
				t.Errorf("client %d cat|wc: got %q, want 20", i, got)
			}

			// tail the last 3 lines
			resp = c.call("session.exec", map[string]any{
				"session_id": sid,
				"script":     fmt.Sprintf("tail -n 3 data-%d.txt", i%5),
			})
			mustOK(t, &resp)
			decodeResult(t, &resp, &payload)
			tailLines := strings.Split(strings.TrimRight(payload.Stdout, "\n"), "\n")
			if len(tailLines) != 3 {
				t.Errorf("client %d tail: got %d lines, want 3", i, len(tailLines))
			}

			// write a new file in the sandbox overlay, then read it back
			resp = c.call("session.exec", map[string]any{
				"session_id": sid,
				"script": fmt.Sprintf(
					"seq 1 100 > /tmp/out-%d.txt\nwc -l < /tmp/out-%d.txt", i, i),
			})
			mustOK(t, &resp)
			decodeResult(t, &resp, &payload)
			if got := strings.TrimSpace(payload.Stdout); got != "100" {
				t.Errorf("client %d seq|wc: got %q, want 100", i, got)
			}

			// head + grep pipeline on host file
			resp = c.call("session.exec", map[string]any{
				"session_id": sid,
				"script":     fmt.Sprintf("head -n 10 data-%d.txt | grep 'line-%d-5'", i%5, i%5),
			})
			mustOK(t, &resp)
			decodeResult(t, &resp, &payload)
			want := fmt.Sprintf("line-%d-5\n", i%5)
			if payload.Stdout != want {
				t.Errorf("client %d head|grep: got %q, want %q", i, payload.Stdout, want)
			}

			// append to a file then cat it
			resp = c.call("session.exec", map[string]any{
				"session_id": sid,
				"script": fmt.Sprintf(
					"cp data-%d.txt /tmp/copy-%d.txt\n"+
						"echo 'appended' >> /tmp/copy-%d.txt\n"+
						"tail -n 1 /tmp/copy-%d.txt", i%5, i, i, i),
			})
			mustOK(t, &resp)
			decodeResult(t, &resp, &payload)
			if got := strings.TrimSpace(payload.Stdout); got != "appended" {
				t.Errorf("client %d append+tail: got %q, want appended", i, got)
			}

			resp = c.call("session.destroy", map[string]any{"session_id": sid})
			mustOK(t, &resp)
		})
	}
	wg.Wait()
}

func TestServerRejectsInvalidMethod(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	})
	client := dialClient(t, srv.socket)
	defer client.Close()

	resp := client.call("session.stream", nil)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("response = %#v, want method not found", resp)
	}
}

func TestServerExecCarriesSessionStateInResult(t *testing.T) {
	t.Parallel()
	srv := startServer(t, gbserver.Config{
		Name:       "gbash",
		Version:    "test",
		SessionTTL: time.Second,
	})
	client := dialClient(t, srv.socket)
	defer client.Close()

	sessionID := mustCreateSession(t, client)
	resp := client.call("session.exec", map[string]any{
		"session_id": sessionID,
		"script":     "cd /tmp\npwd\n",
	})
	mustOK(t, &resp)

	var payload execResult
	decodeResult(t, &resp, &payload)
	if got, want := payload.Stdout, "/tmp\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if payload.Session.State != "idle" {
		t.Fatalf("session = %+v, want idle summary in exec result", payload.Session)
	}
	if payload.Session.SessionID != sessionID {
		t.Fatalf("session = %+v, want session %s", payload.Session, sessionID)
	}
	if !strings.Contains(payload.FinalEnv["PWD"], "/tmp") {
		t.Fatalf("final env = %+v, want PWD to end in /tmp", payload.FinalEnv)
	}
}
