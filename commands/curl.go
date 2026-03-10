package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	jbfs "github.com/cadencerpm/just-bash-go/fs"
	"github.com/cadencerpm/just-bash-go/network"
)

type Curl struct{}

type curlOptions struct {
	fail            bool
	followRedirects bool
	head            bool
	includeHeaders  bool
	showError       bool
	silent          bool
	method          string
	outputFile      string
	url             string
	data            string
	headers         map[string]string
}

const curlHelpText = `usage: curl [OPTIONS] URL

  -L, --location         follow redirects
  -I, --head             use HEAD and print response headers
  -i, --include          include response headers in output
  -X, --request METHOD   specify request method
  -H, --header HEADER    add request header
  -d, --data DATA        send request body (defaults method to POST)
  -o, --output FILE      write response body to FILE
  -f, --fail             fail on HTTP status >= 400
  -s, --silent           accepted for compatibility
  -S, --show-error       show errors even with -s
      --help             show this help
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
	if inv.Net == nil {
		return exitf(inv, 1, "curl: network client not available")
	}

	method := opts.method
	if method == "" {
		switch {
		case opts.head:
			method = string(network.MethodHead)
		case opts.data != "":
			method = string(network.MethodPost)
		default:
			method = string(network.MethodGet)
		}
	}

	urlValue := opts.url
	if !strings.HasPrefix(urlValue, "http://") && !strings.HasPrefix(urlValue, "https://") {
		urlValue = "https://" + urlValue
	}

	resp, err := inv.Net.Do(ctx, &network.Request{
		Method:          method,
		URL:             urlValue,
		Headers:         opts.headers,
		Body:            []byte(opts.data),
		FollowRedirects: opts.followRedirects,
	})
	if err != nil {
		if network.IsDenied(err) {
			return exitf(inv, 126, "curl: %v", err)
		}
		return exitf(inv, 1, "curl: %v", err)
	}

	if opts.fail && resp.StatusCode >= 400 {
		if !opts.silent || opts.showError {
			return exitf(inv, 22, "curl: (22) The requested URL returned error: %d", resp.StatusCode)
		}
		return &ExitError{Code: 22}
	}

	var output bytes.Buffer
	if opts.head || opts.includeHeaders {
		writeCurlHeaders(&output, resp)
		if !opts.head {
			output.WriteString("\r\n")
		}
	}
	if !opts.head {
		output.Write(resp.Body)
	}

	if opts.outputFile != "" {
		if err := writeFileContents(ctx, inv, jbfs.Resolve(inv.Dir, opts.outputFile), resp.Body, 0o644); err != nil {
			return err
		}
		if opts.includeHeaders || opts.head {
			if _, err := inv.Stdout.Write(output.Bytes()[:output.Len()-len(resp.Body)]); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	if _, err := inv.Stdout.Write(output.Bytes()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseCurlArgs(inv *Invocation) (*curlOptions, error) {
	args := inv.Args
	opts := &curlOptions{
		headers: make(map[string]string),
	}

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}

		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := splitLongFlag(arg)
			switch name {
			case "help":
				_, _ = io.WriteString(inv.Stdout, curlHelpText)
				return nil, nil
			case "location":
				opts.followRedirects = true
			case "head":
				opts.head = true
			case "include":
				opts.includeHeaders = true
			case "fail":
				opts.fail = true
			case "silent":
				opts.silent = true
			case "show-error":
				opts.showError = true
			case "request":
				method, rest, err := requireFlagValue(inv, arg, value, hasValue, args[1:])
				if err != nil {
					return nil, err
				}
				opts.method = strings.ToUpper(method)
				args = rest
				continue
			case "header":
				header, rest, err := requireFlagValue(inv, arg, value, hasValue, args[1:])
				if err != nil {
					return nil, err
				}
				if err := parseCurlHeader(opts, header); err != nil {
					return nil, exitf(inv, 2, "curl: %v", err)
				}
				args = rest
				continue
			case "data":
				data, rest, err := requireFlagValue(inv, arg, value, hasValue, args[1:])
				if err != nil {
					return nil, err
				}
				opts.data = data
				args = rest
				continue
			case "output":
				file, rest, err := requireFlagValue(inv, arg, value, hasValue, args[1:])
				if err != nil {
					return nil, err
				}
				opts.outputFile = file
				args = rest
				continue
			default:
				return nil, exitf(inv, 2, "curl: unsupported flag %s", arg)
			}
			args = args[1:]
			continue
		}

		rest, err := parseCurlShortFlags(inv, opts, arg, args[1:])
		if err != nil {
			return nil, err
		}
		args = rest
	}

	if len(args) == 0 {
		return nil, exitf(inv, 2, "curl: no URL specified")
	}
	opts.url = args[0]
	return opts, nil
}

func parseCurlShortFlags(inv *Invocation, opts *curlOptions, current string, rest []string) ([]string, error) {
	flags := current[1:]
	for flags != "" {
		switch flags[0] {
		case 'L':
			opts.followRedirects = true
			flags = flags[1:]
		case 'I':
			opts.head = true
			flags = flags[1:]
		case 'i':
			opts.includeHeaders = true
			flags = flags[1:]
		case 'f':
			opts.fail = true
			flags = flags[1:]
		case 's':
			opts.silent = true
			flags = flags[1:]
		case 'S':
			opts.showError = true
			flags = flags[1:]
		case 'X':
			value, remaining, err := shortFlagValue(inv, current, flags[1:], rest, "X")
			if err != nil {
				return nil, err
			}
			opts.method = strings.ToUpper(value)
			return remaining, nil
		case 'H':
			value, remaining, err := shortFlagValue(inv, current, flags[1:], rest, "H")
			if err != nil {
				return nil, err
			}
			if err := parseCurlHeader(opts, value); err != nil {
				return nil, exitf(inv, 2, "curl: %v", err)
			}
			return remaining, nil
		case 'd':
			value, remaining, err := shortFlagValue(inv, current, flags[1:], rest, "d")
			if err != nil {
				return nil, err
			}
			opts.data = value
			return remaining, nil
		case 'o':
			value, remaining, err := shortFlagValue(inv, current, flags[1:], rest, "o")
			if err != nil {
				return nil, err
			}
			opts.outputFile = value
			return remaining, nil
		default:
			return nil, exitf(inv, 2, "curl: unsupported flag -%c", flags[0])
		}
	}
	return rest, nil
}

func splitLongFlag(arg string) (name, value string, hasValue bool) {
	trimmed := strings.TrimPrefix(arg, "--")
	name, value, hasValue = strings.Cut(trimmed, "=")
	return name, value, hasValue
}

func requireFlagValue(inv *Invocation, flag, inline string, hasInline bool, rest []string) (value string, remaining []string, err error) {
	if hasInline {
		return inline, rest, nil
	}
	if len(rest) == 0 {
		return "", nil, exitf(inv, 2, "curl: option %s requires an argument", flag)
	}
	return rest[0], rest[1:], nil
}

func shortFlagValue(inv *Invocation, current, inline string, rest []string, flag string) (value string, remaining []string, err error) {
	if inline != "" {
		return inline, rest, nil
	}
	if len(rest) == 0 {
		return "", nil, exitf(inv, 2, "curl: option -%s requires an argument", flag)
	}
	return rest[0], rest[1:], nil
}

func parseCurlHeader(opts *curlOptions, header string) error {
	name, value, ok := strings.Cut(header, ":")
	if !ok {
		return errors.New("invalid header format")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("invalid header name")
	}
	opts.headers[name] = strings.TrimSpace(value)
	return nil
}

func writeCurlHeaders(buf *bytes.Buffer, resp *network.Response) {
	fmt.Fprintf(buf, "HTTP/1.1 %s\r\n", resp.Status)
	keys := make([]string, 0, len(resp.Headers))
	for name := range resp.Headers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		value := resp.Headers[name]
		fmt.Fprintf(buf, "%s: %s\r\n", name, value)
	}
}

var _ Command = (*Curl)(nil)
