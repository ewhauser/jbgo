package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/ewhauser/gbash/network"
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

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

type tarFixtureEntry struct {
	Name     string
	Body     []byte
	Typeflag byte
	Linkname string
}

func buildTarFixture(t *testing.T, entries ...tarFixtureEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.Name,
			Typeflag: typeflag,
			Linkname: entry.Linkname,
			Mode:     0o644,
		}
		switch typeflag {
		case tar.TypeReg, 0:
			header.Size = int64(len(entry.Body))
		case tar.TypeSymlink:
			header.Mode = 0o777
		case tar.TypeDir:
			header.Mode = 0o755
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%q) error = %v", entry.Name, err)
		}
		if typeflag == tar.TypeReg || typeflag == 0 {
			if _, err := tw.Write(entry.Body); err != nil {
				t.Fatalf("Write(%q) error = %v", entry.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buf.Bytes()
}

func buildGzipFixture(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip.Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip.Close() error = %v", err)
	}
	return buf.Bytes()
}
