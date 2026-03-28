package conformance

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	gbruntime "github.com/ewhauser/gbash/internal/runtime"
	"github.com/ewhauser/gbash/internal/testutil"
	"github.com/ewhauser/gbash/network"
)

func newCurlSuiteConfig(t *testing.T) SuiteConfig {
	t.Helper()

	curlPath := testutil.RequireNixCurl(t)
	server := newCurlConformanceServer(t)
	t.Cleanup(server.Close)

	baseURL := server.URL
	return SuiteConfig{
		Name:         "curl",
		SpecDir:      "curl",
		BinDir:       "bin",
		FixtureDirs:  []string{"fixtures/spec"},
		ManifestPath: "manifest.json",
		OracleMode:   OracleBash,
		Env: map[string]string{
			"GBASH_CONFORMANCE_CURL_BASE_URL": baseURL,
		},
		ExtraBinaries: map[string]string{
			"curl": curlPath,
		},
		GBashConfig: &gbruntime.Config{
			Network: &network.Config{
				AllowedURLPrefixes: []string{baseURL + "/"},
				AllowedMethods: []network.Method{
					network.MethodGet,
					network.MethodHead,
					network.MethodPost,
					network.MethodPut,
				},
				DenyPrivateRanges: false,
			},
		},
	}
}

func newCurlConformanceServer(t testing.TB) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello from curl")
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("http://%s/plain", r.Host), http.StatusFound)
	})
	mux.HandleFunc("/inspect/request", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintf(
			w,
			"authorization=%s\nuser-agent=%s\nreferer=%s\ncookie=%s\n",
			r.Header.Get("Authorization"),
			r.Header.Get("User-Agent"),
			r.Header.Get("Referer"),
			r.Header.Get("Cookie"),
		)
	})
	mux.HandleFunc("/echo/body", func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		w.Header().Set("Content-Type", "text/plain")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/inspect/form", func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		w.Header().Set("Content-Type", "text/plain")
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = part.Close() }()

		body, err := io.ReadAll(part)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprintf(
			w,
			"method=%s\nfield=%s\nfilename=%s\ncontent-type=%s\nbody=%s\n",
			r.Method,
			part.FormName(),
			part.FileName(),
			part.Header.Get("Content-Type"),
			string(body),
		)
	})
	mux.HandleFunc("/files/report.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "report payload")
	})
	mux.HandleFunc("/include", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Test", "include")
		_, _ = io.WriteString(w, "included-body\n")
	})
	mux.HandleFunc("/head", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Test", "head-only")
		_, _ = io.WriteString(w, "head-body\n")
	})
	mux.HandleFunc("/status/404", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "missing\n")
	})

	return httptest.NewServer(mux)
}
