package runtime

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ewhauser/jbgo/network"
)

type curlStubClient struct {
	mu       sync.Mutex
	requests []*network.Request
	do       func(context.Context, *network.Request) (*network.Response, error)
}

func (c *curlStubClient) Do(ctx context.Context, req *network.Request) (*network.Response, error) {
	clone := &network.Request{
		Method:          req.Method,
		URL:             req.URL,
		Headers:         cloneStringMap(req.Headers),
		Body:            append([]byte(nil), req.Body...),
		FollowRedirects: req.FollowRedirects,
		Timeout:         req.Timeout,
	}

	c.mu.Lock()
	c.requests = append(c.requests, clone)
	c.mu.Unlock()

	if c.do != nil {
		return c.do(ctx, clone)
	}
	return &network.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       []byte("OK"),
		URL:        clone.URL,
	}, nil
}

func (c *curlStubClient) LastRequest() *network.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		return nil
	}
	last := c.requests[len(c.requests)-1]
	return &network.Request{
		Method:          last.Method,
		URL:             last.URL,
		Headers:         cloneStringMap(last.Headers),
		Body:            append([]byte(nil), last.Body...),
		FollowRedirects: last.FollowRedirects,
		Timeout:         last.Timeout,
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func TestCurlSupportsAuthCookiesRemoteNameAndWriteOut(t *testing.T) {
	client := &curlStubClient{
		do: func(_ context.Context, req *network.Request) (*network.Response, error) {
			return &network.Response{
				StatusCode: 201,
				Status:     "201 Created",
				Headers: map[string]string{
					"Content-Type": "application/json; charset=utf-8",
					"Set-Cookie":   "session=abc123; Path=/; HttpOnly",
				},
				Body: []byte(`{"ok":true}`),
				URL:  req.URL,
			}, nil
		},
	}
	session := newSession(t, &Config{NetworkClient: client})

	result := mustExecSession(t, session,
		"curl -s -u user:pass -A 'agent/1.0' -e https://ref.example -b 'a=1; b=2' -c /tmp/cookies.txt -O -w '%{http_code} %{content_type} %{url_effective} %{size_download}' https://api.example.com/files/report.json\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "201 application/json; charset=utf-8 https://api.example.com/files/report.json 11"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	req := client.LastRequest()
	if req == nil {
		t.Fatal("LastRequest = nil, want captured request")
	}
	if got, want := req.Method, "GET"; got != want {
		t.Fatalf("Method = %q, want %q", got, want)
	}
	if got := req.Headers["Authorization"]; got != "Basic dXNlcjpwYXNz" {
		t.Fatalf("Authorization = %q, want basic auth header", got)
	}
	for key, want := range map[string]string{
		"User-Agent": "agent/1.0",
		"Referer":    "https://ref.example",
		"Cookie":     "a=1; b=2",
	} {
		if got := req.Headers[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}

	if got, want := string(readSessionFile(t, session, "/tmp/cookies.txt")), "session=abc123; Path=/; HttpOnly"; got != want {
		t.Fatalf("cookies.txt = %q, want %q", got, want)
	}
	if got, want := string(readSessionFile(t, session, "/home/agent/report.json")), `{"ok":true}`; got != want {
		t.Fatalf("report.json = %q, want %q", got, want)
	}
}

func TestCurlSupportsURLEncodedDataAndForgivingHeaders(t *testing.T) {
	client := &curlStubClient{}
	session := newSession(t, &Config{NetworkClient: client})

	result := mustExecSession(t, session,
		`curl -s -H "NoColonHeader" -H "X-Empty:" -H "X-Time: 12:30:45" --data-urlencode "message=hello world" -m 0.5 https://api.example.com/post`+"\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	req := client.LastRequest()
	if req == nil {
		t.Fatal("LastRequest = nil, want captured request")
	}
	if got, want := req.Method, "POST"; got != want {
		t.Fatalf("Method = %q, want %q", got, want)
	}
	if got := string(req.Body); got != "message=hello%20world" {
		t.Fatalf("Body = %q, want URL-encoded payload", got)
	}
	if got := req.Timeout; got != 500*time.Millisecond {
		t.Fatalf("Timeout = %s, want 500ms", got)
	}
	if _, ok := req.Headers["NoColonHeader"]; ok {
		t.Fatalf("Headers = %#v, want invalid header ignored", req.Headers)
	}
	if got := req.Headers["X-Empty"]; got != "" {
		t.Fatalf("X-Empty = %q, want empty value", got)
	}
	if got := req.Headers["X-Time"]; got != "12:30:45" {
		t.Fatalf("X-Time = %q, want preserved colon value", got)
	}
}

func TestCurlSupportsMultipartAndUploadFile(t *testing.T) {
	client := &curlStubClient{}
	session := newSession(t, &Config{NetworkClient: client})
	writeSessionFile(t, session, "/tmp/upload.bin", []byte("file\x00data"))

	formResult := mustExecSession(t, session,
		"curl -s -F 'file=@/tmp/upload.bin;type=application/octet-stream' https://api.example.com/upload\n",
	)
	if formResult.ExitCode != 0 {
		t.Fatalf("form ExitCode = %d, want 0; stderr=%q", formResult.ExitCode, formResult.Stderr)
	}

	formReq := client.LastRequest()
	if formReq == nil {
		t.Fatal("form request = nil, want captured request")
	}
	if got, want := formReq.Method, "POST"; got != want {
		t.Fatalf("form Method = %q, want %q", got, want)
	}
	if got := formReq.Headers["Content-Type"]; !strings.HasPrefix(got, "multipart/form-data; boundary=") {
		t.Fatalf("form Content-Type = %q, want multipart boundary", got)
	}
	body := formReq.Body
	for _, want := range [][]byte{
		[]byte(`name="file"`),
		[]byte(`filename="upload.bin"`),
		[]byte("Content-Type: application/octet-stream"),
		[]byte("file\x00data"),
	} {
		if !bytes.Contains(body, want) {
			t.Fatalf("multipart body = %q, want %q", string(body), string(want))
		}
	}

	uploadResult := mustExecSession(t, session,
		"curl -s -T /tmp/upload.bin https://api.example.com/files/upload.bin\n",
	)
	if uploadResult.ExitCode != 0 {
		t.Fatalf("upload ExitCode = %d, want 0; stderr=%q", uploadResult.ExitCode, uploadResult.Stderr)
	}

	uploadReq := client.LastRequest()
	if uploadReq == nil {
		t.Fatal("upload request = nil, want captured request")
	}
	if got, want := uploadReq.Method, "PUT"; got != want {
		t.Fatalf("upload Method = %q, want %q", got, want)
	}
	if got := string(uploadReq.Body); got != "file\x00data" {
		t.Fatalf("upload Body = %q, want uploaded file contents", got)
	}
}

func TestCurlVerboseAndHeadOutputMatchUpstreamShape(t *testing.T) {
	client := &curlStubClient{
		do: func(_ context.Context, req *network.Request) (*network.Response, error) {
			return &network.Response{
				StatusCode: 200,
				Status:     "200 OK",
				Headers: map[string]string{
					"Content-Type":    "application/json",
					"X-Custom-Header": "test-value",
				},
				Body: []byte(`{"data":"test"}`),
				URL:  req.URL,
			}, nil
		},
	}
	session := newSession(t, &Config{NetworkClient: client})

	verbose := mustExecSession(t, session,
		"curl -v -H 'Accept: application/json' https://api.example.com/test\n",
	)
	if verbose.ExitCode != 0 {
		t.Fatalf("verbose ExitCode = %d, want 0; stderr=%q", verbose.ExitCode, verbose.Stderr)
	}
	for _, want := range []string{
		"> GET https://api.example.com/test\n",
		"> Accept: application/json\n",
		"< HTTP/1.1 200 OK\n",
		"< Content-Type: application/json\n",
		`{"data":"test"}`,
	} {
		if !strings.Contains(verbose.Stdout, want) {
			t.Fatalf("verbose stdout = %q, want %q", verbose.Stdout, want)
		}
	}

	head := mustExecSession(t, session,
		"curl -I https://api.example.com/test\n",
	)
	if head.ExitCode != 0 {
		t.Fatalf("head ExitCode = %d, want 0; stderr=%q", head.ExitCode, head.Stderr)
	}
	if !strings.Contains(head.Stdout, "HTTP/1.1 200 OK\r\n") {
		t.Fatalf("head stdout = %q, want header block", head.Stdout)
	}
	if strings.Contains(head.Stdout, `{"data":"test"}`) {
		t.Fatalf("head stdout = %q, want no response body", head.Stdout)
	}
}

func TestCurlUsesUpstreamExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		client     *curlStubClient
		wantExit   int
		wantStderr string
	}{
		{
			name:   "allowlist denial",
			script: "curl https://blocked.example.com/data\n",
			client: &curlStubClient{do: func(_ context.Context, req *network.Request) (*network.Response, error) {
				return nil, &network.AccessDeniedError{URL: req.URL, Reason: "URL not in allowlist"}
			}},
			wantExit:   7,
			wantStderr: "Network access denied",
		},
		{
			name:   "method denial",
			script: "curl -X POST https://api.example.com/data\n",
			client: &curlStubClient{do: func(_ context.Context, _ *network.Request) (*network.Response, error) {
				return nil, &network.MethodNotAllowedError{Method: "POST"}
			}},
			wantExit:   3,
			wantStderr: "POST",
		},
		{
			name:   "redirect denial",
			script: "curl https://api.example.com/data\n",
			client: &curlStubClient{do: func(_ context.Context, _ *network.Request) (*network.Response, error) {
				return nil, &network.RedirectNotAllowedError{URL: "https://blocked.example.com/redirect"}
			}},
			wantExit:   47,
			wantStderr: "Redirect",
		},
		{
			name:   "timeout",
			script: "curl -m 0.5 https://api.example.com/slow\n",
			client: &curlStubClient{do: func(_ context.Context, _ *network.Request) (*network.Response, error) {
				return nil, context.DeadlineExceeded
			}},
			wantExit:   28,
			wantStderr: "The operation was aborted",
		},
		{
			name:   "http fail",
			script: "curl -f https://api.example.com/missing\n",
			client: &curlStubClient{do: func(_ context.Context, req *network.Request) (*network.Response, error) {
				return &network.Response{
					StatusCode: 404,
					Status:     "404 Not Found",
					Headers:    map[string]string{"Content-Type": "text/plain"},
					Body:       []byte("missing"),
					URL:        req.URL,
				}, nil
			}},
			wantExit:   22,
			wantStderr: "The requested URL returned error: 404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := newRuntime(t, &Config{NetworkClient: tt.client})
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tt.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tt.wantExit {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tt.wantExit, result.Stderr)
			}
			if !strings.Contains(result.Stderr, tt.wantStderr) {
				t.Fatalf("Stderr = %q, want %q", result.Stderr, tt.wantStderr)
			}
		})
	}
}
