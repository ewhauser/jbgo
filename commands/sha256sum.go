package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/ewhauser/gbash/policy"
)

type SHA256Sum struct{}

var sha256sumCheckLinePattern = regexp.MustCompile(`^([a-fA-F0-9]+)\s+[* ]?(.+)$`)

const sha256sumHelpText = `sha256sum - compute SHA256 message digest

Usage: sha256sum [OPTION]... [FILE]...

Options:
  -c, --check    read checksums from FILEs and check them
      --help     display this help and exit
`

func NewSHA256Sum() *SHA256Sum {
	return &SHA256Sum{}
}

func (c *SHA256Sum) Name() string {
	return "sha256sum"
}

func (c *SHA256Sum) Run(ctx context.Context, inv *Invocation) error {
	if slices.Contains(inv.Args, "--help") {
		if _, err := io.WriteString(inv.Stdout, sha256sumHelpText); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	checkMode, files, err := parseSHA256SumArgs(inv)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		files = []string{"-"}
	}

	reader := &sha256sumReader{
		ctx: ctx,
		inv: inv,
	}
	if checkMode {
		return c.runCheckMode(reader, files)
	}
	return c.runDigestMode(reader, files)
}

func (c *SHA256Sum) runDigestMode(reader *sha256sumReader, files []string) error {
	exitCode := 0
	for _, name := range files {
		data, err := reader.read(name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			if _, writeErr := fmt.Fprintf(reader.inv.Stdout, "sha256sum: %s: No such file or directory\n", name); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			exitCode = 1
			continue
		}
		sum := sha256.Sum256(data)
		if _, err := fmt.Fprintf(reader.inv.Stdout, "%x  %s\n", sum, name); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *SHA256Sum) runCheckMode(reader *sha256sumReader, files []string) error {
	failed := 0
	for _, name := range files {
		data, err := reader.read(name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			return exitf(reader.inv, 1, "sha256sum: %s: No such file or directory", name)
		}

		for line := range strings.SplitSeq(string(data), "\n") {
			match := sha256sumCheckLinePattern.FindStringSubmatch(line)
			if match == nil {
				continue
			}

			expected := strings.ToLower(match[1])
			target := match[2]
			targetData, err := reader.read(target)
			if err != nil {
				if policy.IsDenied(err) {
					return err
				}
				if _, writeErr := fmt.Fprintf(reader.inv.Stdout, "%s: FAILED open or read\n", target); writeErr != nil {
					return &ExitError{Code: 1, Err: writeErr}
				}
				failed++
				continue
			}

			actual := fmt.Sprintf("%x", sha256.Sum256(targetData))
			status := "OK"
			if actual != expected {
				status = "FAILED"
				failed++
			}
			if _, err := fmt.Fprintf(reader.inv.Stdout, "%s: %s\n", target, status); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
	}

	if failed > 0 {
		suffix := "s"
		if failed == 1 {
			suffix = ""
		}
		if _, err := fmt.Fprintf(reader.inv.Stdout, "sha256sum: WARNING: %d computed checksum%s did NOT match\n", failed, suffix); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return &ExitError{Code: 1}
	}
	return nil
}

func parseSHA256SumArgs(inv *Invocation) (checkMode bool, files []string, err error) {
	for _, arg := range inv.Args {
		switch arg {
		case "-c", "--check":
			checkMode = true
		case "-b", "-t", "--binary", "--text":
		default:
			if strings.HasPrefix(arg, "-") && arg != "-" {
				return false, nil, writeSHA256SumUnknownOption(inv, arg)
			}
			files = append(files, arg)
		}
	}
	return checkMode, files, nil
}

func writeSHA256SumUnknownOption(inv *Invocation, option string) error {
	var msg string
	if strings.HasPrefix(option, "--") {
		msg = fmt.Sprintf("sha256sum: unrecognized option '%s'\n", option)
	} else {
		msg = fmt.Sprintf("sha256sum: invalid option -- '%s'\n", strings.TrimPrefix(option, "-"))
	}
	if _, err := io.WriteString(inv.Stderr, msg); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return &ExitError{Code: 1}
}

type sha256sumReader struct {
	ctx         context.Context
	inv         *Invocation
	stdin       []byte
	stdinLoaded bool
}

func (r *sha256sumReader) read(name string) ([]byte, error) {
	if name == "-" {
		if !r.stdinLoaded {
			data, err := readAllStdin(r.inv)
			if err != nil {
				return nil, err
			}
			r.stdin = data
			r.stdinLoaded = true
		}
		return r.stdin, nil
	}
	data, _, err := readAllFile(r.ctx, r.inv, name)
	if err != nil {
		return nil, err
	}
	return data, nil
}

var _ Command = (*SHA256Sum)(nil)
