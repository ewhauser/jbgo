package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	stdfs "io/fs"
	"maps"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/network"
)

type Curl struct{}

type curlOptions struct {
	method          string
	headers         map[string]string
	data            string
	hasData         bool
	dataBinary      bool
	formFields      []curlFormField
	user            string
	uploadFile      string
	cookieJar       string
	outputFile      string
	useRemoteName   bool
	headOnly        bool
	includeHeaders  bool
	silent          bool
	showError       bool
	failSilently    bool
	followRedirects bool
	writeOut        string
	verbose         bool
	timeout         time.Duration
	url             string
}

type curlFormField struct {
	name        string
	value       string
	filename    string
	contentType string
}

const (
	curlFormBoundary = "----CurlFormBoundaryjbgo"
)

const curlHelpText = `usage: curl [OPTIONS] URL

  -X, --request METHOD       HTTP method (GET, POST, PUT, DELETE, etc.)
  -H, --header HEADER        add header (can be used multiple times)
  -d, --data DATA            HTTP POST data
      --data-raw DATA        HTTP POST data (no @ interpretation)
      --data-binary DATA     HTTP POST binary data
      --data-urlencode DATA  URL-encode and POST data
  -F, --form NAME=VALUE      multipart form data
  -u, --user USER:PASS       HTTP authentication
  -A, --user-agent STR       set User-Agent header
  -e, --referer URL          set Referer header
  -b, --cookie DATA          send cookies
  -c, --cookie-jar FILE      save cookies to file
  -T, --upload-file FILE     upload file (defaults method to PUT)
  -o, --output FILE          write output to file
  -O, --remote-name          write to a file named from the URL
  -I, --head                 show headers only (HEAD request)
  -i, --include              include response headers in output
  -s, --silent               silent mode (no progress)
  -S, --show-error           show errors even when silent
  -f, --fail                 fail on HTTP status >= 400
  -L, --location             follow redirects (default)
      --max-redirs NUM       accepted for compatibility
  -m, --max-time SECS        maximum request time
      --connect-timeout SECS accepted as an overall timeout fallback
  -w, --write-out FMT        output format after completion
  -v, --verbose              verbose output
      --help                 show this help
`

func NewCurl() *Curl {
	return &Curl{}
}

func (c *Curl) Name() string {
	return "curl"
}

func (c *Curl) Run(ctx context.Context, inv *Invocation) error {
	opts, err := parseCurlArgs(inv)
	if err != nil {
		return err
	}
	if opts == nil {
		return nil
	}
	if inv.Fetch == nil {
		return exitf(inv, 1, "curl: internal error: network client not available")
	}

	requestURL := opts.url
	if !strings.HasPrefix(requestURL, "http://") && !strings.HasPrefix(requestURL, "https://") {
		requestURL = "https://" + requestURL
	}

	body, contentType, err := prepareCurlRequestBody(ctx, inv, opts)
	if err != nil {
		return err
	}
	headers := prepareCurlHeaders(opts, contentType)

	resp, err := inv.Fetch(ctx, &FetchRequest{
		Method:          opts.method,
		URL:             requestURL,
		Headers:         headers,
		Body:            body,
		FollowRedirects: opts.followRedirects,
		Timeout:         opts.timeout,
	})
	if err != nil {
		return curlRequestError(inv, opts, err)
	}

	if err := saveCurlCookies(ctx, inv, opts, resp); err != nil {
		return err
	}

	if opts.failSilently && resp.StatusCode >= 400 {
		if !opts.silent || opts.showError {
			return exitf(inv, 22, "curl: (22) The requested URL returned error: %d", resp.StatusCode)
		}
		return &ExitError{Code: 22}
	}

	output := buildCurlOutput(opts, resp, requestURL)
	if opts.outputFile != "" || opts.useRemoteName {
		filename := opts.outputFile
		if filename == "" {
			filename = extractCurlFilename(requestURL)
		}
		if err := writeFileContents(ctx, inv, gbfs.Resolve(inv.Cwd, filename), curlOutputFileBody(opts, resp), 0o644); err != nil {
			return err
		}
		if !opts.verbose {
			output.Reset()
		}
		if opts.writeOut != "" {
			output.Reset()
			output.WriteString(applyCurlWriteOut(opts.writeOut, resp))
		}
	}

	if _, err := inv.Stdout.Write(output.Bytes()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseCurlArgs(inv *Invocation) (*curlOptions, error) {
	opts := &curlOptions{
		method:          "GET",
		headers:         make(map[string]string),
		followRedirects: true,
	}
	impliesPost := false
	args := inv.Args

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" {
			_, _ = inv.Stdout.Write([]byte(curlHelpText))
			return nil, nil
		}
		if arg == "--" {
			if i+1 < len(args) {
				opts.url = args[i+1]
			}
			break
		}

		switch {
		case arg == "-X" || arg == "--request":
			opts.method = curlNextArg(args, &i, "GET")
		case strings.HasPrefix(arg, "-X"):
			opts.method = arg[2:]
		case strings.HasPrefix(arg, "--request="):
			opts.method = arg[len("--request="):]

		case arg == "-H" || arg == "--header":
			parseCurlHeader(opts.headers, curlNextArg(args, &i, ""))
		case strings.HasPrefix(arg, "--header="):
			parseCurlHeader(opts.headers, arg[len("--header="):])

		case arg == "-d" || arg == "--data" || arg == "--data-raw":
			opts.data = curlNextArg(args, &i, "")
			opts.hasData = true
			impliesPost = true
		case strings.HasPrefix(arg, "-d"):
			opts.data = arg[2:]
			opts.hasData = true
			impliesPost = true
		case strings.HasPrefix(arg, "--data="):
			opts.data = arg[len("--data="):]
			opts.hasData = true
			impliesPost = true
		case strings.HasPrefix(arg, "--data-raw="):
			opts.data = arg[len("--data-raw="):]
			opts.hasData = true
			impliesPost = true

		case arg == "--data-binary":
			opts.data = curlNextArg(args, &i, "")
			opts.hasData = true
			opts.dataBinary = true
			impliesPost = true
		case strings.HasPrefix(arg, "--data-binary="):
			opts.data = arg[len("--data-binary="):]
			opts.hasData = true
			opts.dataBinary = true
			impliesPost = true

		case arg == "--data-urlencode":
			curlAppendURLEncodedData(opts, curlNextArg(args, &i, ""))
			impliesPost = true
		case strings.HasPrefix(arg, "--data-urlencode="):
			curlAppendURLEncodedData(opts, arg[len("--data-urlencode="):])
			impliesPost = true

		case arg == "-F" || arg == "--form":
			if field, ok := parseCurlFormField(curlNextArg(args, &i, "")); ok {
				opts.formFields = append(opts.formFields, field)
			}
			impliesPost = true
		case strings.HasPrefix(arg, "--form="):
			if field, ok := parseCurlFormField(arg[len("--form="):]); ok {
				opts.formFields = append(opts.formFields, field)
			}
			impliesPost = true

		case arg == "-u" || arg == "--user":
			opts.user = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "-u"):
			opts.user = arg[2:]
		case strings.HasPrefix(arg, "--user="):
			opts.user = arg[len("--user="):]

		case arg == "-A" || arg == "--user-agent":
			opts.headers["User-Agent"] = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "-A"):
			opts.headers["User-Agent"] = arg[2:]
		case strings.HasPrefix(arg, "--user-agent="):
			opts.headers["User-Agent"] = arg[len("--user-agent="):]

		case arg == "-e" || arg == "--referer":
			opts.headers["Referer"] = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "-e"):
			opts.headers["Referer"] = arg[2:]
		case strings.HasPrefix(arg, "--referer="):
			opts.headers["Referer"] = arg[len("--referer="):]

		case arg == "-b" || arg == "--cookie":
			opts.headers["Cookie"] = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "-b"):
			opts.headers["Cookie"] = arg[2:]
		case strings.HasPrefix(arg, "--cookie="):
			opts.headers["Cookie"] = arg[len("--cookie="):]

		case arg == "-c" || arg == "--cookie-jar":
			opts.cookieJar = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "--cookie-jar="):
			opts.cookieJar = arg[len("--cookie-jar="):]

		case arg == "-T" || arg == "--upload-file":
			opts.uploadFile = curlNextArg(args, &i, "")
			if opts.method == "GET" {
				opts.method = "PUT"
			}
		case strings.HasPrefix(arg, "--upload-file="):
			opts.uploadFile = arg[len("--upload-file="):]
			if opts.method == "GET" {
				opts.method = "PUT"
			}

		case arg == "-m" || arg == "--max-time":
			opts.timeout = parseCurlTimeout(curlNextArg(args, &i, ""))
		case strings.HasPrefix(arg, "--max-time="):
			opts.timeout = parseCurlTimeout(arg[len("--max-time="):])

		case arg == "--connect-timeout":
			if opts.timeout <= 0 {
				opts.timeout = parseCurlTimeout(curlNextArg(args, &i, ""))
			} else {
				i++
			}
		case strings.HasPrefix(arg, "--connect-timeout="):
			if opts.timeout <= 0 {
				opts.timeout = parseCurlTimeout(arg[len("--connect-timeout="):])
			}

		case arg == "-o" || arg == "--output":
			opts.outputFile = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "--output="):
			opts.outputFile = arg[len("--output="):]
		case arg == "-O" || arg == "--remote-name":
			opts.useRemoteName = true

		case arg == "-I" || arg == "--head":
			opts.headOnly = true
			opts.method = "HEAD"
		case arg == "-i" || arg == "--include":
			opts.includeHeaders = true
		case arg == "-s" || arg == "--silent":
			opts.silent = true
		case arg == "-S" || arg == "--show-error":
			opts.showError = true
		case arg == "-f" || arg == "--fail":
			opts.failSilently = true
		case arg == "-L" || arg == "--location":
			opts.followRedirects = true
		case arg == "--max-redirs":
			i++
		case strings.HasPrefix(arg, "--max-redirs="):
			// Accepted for compatibility. Redirect depth is enforced by network.Config.

		case arg == "-w" || arg == "--write-out":
			opts.writeOut = curlNextArg(args, &i, "")
		case strings.HasPrefix(arg, "--write-out="):
			opts.writeOut = arg[len("--write-out="):]
		case arg == "-v" || arg == "--verbose":
			opts.verbose = true

		case strings.HasPrefix(arg, "--"):
			return nil, exitf(inv, 1, "curl: unrecognized option '%s'", arg)
		case strings.HasPrefix(arg, "-") && arg != "-":
			for _, ch := range arg[1:] {
				switch ch {
				case 's':
					opts.silent = true
				case 'S':
					opts.showError = true
				case 'f':
					opts.failSilently = true
				case 'L':
					opts.followRedirects = true
				case 'I':
					opts.headOnly = true
					opts.method = "HEAD"
				case 'i':
					opts.includeHeaders = true
				case 'O':
					opts.useRemoteName = true
				case 'v':
					opts.verbose = true
				default:
					return nil, exitf(inv, 1, "curl: invalid option -- '%c'", ch)
				}
			}
		default:
			opts.url = arg
		}
	}

	if impliesPost && opts.method == "GET" {
		opts.method = "POST"
	}
	if opts.url == "" {
		return nil, exitf(inv, 2, "curl: no URL specified")
	}
	return opts, nil
}

func curlNextArg(args []string, i *int, fallback string) string {
	next := *i + 1
	if next >= len(args) {
		return fallback
	}
	*i = next
	return args[next]
}

func curlAppendURLEncodedData(opts *curlOptions, value string) {
	if opts.hasData && opts.data != "" {
		opts.data += "&"
	}
	opts.data += encodeCurlFormData(value)
	opts.hasData = true
}

func parseCurlTimeout(value string) time.Duration {
	secs, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

func parseCurlHeader(headers map[string]string, header string) {
	colonIndex := strings.IndexByte(header, ':')
	if colonIndex <= 0 {
		return
	}
	name := strings.TrimSpace(header[:colonIndex])
	if name == "" {
		return
	}
	headers[name] = strings.TrimSpace(header[colonIndex+1:])
}

func parseCurlFormField(spec string) (curlFormField, bool) {
	before, after, ok := strings.Cut(spec, "=")
	if !ok {
		return curlFormField{}, false
	}

	field := curlFormField{
		name:  before,
		value: after,
	}

	if idx := strings.LastIndex(field.value, ";type="); idx >= 0 && !strings.Contains(field.value[idx+len(";type="):], ";") {
		field.contentType = field.value[idx+len(";type="):]
		field.value = field.value[:idx]
	}
	if idx := strings.Index(field.value, ";filename="); idx >= 0 {
		filename := field.value[idx+len(";filename="):]
		if cut := strings.IndexByte(filename, ';'); cut >= 0 {
			filename = filename[:cut]
		}
		field.filename = filename
		field.value = strings.Replace(field.value, ";filename="+filename, "", 1)
	}
	if strings.HasPrefix(field.value, "@") || strings.HasPrefix(field.value, "<") {
		if field.filename == "" {
			field.filename = path.Base(field.value[1:])
		}
	}
	return field, true
}

func encodeCurlFormData(input string) string {
	before, after, ok := strings.Cut(input, "=")
	if ok {
		name := before
		value := after
		if name != "" {
			return curlURIComponent(name) + "=" + curlURIComponent(value)
		}
		return curlURIComponent(value)
	}
	return curlURIComponent(input)
}

func curlURIComponent(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func prepareCurlRequestBody(ctx context.Context, inv *Invocation, opts *curlOptions) (body []byte, contentType string, err error) {
	if opts.uploadFile != "" {
		data, _, err := readAllFile(ctx, inv, opts.uploadFile)
		if err != nil {
			return nil, "", err
		}
		return data, "", nil
	}

	if len(opts.formFields) > 0 {
		fileContents := make(map[string][]byte, len(opts.formFields))
		for _, field := range opts.formFields {
			if !strings.HasPrefix(field.value, "@") && !strings.HasPrefix(field.value, "<") {
				continue
			}
			target := field.value[1:]
			data, _, err := readAllFile(ctx, inv, target)
			if err != nil {
				if errors.Is(err, stdfs.ErrNotExist) {
					fileContents[target] = nil
					continue
				}
				return nil, "", err
			}
			fileContents[target] = data
		}
		return generateCurlMultipartBody(opts.formFields, fileContents), "multipart/form-data; boundary=" + curlFormBoundary, nil
	}

	if opts.hasData {
		return []byte(opts.data), "", nil
	}

	return nil, "", nil
}

func generateCurlMultipartBody(fields []curlFormField, fileContents map[string][]byte) []byte {
	var buf bytes.Buffer

	for _, field := range fields {
		value := []byte(field.value)
		if strings.HasPrefix(field.value, "@") || strings.HasPrefix(field.value, "<") {
			value = fileContents[field.value[1:]]
		}

		fmt.Fprintf(&buf, "--%s\r\n", curlFormBoundary)
		if field.filename != "" {
			fmt.Fprintf(&buf, "Content-Disposition: form-data; name=%q; filename=%q\r\n", field.name, field.filename)
			if field.contentType != "" {
				fmt.Fprintf(&buf, "Content-Type: %s\r\n", field.contentType)
			}
		} else {
			fmt.Fprintf(&buf, "Content-Disposition: form-data; name=%q\r\n", field.name)
		}
		buf.WriteString("\r\n")
		buf.Write(value)
		buf.WriteString("\r\n")
	}

	fmt.Fprintf(&buf, "--%s--\r\n", curlFormBoundary)
	return buf.Bytes()
}

func prepareCurlHeaders(opts *curlOptions, contentType string) map[string]string {
	headers := make(map[string]string, len(opts.headers)+2)
	maps.Copy(headers, opts.headers)
	if opts.user != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(opts.user))
	}
	if contentType != "" && !curlHasHeader(headers, "Content-Type") {
		headers["Content-Type"] = contentType
	}
	return headers
}

func curlHasHeader(headers map[string]string, name string) bool {
	for existing := range headers {
		if strings.EqualFold(existing, name) {
			return true
		}
	}
	return false
}

func saveCurlCookies(ctx context.Context, inv *Invocation, opts *curlOptions, resp *network.Response) error {
	if opts.cookieJar == "" {
		return nil
	}
	setCookie := curlHeaderValue(resp.Headers, "Set-Cookie")
	if setCookie == "" {
		return nil
	}
	return writeFileContents(ctx, inv, gbfs.Resolve(inv.Cwd, opts.cookieJar), []byte(setCookie), 0o644)
}

func buildCurlOutput(opts *curlOptions, resp *network.Response, requestURL string) *bytes.Buffer {
	var output bytes.Buffer

	if opts.verbose {
		fmt.Fprintf(&output, "> %s %s\n", opts.method, requestURL)
		curlWriteVerboseHeaders(&output, '>', opts.headers)
		output.WriteString(">\n")
		fmt.Fprintf(&output, "< HTTP/1.1 %d %s\n", resp.StatusCode, curlStatusText(resp))
		curlWriteVerboseHeaders(&output, '<', resp.Headers)
		output.WriteString("<\n")
	}

	if opts.includeHeaders && !opts.verbose {
		fmt.Fprintf(&output, "HTTP/1.1 %d %s\r\n", resp.StatusCode, curlStatusText(resp))
		output.WriteString(formatCurlHeaders(resp.Headers))
		output.WriteString("\r\n\r\n")
	}

	if !opts.headOnly {
		output.Write(resp.Body)
	} else if !opts.includeHeaders && !opts.verbose {
		fmt.Fprintf(&output, "HTTP/1.1 %d %s\r\n", resp.StatusCode, curlStatusText(resp))
		output.WriteString(formatCurlHeaders(resp.Headers))
		output.WriteString("\r\n")
	}

	if opts.writeOut != "" {
		output.WriteString(applyCurlWriteOut(opts.writeOut, resp))
	}

	return &output
}

func curlWriteVerboseHeaders(buf *bytes.Buffer, prefix rune, headers map[string]string) {
	keys := make([]string, 0, len(headers))
	for name := range headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		fmt.Fprintf(buf, "%c %s: %s\n", prefix, name, headers[name])
	}
}

func curlStatusText(resp *network.Response) string {
	if resp == nil {
		return ""
	}
	status := strings.TrimSpace(resp.Status)
	if status == "" {
		return http.StatusText(resp.StatusCode)
	}
	if after, ok := strings.CutPrefix(status, strconv.Itoa(resp.StatusCode)); ok {
		return strings.TrimSpace(after)
	}
	return status
}

func formatCurlHeaders(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for name := range headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, name := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", name, headers[name]))
	}
	return strings.Join(lines, "\r\n")
}

func extractCurlFilename(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "index.html"
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return "index.html"
	}
	return name
}

func applyCurlWriteOut(format string, resp *network.Response) string {
	output := format
	output = strings.ReplaceAll(output, "%{http_code}", strconv.Itoa(resp.StatusCode))
	output = strings.ReplaceAll(output, "%{content_type}", curlHeaderValue(resp.Headers, "Content-Type"))
	output = strings.ReplaceAll(output, "%{url_effective}", resp.URL)
	output = strings.ReplaceAll(output, "%{size_download}", strconv.Itoa(len(resp.Body)))
	output = strings.ReplaceAll(output, "\\n", "\n")
	return output
}

func curlHeaderValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func curlOutputFileBody(opts *curlOptions, resp *network.Response) []byte {
	if opts.headOnly {
		return nil
	}
	return resp.Body
}

func curlRequestError(inv *Invocation, opts *curlOptions, err error) error {
	exitCode := 1

	var methodErr *network.MethodNotAllowedError
	var redirectErr *network.RedirectNotAllowedError
	var redirectLimitErr *network.TooManyRedirectsError
	switch {
	case errors.As(err, &methodErr):
		exitCode = 3
	case errors.As(err, &redirectErr):
		exitCode = 47
	case errors.As(err, &redirectLimitErr):
		exitCode = 47
	case errors.Is(err, context.DeadlineExceeded):
		exitCode = 28
	case strings.Contains(strings.ToLower(err.Error()), "aborted"):
		exitCode = 28
	case network.IsDenied(err):
		exitCode = 7
	}

	if !opts.silent || opts.showError {
		return exitf(inv, exitCode, "curl: (%d) %s", exitCode, curlErrorMessage(err))
	}
	return &ExitError{Code: exitCode}
}

func curlErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var redirectErr *network.RedirectNotAllowedError
	switch {
	case errors.As(err, &redirectErr):
		return "Redirect target not in allow-list: " + redirectErr.URL
	case errors.Is(err, context.DeadlineExceeded):
		return "The operation was aborted"
	case strings.Contains(strings.ToLower(err.Error()), "aborted"):
		return err.Error()
	}
	message := err.Error()
	if message == "" {
		return ""
	}
	return strings.ToUpper(message[:1]) + message[1:]
}

var _ Command = (*Curl)(nil)
