package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ewhauser/gbash"
)

const (
	jsonRPCVersion = "2.0"
	defaultTTL     = 30 * time.Minute
	minReaperTick  = 100 * time.Millisecond
	maxReaperTick  = time.Second

	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603

	rpcSessionNotFound = -32010
	rpcSessionBusy     = -32011
)

// Config configures the shared gbash server mode.
type Config struct {
	Runtime    *gbash.Runtime
	Name       string
	Version    string
	SessionTTL time.Duration
}

// ListenAndServeUnix serves the gbash JSON-RPC protocol on a Unix domain socket.
func ListenAndServeUnix(ctx context.Context, socketPath string, cfg Config) error {
	socketPath, ln, err := listenUnixSocket(socketPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()
	return Serve(ctx, ln, cfg)
}

func listenUnixSocket(socketPath string) (string, net.Listener, error) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return "", nil, fmt.Errorf("server: socket path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		return "", nil, fmt.Errorf("server: create socket directory: %w", err)
	}
	if err := removeStaleUnixSocket(socketPath); err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return "", nil, fmt.Errorf("server: listen on unix socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return "", nil, fmt.Errorf("server: chmod socket: %w", err)
	}
	return socketPath, ln, nil
}

// Serve serves the gbash JSON-RPC protocol on an existing listener.
func Serve(ctx context.Context, ln net.Listener, cfg Config) error {
	if ln == nil {
		return fmt.Errorf("server: listener is nil")
	}
	cfg = normalizeConfig(cfg)
	if cfg.Runtime == nil {
		return fmt.Errorf("server: runtime is nil")
	}

	srv := &serverState{
		ctx:       ctx,
		cfg:       cfg,
		transport: listenerTransport(ln),
		sessions:  make(map[string]*serverSession),
		conns:     make(map[string]*clientConn),
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
			srv.closeConnections()
		case <-done:
		}
	}()
	go srv.reapLoop(done)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("server: accept: %w", err)
		}
		srv.addConn(conn)
	}
}

type serverState struct {
	ctx       context.Context
	cfg       Config
	transport string

	nextID atomic.Uint64

	connsMu sync.RWMutex
	conns   map[string]*clientConn

	sessionsMu sync.Mutex
	sessions   map[string]*serverSession
}

type clientConn struct {
	server *serverState
	id     string
	conn   net.Conn
	enc    *json.Encoder
	dec    *json.Decoder

	writeMu sync.Mutex
	closed  chan struct{}
	once    sync.Once
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrorObject `json:"error,omitempty"`
}

type rpcErrorObject struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Data    *rpcErrorData `json:"data,omitempty"`
}

type rpcErrorData struct {
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

type helloParams struct {
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
}

func normalizeConfig(cfg Config) Config {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaultTTL
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		cfg.Name = "gbash"
	}
	cfg.Version = strings.TrimSpace(cfg.Version)
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	return cfg
}

func listenerTransport(ln net.Listener) string {
	if ln == nil || ln.Addr() == nil {
		return ""
	}
	switch strings.TrimSpace(ln.Addr().Network()) {
	case "tcp4", "tcp6":
		return "tcp"
	default:
		return strings.TrimSpace(ln.Addr().Network())
	}
}

func removeStaleUnixSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("server: path exists and is not a socket: %s", socketPath)
		}
		conn, dialErr := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			return fmt.Errorf("server: socket already in use: %s", socketPath)
		}
		switch {
		case errors.Is(dialErr, syscall.ECONNREFUSED), errors.Is(dialErr, syscall.ENOENT), errors.Is(dialErr, os.ErrNotExist):
		default:
			return fmt.Errorf("server: probe existing socket: %w", dialErr)
		}
		if removeErr := os.Remove(socketPath); removeErr != nil {
			return fmt.Errorf("server: remove stale socket: %w", removeErr)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("server: stat socket path: %w", err)
}

func (s *serverState) reapLoop(done <-chan struct{}) {
	tick := s.cfg.SessionTTL / 2
	switch {
	case tick <= 0:
		tick = maxReaperTick
	case tick < minReaperTick:
		tick = minReaperTick
	case tick > maxReaperTick:
		tick = maxReaperTick
	}

	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.reapExpiredSessions(time.Now().UTC())
		case <-done:
			return
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *serverState) addConn(nc net.Conn) {
	conn := &clientConn{
		server: s,
		id:     s.nextIDValue("conn"),
		conn:   nc,
		enc:    json.NewEncoder(nc),
		dec:    json.NewDecoder(nc),
		closed: make(chan struct{}),
	}

	s.connsMu.Lock()
	s.conns[conn.id] = conn
	s.connsMu.Unlock()

	go conn.readLoop()
}

func (s *serverState) removeConn(id string) {
	s.connsMu.Lock()
	delete(s.conns, id)
	s.connsMu.Unlock()
}

func (s *serverState) closeConnections() {
	s.connsMu.RLock()
	conns := make([]*clientConn, 0, len(s.conns))
	for _, conn := range s.conns {
		conns = append(conns, conn)
	}
	s.connsMu.RUnlock()

	for _, conn := range conns {
		conn.close()
	}
}

func (s *serverState) nextIDValue(prefix string) string {
	return fmt.Sprintf("%s-%06d", prefix, s.nextID.Add(1))
}

func (c *clientConn) readLoop() {
	defer c.close()

	for {
		var raw json.RawMessage
		if err := c.dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			_ = c.writeResponse(newRPCErrorResponse(nil, rpcParseError, "parse error", nil))
			return
		}

		req, errResp := parseRPCRequest(raw)
		if errResp != nil {
			if err := c.writeResponse(errResp); err != nil {
				return
			}
			continue
		}

		resp := c.handleRequest(req)
		if resp == nil {
			continue
		}
		if err := c.writeResponse(resp); err != nil {
			return
		}
	}
}

func parseRPCRequest(raw json.RawMessage) (*rpcRequest, *rpcResponse) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, newRPCErrorResponse(nil, rpcInvalidRequest, "invalid request", nil)
	}
	if trimmed[0] == '[' {
		return nil, newRPCErrorResponse(nil, rpcInvalidRequest, "batch requests are not supported", nil)
	}

	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return nil, newRPCErrorResponse(nil, rpcInvalidRequest, "invalid request", &rpcErrorData{
			Code:    "INVALID_REQUEST",
			Details: err.Error(),
		})
	}
	if req.JSONRPC != jsonRPCVersion {
		return nil, newRPCErrorResponse(validResponseID(req.ID), rpcInvalidRequest, "invalid request", &rpcErrorData{
			Code:    "INVALID_REQUEST",
			Details: "jsonrpc must be \"2.0\"",
		})
	}
	if len(req.ID) == 0 || !validRequestID(req.ID) {
		return nil, newRPCErrorResponse(nil, rpcInvalidRequest, "invalid request", &rpcErrorData{
			Code:    "INVALID_REQUEST",
			Details: "id must be a string, number, or null",
		})
	}
	if strings.TrimSpace(req.Method) == "" {
		return nil, newRPCErrorResponse(validResponseID(req.ID), rpcInvalidRequest, "invalid request", &rpcErrorData{
			Code:    "INVALID_REQUEST",
			Details: "method must not be empty",
		})
	}
	return &req, nil
}

func validRequestID(id json.RawMessage) bool {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '"', 'n', '-':
		return true
	default:
		return trimmed[0] >= '0' && trimmed[0] <= '9'
	}
}

func validResponseID(id json.RawMessage) json.RawMessage {
	if validRequestID(id) {
		return id
	}
	return nil
}

func (c *clientConn) handleRequest(req *rpcRequest) *rpcResponse {
	if req == nil {
		return newRPCErrorResponse(nil, rpcInvalidRequest, "invalid request", nil)
	}

	switch req.Method {
	case "system.hello", "hello":
		if _, err := decodeParams[helloParams](req.Params); err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, helloResult{
			ServerName:    c.server.cfg.Name,
			ServerVersion: c.server.cfg.Version,
			Protocol:      jsonRPCVersion,
			Capabilities: helloCapabilities{
				Binary:             c.server.cfg.Name,
				Transport:          c.server.transport,
				PersistentSessions: true,
				SessionExec:        true,
				FileSystemRPC:      false,
				InteractiveShell:   false,
			},
		})
	case "system.ping", "ping":
		if _, err := decodeParams[map[string]any](req.Params); err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, map[string]any{"pong": true})
	case "session.create":
		if _, err := decodeParams[map[string]any](req.Params); err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		session, err := c.server.createSession()
		if err != nil {
			return internalErrorResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, sessionCreateResult{Session: session.summary()})
	case "session.get":
		params, err := decodeParams[sessionIDParams](req.Params)
		if err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		session, err := c.server.lookupSession(params.SessionID)
		if err != nil {
			return appErrorResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, sessionGetResult{Session: session.summary()})
	case "session.list":
		if _, err := decodeParams[map[string]any](req.Params); err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		c.server.reapExpiredSessions(time.Now().UTC())
		return newRPCResultResponse(req.ID, sessionListResult{Sessions: c.server.listSessions()})
	case "session.destroy":
		params, err := decodeParams[sessionIDParams](req.Params)
		if err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		session, err := c.server.destroySession(params.SessionID)
		if err != nil {
			return appErrorResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, sessionGetResult{Session: session.summary()})
	case "session.exec":
		params, err := decodeParams[sessionExecParams](req.Params)
		if err != nil {
			return invalidParamsResponse(req.ID, err)
		}
		session, err := c.server.lookupSession(params.SessionID)
		if err != nil {
			return appErrorResponse(req.ID, err)
		}
		result, err := session.exec(c.server.ctx, &params)
		if err != nil {
			return appErrorResponse(req.ID, err)
		}
		return newRPCResultResponse(req.ID, result)
	default:
		return newRPCErrorResponse(req.ID, rpcMethodNotFound, "method not found", nil)
	}
}

func (c *clientConn) writeResponse(resp *rpcResponse) error {
	if resp == nil {
		return nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.enc.Encode(resp)
}

func (c *clientConn) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		c.server.removeConn(c.id)
	})
}

func newRPCResultResponse(id json.RawMessage, result any) *rpcResponse {
	return &rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      responseID(id),
		Result:  result,
	}
}

func newRPCErrorResponse(id json.RawMessage, code int, message string, data *rpcErrorData) *rpcResponse {
	return &rpcResponse{
		JSONRPC: jsonRPCVersion,
		ID:      responseID(id),
		Error: &rpcErrorObject{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}

func responseID(id json.RawMessage) json.RawMessage {
	if validRequestID(id) {
		return id
	}
	return json.RawMessage("null")
}

func invalidParamsResponse(id json.RawMessage, err error) *rpcResponse {
	return newRPCErrorResponse(id, rpcInvalidParams, "invalid params", &rpcErrorData{
		Code:    "INVALID_ARGUMENT",
		Details: err.Error(),
	})
}

func internalErrorResponse(id json.RawMessage, err error) *rpcResponse {
	details := ""
	if err != nil {
		details = err.Error()
	}
	return newRPCErrorResponse(id, rpcInternalError, "internal error", &rpcErrorData{
		Code:    "INTERNAL",
		Details: details,
	})
}

func appErrorResponse(id json.RawMessage, err error) *rpcResponse {
	switch {
	case errors.Is(err, errInvalidArgument):
		return invalidParamsResponse(id, err)
	case errors.Is(err, errSessionNotFound):
		return newRPCErrorResponse(id, rpcSessionNotFound, "session not found", &rpcErrorData{
			Code:    "SESSION_NOT_FOUND",
			Details: err.Error(),
		})
	case errors.Is(err, errSessionBusy):
		return newRPCErrorResponse(id, rpcSessionBusy, "session is busy", &rpcErrorData{
			Code:    "SESSION_BUSY",
			Details: err.Error(),
		})
	default:
		return internalErrorResponse(id, err)
	}
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var out T
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return out, nil
	}
	if err := json.Unmarshal(trimmed, &out); err != nil {
		return out, fmt.Errorf("%w: %v", errInvalidArgument, err)
	}
	return out, nil
}
