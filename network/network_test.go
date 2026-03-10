package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type resolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (fn resolverFunc) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return fn(ctx, host)
}

func TestNewRejectsInvalidAllowList(t *testing.T) {
	_, err := New(&Config{
		AllowedURLPrefixes: []string{"example.com"},
	})
	if err == nil {
		t.Fatal("New() error = nil, want invalid config error")
	}
}

func TestClientAllowsMatchingOriginAndPathPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client, err := New(&Config{
		AllowedURLPrefixes: []string{server.URL + "/v1/"},
		DenyPrivateRanges:  false,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resp, err := client.Do(context.Background(), &Request{
		URL: server.URL + "/v1/users",
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if got, want := string(resp.Body), "ok"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func TestClientBlocksPathOutsideAllowListPrefix(t *testing.T) {
	client, err := New(&Config{
		AllowedURLPrefixes: []string{"https://api.example.com/v1/"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{
		URL: "https://api.example.com/v2/users",
	})
	if !IsDenied(err) {
		t.Fatalf("Do() error = %v, want denied error", err)
	}
}

func TestClientBlocksDisallowedMethod(t *testing.T) {
	client, err := New(&Config{
		AllowedURLPrefixes: []string{"https://api.example.com"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{
		Method: "POST",
		URL:    "https://api.example.com/items",
	})
	var methodErr *MethodNotAllowedError
	if !errors.As(err, &methodErr) {
		t.Fatalf("Do() error = %v, want method denied error", err)
	}
}

func TestClientRevalidatesRedirectTargets(t *testing.T) {
	deniedURL := "https://other.example.com/blocked"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, deniedURL, http.StatusFound)
	}))
	defer server.Close()

	client, err := New(&Config{
		AllowedURLPrefixes: []string{server.URL},
		DenyPrivateRanges:  false,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{
		URL:             server.URL,
		FollowRedirects: true,
	})
	var redirectErr *RedirectNotAllowedError
	if !errors.As(err, &redirectErr) {
		t.Fatalf("Do() error = %v, want redirect denied error", err)
	}
}

func TestClientEnforcesResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 32)))
	}))
	defer server.Close()

	client, err := New(&Config{
		AllowedURLPrefixes: []string{server.URL},
		MaxResponseBytes:   8,
		DenyPrivateRanges:  false,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{URL: server.URL})
	var sizeErr *ResponseTooLargeError
	if !errors.As(err, &sizeErr) {
		t.Fatalf("Do() error = %v, want response-too-large error", err)
	}
}

func TestClientBlocksPrivateRangesLexically(t *testing.T) {
	client, err := New(&Config{
		AllowedURLPrefixes: []string{"http://127.0.0.1"},
		DenyPrivateRanges:  true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{URL: "http://127.0.0.1/"})
	if !IsDenied(err) {
		t.Fatalf("Do() error = %v, want denied error", err)
	}
}

func TestClientBlocksPrivateRangesAfterDNSResolution(t *testing.T) {
	client, err := New(&Config{
		AllowedURLPrefixes: []string{"https://api.example.com"},
		DenyPrivateRanges:  true,
	}, WithResolver(resolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Do(context.Background(), &Request{URL: "https://api.example.com/path"})
	if !IsDenied(err) {
		t.Fatalf("Do() error = %v, want denied error", err)
	}
}
