package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cadencerpm/just-bash-go/policy"
)

type Touch struct{}

func NewTouch() *Touch {
	return &Touch{}
}

func (c *Touch) Name() string {
	return "touch"
}

func (c *Touch) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	noCreate := false
	modTime := time.Now().UTC()

	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case args[0] == "-c" || args[0] == "--no-create":
			noCreate = true
			args = args[1:]
		case args[0] == "-a" || args[0] == "-m":
			args = args[1:]
		case args[0] == "-d" || args[0] == "--date":
			if len(args) < 2 {
				return exitf(inv, 1, "touch: option requires an argument -- d")
			}
			parsed, err := parseTouchTime(args[1])
			if err != nil {
				return exitf(inv, 1, "touch: invalid date format %q", args[1])
			}
			modTime = parsed
			args = args[2:]
		case strings.HasPrefix(args[0], "--date="):
			parsed, err := parseTouchTime(strings.TrimPrefix(args[0], "--date="))
			if err != nil {
				return exitf(inv, 1, "touch: invalid date format %q", strings.TrimPrefix(args[0], "--date="))
			}
			modTime = parsed
			args = args[1:]
		default:
			return exitf(inv, 1, "touch: unsupported flag %s", args[0])
		}
	}

	if len(args) == 0 {
		return exitf(inv, 1, "touch: missing file operand")
	}

	for _, name := range args {
		info, abs, exists, err := statMaybe(ctx, inv, policy.FileActionStat, name)
		if err != nil {
			return err
		}
		if !exists {
			if noCreate {
				continue
			}
			abs, err = allowPath(ctx, inv, policy.FileActionWrite, name)
			if err != nil {
				return err
			}
			if err := ensureParentDirExists(ctx, inv, abs); err != nil {
				return err
			}
			file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if err := file.Close(); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			recordFileMutation(inv.Trace, "touch", abs, "", abs)
		} else if info.IsDir() {
			// Touching directories updates metadata only.
			if _, err := allowPath(ctx, inv, policy.FileActionWrite, name); err != nil {
				return err
			}
		}
		if err := inv.FS.Chtimes(ctx, abs, modTime, modTime); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func parseTouchTime(value string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006/01/02 15:04:05",
		"2006-01-02",
		"2006/01/02",
	}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, value, time.UTC)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format")
}

var _ Command = (*Touch)(nil)
