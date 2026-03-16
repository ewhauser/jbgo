package network

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
)

func FuzzHTTPClientPolicy(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{1, 1, 1, 1})
	f.Add([]byte{2, 2, 1, 0})
	f.Add([]byte("98"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		cursor := newNetworkFuzzCursor(raw)

		allowlist := []string{
			"https://api.example.com/",
			"https://api.example.com/private",
			"https://api.example.com/private/",
			"https://cdn.example.com/files/",
		}
		requests := []string{
			"https://api.example.com/data",
			"https://api.example.com/private/token",
			"https://api.example.com/private-token",
			"https://cdn.example.com/files/item.txt",
			"https://evil.example.net/data",
			"http://localhost/admin",
		}
		redirects := []string{
			"",
			"/data",
			"https://api.example.com/data",
			"https://api.example.com/private-token",
			"https://evil.example.net/data",
			"http://localhost/admin",
		}

		requestURL := requests[cursor.Intn(len(requests))]
		redirectTarget := redirects[cursor.Intn(len(redirects))]
		follow := cursor.Intn(2) == 1
		denyPrivate := cursor.Intn(2) == 1
		statusCode := []int{http.StatusOK, http.StatusFound}[cursor.Intn(2)]

		client, err := New(&Config{
			AllowedURLPrefixes: allowlist,
			DenyPrivateRanges:  denyPrivate,
			MaxRedirects:       1,
			MaxResponseBytes:   32,
		}, WithDoer(&networkFuzzDoer{
			statusCode: statusCode,
			location:   redirectTarget,
			body:       bytes.Repeat([]byte("a"), 8),
		}), WithResolver(networkFuzzResolver{}))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		resp, err := client.Do(context.Background(), &Request{
			Method:          string(MethodGet),
			URL:             requestURL,
			FollowRedirects: follow,
		})

		expectDenied := false
		if err := client.checkURLAllowed(context.Background(), requestURL); err != nil {
			expectDenied = true
		} else if follow && statusCode == http.StatusFound && redirectTarget != "" {
			nextURL, err := resolveRedirectURL(requestURL, redirectTarget)
			if err != nil {
				t.Fatalf("resolveRedirectURL(%q, %q) error = %v", requestURL, redirectTarget, err)
			}
			if err := client.checkURLAllowed(context.Background(), nextURL); err != nil {
				expectDenied = true
			}
		}

		if expectDenied {
			if err == nil || !IsDenied(err) {
				t.Fatalf("Do(%q) error = %v, want denied (redirect=%q follow=%v)", requestURL, err, redirectTarget, follow)
			}
			return
		}

		if err != nil {
			t.Fatalf("Do(%q) error = %v", requestURL, err)
		}
		if resp == nil {
			t.Fatalf("Do(%q) returned nil response", requestURL)
		}
	})
}

type networkFuzzDoer struct {
	statusCode int
	location   string
	body       []byte
	calls      int
}

func (d *networkFuzzDoer) Do(req *http.Request) (*http.Response, error) {
	statusCode := d.statusCode
	location := d.location
	if d.calls > 0 && statusCode == http.StatusFound {
		statusCode = http.StatusOK
		location = ""
	}
	d.calls++

	resp := &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(d.body)),
	}
	if location != "" {
		resp.Header.Set("Location", location)
	}
	return resp, nil
}

type networkFuzzResolver struct{}

func (networkFuzzResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	switch host {
	case "localhost":
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	default:
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
}

type networkFuzzCursor struct {
	data []byte
	idx  int
}

func newNetworkFuzzCursor(data []byte) *networkFuzzCursor {
	return &networkFuzzCursor{data: data}
}

func (c *networkFuzzCursor) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	if len(c.data) == 0 {
		c.idx++
		return c.idx % n
	}
	value := int(c.data[c.idx%len(c.data)])
	c.idx++
	return value % n
}
