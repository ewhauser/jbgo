package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/runewidth"
)

const (
	foldDefaultWidth = 80
	foldTabWidth     = 8
)

const foldHelpText = `Usage: fold [OPTION]... [FILE]...
Wrap input lines in each FILE, writing to standard output.

With no FILE, or when FILE is -, read standard input.

Mandatory arguments to long options are mandatory for short options too.
  -b, --bytes         count bytes rather than columns
  -c, --characters    count characters rather than columns
  -s, --spaces        break at spaces
  -w, --width=WIDTH   use WIDTH columns instead of 80
      --help          display this help and exit
      --version       output version information and exit
`

type foldWidthMode int

const (
	foldModeColumns foldWidthMode = iota
	foldModeCharacters
	foldModeBytes
)

type Fold struct{}

func NewFold() *Fold {
	return &Fold{}
}

func (c *Fold) Name() string {
	return "fold"
}

func (c *Fold) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Fold) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}

	normalized := make([]string, 0, len(inv.Args))
	changed := false
	for i, arg := range inv.Args {
		if arg == "--" {
			normalized = append(normalized, arg)
			normalized = append(normalized, inv.Args[i+1:]...)
			break
		}
		if len(arg) >= 2 && arg[0] == '-' && arg[1] != '-' {
			if arg[1] >= '0' && arg[1] <= '9' {
				// Pure obsolete: -4, -20
				normalized = append(normalized, "-w", arg[1:])
				changed = true
				continue
			}
			// Grouped flags ending with digits: -s4, -bs20
			if digitIdx := foldFindTrailingDigits(arg); digitIdx > 0 {
				normalized = append(normalized, arg[:digitIdx], "-w", arg[digitIdx:])
				changed = true
				continue
			}
		}
		normalized = append(normalized, arg)
	}
	if !changed {
		return inv
	}
	clone := *inv
	clone.Args = normalized
	return &clone
}

// foldFindTrailingDigits returns the index where trailing digits start in a
// short-option group like "-s4" or "-bs20". Returns -1 if no trailing digits,
// or if the prefix contains non-flag characters (only b, c, s are boolean flags).
func foldFindTrailingDigits(arg string) int {
	i := len(arg) - 1
	for i >= 2 && arg[i] >= '0' && arg[i] <= '9' {
		i--
	}
	if i == len(arg)-1 {
		return -1 // no trailing digits
	}
	// Verify all chars between '-' and the digits are known boolean flags.
	for j := 1; j <= i; j++ {
		if arg[j] != 'b' && arg[j] != 'c' && arg[j] != 's' {
			return -1
		}
	}
	return i + 1
}

func (c *Fold) Spec() CommandSpec {
	return CommandSpec{
		Name:  "fold",
		About: "Wrap input lines to fit in specified width.",
		Usage: "fold [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "bytes", Short: 'b', Long: "bytes", Help: "count bytes rather than columns"},
			{Name: "characters", Short: 'c', Long: "characters", Help: "count characters rather than columns"},
			{Name: "spaces", Short: 's', Long: "spaces", Help: "break at spaces"},
			{Name: "width", Short: 'w', Long: "width", Arity: OptionRequiredValue, ValueName: "WIDTH", Help: "use WIDTH columns instead of 80"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
		HelpRenderer: renderStaticHelp(foldHelpText),
	}
}

func (c *Fold) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	width := foldDefaultWidth
	if matches.Has("width") {
		v := matches.Value("width")
		w, err := strconv.Atoi(v)
		if err != nil {
			return exitf(inv, 1, "fold: invalid number of columns: %s", quoteValue(v))
		}
		if w <= 0 {
			return exitf(inv, 1, "fold: invalid number of columns: %s", quoteValue(v))
		}
		width = w
	}

	mode := foldModeColumns
	if matches.Has("bytes") {
		mode = foldModeBytes
	} else if matches.Has("characters") {
		mode = foldModeCharacters
	}
	spaces := matches.Has("spaces")

	files := matches.Args("file")
	if len(files) == 0 {
		files = []string{"-"}
	}

	var hadErrors bool
	for _, name := range files {
		data, err := func() ([]byte, error) {
			if name == "-" {
				return readAllStdin(ctx, inv)
			}
			read, _, err := readAllFile(ctx, inv, name)
			if err != nil {
				return nil, err
			}
			return read, nil
		}()
		if err != nil {
			hadErrors = true
			if _, writeErr := fmt.Fprintf(inv.Stderr, "fold: %s: %s\n", name, readAllErrorText(err)); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			continue
		}

		var out []byte
		if mode == foldModeBytes {
			out = foldBytewise(data, spaces, width)
		} else {
			out = foldText(data, spaces, width, mode)
		}
		if _, err := inv.Stdout.Write(out); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadErrors {
		return &ExitError{Code: 1}
	}
	return nil
}

func quoteValue(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// foldBytewise implements -b mode: every byte counts as 1 column.
// Tabs, backspace, and CR have no special width handling.
func foldBytewise(data []byte, spaces bool, width int) []byte {
	var out []byte
	lines := splitFoldLines(data)
	for _, line := range lines {
		if len(line) == 1 && line[0] == '\n' {
			out = append(out, '\n')
			continue
		}

		lineLen := len(line)
		i := 0
		for i < lineLen {
			end := min(i+width, lineLen)
			chunk := line[i:end]
			if spaces && end < lineLen {
				if bp := lastByteSpacePos(chunk); bp >= 0 {
					chunk = line[i : i+bp+1]
				}
			}

			// Skip lone trailing newline from previous fold point.
			if len(chunk) == 1 && chunk[0] == '\n' {
				break
			}

			i += len(chunk)
			atEOL := i >= lineLen

			out = append(out, chunk...)
			if !atEOL {
				out = append(out, '\n')
			}
		}
	}
	return out
}

func lastByteSpacePos(chunk []byte) int {
	for j := len(chunk) - 1; j >= 0; j-- {
		if isASCIIWhitespace(chunk[j]) && chunk[j] != '\r' {
			return j
		}
	}
	return -1
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\v' || b == '\f' || b == '\r'
}

// foldText implements column mode (default) and character mode (-c).
func foldText(data []byte, spaces bool, width int, mode foldWidthMode) []byte {
	var output []byte
	var lineBuf []byte
	colCount := 0
	lastSpace := -1

	emit := func() {
		consume := len(lineBuf)
		if lastSpace >= 0 {
			consume = lastSpace + 1
		}

		if consume > 0 {
			output = append(output, lineBuf[:consume]...)
		}
		output = append(output, '\n')

		if consume < len(lineBuf) {
			lineBuf = append(lineBuf[:0], lineBuf[consume:]...)
		} else {
			lineBuf = lineBuf[:0]
		}

		colCount = computeColCount(lineBuf, mode)

		if spaces {
			// Rebase lastSpace into remaining buffer.
			if lastSpace >= 0 && lastSpace >= consume {
				lastSpace -= consume
			} else {
				lastSpace = -1
			}
		} else {
			lastSpace = -1
		}
	}

	processRune := func(r rune, raw []byte) {
		if r == '\n' {
			lastSpace = -1
			emit()
			return
		}

		if r == '\r' {
			lineBuf = append(lineBuf, raw...)
			colCount = 0
			return
		}

		if r == '\x08' {
			lineBuf = append(lineBuf, raw...)
			if colCount > 0 {
				colCount--
			}
			return
		}

		if colCount >= width {
			emit()
		}

		if r == '\t' {
			for {
				nextStop := foldNextTabStop(colCount)
				if nextStop > width && len(lineBuf) > 0 {
					emit()
					continue
				}
				colCount = nextStop
				break
			}
			if spaces {
				lastSpace = len(lineBuf)
			} else {
				lastSpace = -1
			}
			lineBuf = append(lineBuf, raw...)
			return
		}

		added := 1
		if mode == foldModeColumns {
			added = runewidth.RuneWidth(r)
		}

		// For column mode, wide char might exceed width.
		if mode == foldModeColumns && added > 0 && colCount+added > width && len(lineBuf) > 0 {
			emit()
		}

		if spaces && r < 128 && isASCIIWhitespace(byte(r)) {
			lastSpace = len(lineBuf)
		}

		lineBuf = append(lineBuf, raw...)
		colCount += added
	}

	// Process data handling UTF-8 properly.
	if isValidUTF8ForFold(data) {
		processUTF8(data, mode, processRune)
	} else {
		processNonUTF8(data, mode, processRune)
	}

	// Flush remaining buffer without trailing newline.
	if len(lineBuf) > 0 {
		output = append(output, lineBuf...)
	}

	return output
}

func processUTF8(data []byte, mode foldWidthMode, fn func(r rune, raw []byte)) {
	i := 0
	for i < len(data) {
		r, size := utf8.DecodeRune(data[i:])
		end := i + size

		// In column mode, coalesce combining characters with their base.
		if mode == foldModeColumns {
			for end < len(data) {
				nr, ns := utf8.DecodeRune(data[end:])
				if runewidth.RuneWidth(nr) == 0 && nr != '\n' && nr != '\r' && nr != '\t' && nr != '\x08' {
					end += ns
				} else {
					break
				}
			}
		}

		fn(r, data[i:end])
		i = end
	}
}

func processNonUTF8(data []byte, _ foldWidthMode, fn func(r rune, raw []byte)) {
	for _, b := range data {
		fn(rune(b), []byte{b})
	}
}

func isValidUTF8ForFold(data []byte) bool {
	return utf8.Valid(data)
}

func computeColCount(buf []byte, mode foldWidthMode) int {
	s := string(buf)
	width := 0
	for _, ch := range s {
		switch ch {
		case '\r':
			width = 0
		case '\t':
			width = foldNextTabStop(width)
		case '\x08':
			if width > 0 {
				width--
			}
		default:
			if mode == foldModeCharacters {
				width++
			} else {
				width += runewidth.RuneWidth(ch)
			}
		}
	}
	return width
}

func foldNextTabStop(col int) int {
	return col + foldTabWidth - col%foldTabWidth
}

// splitFoldLines splits data into lines, each including its trailing newline.
func splitFoldLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i+1])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

var (
	_ Command                   = (*Fold)(nil)
	_ SpecProvider              = (*Fold)(nil)
	_ ParsedRunner              = (*Fold)(nil)
	_ ParseInvocationNormalizer = (*Fold)(nil)
)
