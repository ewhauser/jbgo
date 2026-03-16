package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Method string

const (
	MethodGet     Method = "GET"
	MethodHead    Method = "HEAD"
	MethodPost    Method = "POST"
	MethodPut     Method = "PUT"
	MethodDelete  Method = "DELETE"
	MethodPatch   Method = "PATCH"
	MethodOptions Method = "OPTIONS"
)

const (
	defaultMaxRedirects    = 20
	defaultTimeout         = 30 * time.Second
	defaultMaxResponseSize = 10 << 20
)

var defaultAllowedMethods = []Method{MethodGet, MethodHead}

type Config struct {
	AllowedURLPrefixes []string
	AllowedMethods     []Method
	MaxRedirects       int
	Timeout            time.Duration
	MaxResponseBytes   int64
	DenyPrivateRanges  bool
}

type Request struct {
	Method          string
	URL             string
	Headers         map[string]string
	Body            []byte
	FollowRedirects bool
	Timeout         time.Duration
}

type Response struct {
	StatusCode int
	Status     string
	Headers    map[string]string
	Body       []byte
	URL        string
}

type Client interface {
	Do(context.Context, *Request) (*Response, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Option func(*HTTPClient)

type HTTPClient struct {
	cfg      resolvedConfig
	doer     HTTPDoer
	resolver Resolver
}

type resolvedConfig struct {
	allowedURLPrefixes []allowListEntry
	allowedMethods     map[Method]struct{}
	maxRedirects       int
	timeout            time.Duration
	maxResponseBytes   int64
	denyPrivateRanges  bool
}

type allowListEntry struct {
	origin     string
	pathPrefix string
}

type deniedMarker interface {
	networkDenied()
}

type AccessDeniedError struct {
	URL    string
	Reason string
}

func (e *AccessDeniedError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("network access denied: %s", e.URL)
	}
	return fmt.Sprintf("network access denied: %s: %s", e.Reason, e.URL)
}

func (*AccessDeniedError) networkDenied() {}

type MethodNotAllowedError struct {
	Method string
}

func (e *MethodNotAllowedError) Error() string {
	return fmt.Sprintf("network access denied: method %q not allowed", e.Method)
}

func (*MethodNotAllowedError) networkDenied() {}

type RedirectNotAllowedError struct {
	URL string
}

func (e *RedirectNotAllowedError) Error() string {
	return fmt.Sprintf("network redirect denied: %s", e.URL)
}

func (*RedirectNotAllowedError) networkDenied() {}

type TooManyRedirectsError struct {
	MaxRedirects int
}

func (e *TooManyRedirectsError) Error() string {
	return fmt.Sprintf("too many redirects (max %d)", e.MaxRedirects)
}

type ResponseTooLargeError struct {
	MaxBytes int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("response body too large (max %d bytes)", e.MaxBytes)
}

type InvalidConfigError struct {
	Problems []string
}

func (e *InvalidConfigError) Error() string {
	return fmt.Sprintf("invalid network config:\n%s", strings.Join(e.Problems, "\n"))
}

func IsDenied(err error) bool {
	var denied deniedMarker
	return errors.As(err, &denied)
}

func WithDoer(doer HTTPDoer) Option {
	return func(client *HTTPClient) {
		client.doer = doer
	}
}

func WithResolver(resolver Resolver) Option {
	return func(client *HTTPClient) {
		client.resolver = resolver
	}
}

func New(cfg *Config, opts ...Option) (*HTTPClient, error) {
	resolved, err := resolveConfig(cfg)
	if err != nil {
		return nil, err
	}

	client := &HTTPClient{
		cfg: resolved,
		doer: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		resolver: net.DefaultResolver,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client, nil
}

func resolveConfig(cfg *Config) (resolvedConfig, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	entries, problems := normalizeAllowList(cfg.AllowedURLPrefixes)
	if len(problems) > 0 {
		return resolvedConfig{}, &InvalidConfigError{Problems: problems}
	}
	if len(entries) == 0 {
		return resolvedConfig{}, &InvalidConfigError{Problems: []string{"at least one allowed URL prefix is required"}}
	}

	methods := cfg.AllowedMethods
	if len(methods) == 0 {
		methods = defaultAllowedMethods
	}
	allowedMethods := make(map[Method]struct{}, len(methods))
	for _, method := range methods {
		method = Method(strings.ToUpper(strings.TrimSpace(string(method))))
		if method == "" {
			continue
		}
		allowedMethods[method] = struct{}{}
	}
	if len(allowedMethods) == 0 {
		return resolvedConfig{}, &InvalidConfigError{Problems: []string{"at least one allowed HTTP method is required"}}
	}

	maxRedirects := cfg.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseSize
	}

	return resolvedConfig{
		allowedURLPrefixes: entries,
		allowedMethods:     allowedMethods,
		maxRedirects:       maxRedirects,
		timeout:            timeout,
		maxResponseBytes:   maxResponseBytes,
		denyPrivateRanges:  cfg.DenyPrivateRanges,
	}, nil
}

func (c *HTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		req = &Request{}
	}

	method := Method(strings.ToUpper(strings.TrimSpace(req.Method)))
	if method == "" {
		method = MethodGet
	}
	if _, ok := c.cfg.allowedMethods[method]; !ok {
		return nil, &MethodNotAllowedError{Method: string(method)}
	}

	currentURL := strings.TrimSpace(req.URL)
	if currentURL == "" {
		return nil, &AccessDeniedError{URL: currentURL, Reason: "missing URL"}
	}

	timeout := c.cfg.timeout
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	redirects := 0
	for {
		if err := c.checkURLAllowed(ctx, currentURL); err != nil {
			return nil, err
		}

		httpReq, err := http.NewRequestWithContext(ctx, string(method), currentURL, bytes.NewReader(req.Body))
		if err != nil {
			return nil, err
		}
		for name, value := range req.Headers {
			httpReq.Header.Set(name, value)
		}

		resp, err := c.doer.Do(httpReq)
		if err != nil {
			return nil, err
		}
		if isRedirect(resp.StatusCode) && req.FollowRedirects {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			if location == "" {
				return c.readResponse(currentURL, resp)
			}
			_ = resp.Body.Close()

			nextURL, err := resolveRedirectURL(currentURL, location)
			if err != nil {
				return nil, err
			}
			if err := c.checkURLAllowed(ctx, nextURL); err != nil {
				return nil, &RedirectNotAllowedError{URL: nextURL}
			}
			redirects++
			if redirects > c.cfg.maxRedirects {
				return nil, &TooManyRedirectsError{MaxRedirects: c.cfg.maxRedirects}
			}
			currentURL = nextURL
			continue
		}

		return c.readResponse(currentURL, resp)
	}
}

func (c *HTTPClient) readResponse(requestURL string, resp *http.Response) (*Response, error) {
	defer func() { _ = resp.Body.Close() }()

	if c.cfg.maxResponseBytes > 0 && resp.ContentLength > c.cfg.maxResponseBytes {
		return nil, &ResponseTooLargeError{MaxBytes: c.cfg.maxResponseBytes}
	}

	reader := io.Reader(resp.Body)
	if c.cfg.maxResponseBytes > 0 {
		reader = io.LimitReader(resp.Body, c.cfg.maxResponseBytes+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if c.cfg.maxResponseBytes > 0 && int64(len(body)) > c.cfg.maxResponseBytes {
		return nil, &ResponseTooLargeError{MaxBytes: c.cfg.maxResponseBytes}
	}

	headers := make(map[string]string, len(resp.Header))
	for name, values := range resp.Header {
		headers[name] = strings.Join(values, ", ")
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Headers:    headers,
		Body:       body,
		URL:        requestURL,
	}, nil
}

func (c *HTTPClient) checkURLAllowed(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return &AccessDeniedError{URL: rawURL, Reason: "invalid URL"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return &AccessDeniedError{URL: rawURL, Reason: "only http and https are allowed"}
	}
	if parsed.Host == "" {
		return &AccessDeniedError{URL: rawURL, Reason: "missing host"}
	}
	if !urlAllowed(parsed, c.cfg.allowedURLPrefixes) {
		return &AccessDeniedError{URL: rawURL, Reason: "URL not in allowlist"}
	}
	if c.cfg.denyPrivateRanges {
		if err := c.checkPrivateHost(ctx, parsed); err != nil {
			return err
		}
	}
	return nil
}

func (c *HTTPClient) checkPrivateHost(ctx context.Context, parsed *url.URL) error {
	host := parsed.Hostname()
	if isPrivateHostname(host) {
		return &AccessDeniedError{URL: parsed.String(), Reason: "private or loopback host blocked"}
	}

	if ip := net.ParseIP(host); ip != nil {
		return nil
	}

	addresses, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.Err == "no such host") {
			return nil
		}
		return &AccessDeniedError{URL: parsed.String(), Reason: "DNS resolution failed for private range check"}
	}
	for _, address := range addresses {
		if isPrivateIP(address.IP) {
			return &AccessDeniedError{URL: parsed.String(), Reason: "host resolves to private or loopback IP"}
		}
	}
	return nil
}

func normalizeAllowList(entries []string) (normalized []allowListEntry, problems []string) {
	if len(entries) == 0 {
		return nil, nil
	}

	out := make([]allowListEntry, 0, len(entries))
	problems = make([]string, 0)
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parsed, err := url.Parse(entry)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			problems = append(problems, fmt.Sprintf("invalid allowlist URL %q", entry))
			continue
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			problems = append(problems, fmt.Sprintf("allowlist URL must use http or https: %q", entry))
			continue
		}
		if parsed.User != nil {
			problems = append(problems, fmt.Sprintf("allowlist URL must not include userinfo: %q", entry))
			continue
		}
		normalized := allowListEntry{
			origin:     parsed.Scheme + "://" + parsed.Host,
			pathPrefix: parsed.EscapedPath(),
		}
		if normalized.pathPrefix == "" {
			normalized.pathPrefix = "/"
		}
		key := normalized.origin + normalized.pathPrefix
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out, problems
}

func urlAllowed(target *url.URL, entries []allowListEntry) bool {
	origin := target.Scheme + "://" + target.Host
	pathname := target.EscapedPath()
	if pathname == "" {
		pathname = "/"
	}

	for _, entry := range entries {
		if origin != entry.origin {
			continue
		}
		if allowListPathMatches(pathname, entry.pathPrefix) {
			return true
		}
	}
	return false
}

func allowListPathMatches(pathname, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	if pathname == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(pathname, prefix)
	}
	return strings.HasPrefix(pathname, prefix+"/")
}

func resolveRedirectURL(currentURL, location string) (string, error) {
	base, err := url.Parse(currentURL)
	if err != nil {
		return "", err
	}
	next, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(next).String(), nil
}

func isRedirect(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func isPrivateHostname(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return isPrivateIP(ip)
}

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast()
}
