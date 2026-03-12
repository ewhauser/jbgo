package commands

import (
	"context"
	"io"
	"strings"
)

type Paste struct{}

type pasteOptions struct {
	serial         bool
	zeroTerminated bool
	delimiters     [][]byte
	showHelp       bool
	showVersion    bool
}

type pasteInput struct {
	records [][]byte
	index   *int
}

type pasteDelimiterState struct {
	delimiters [][]byte
	index      int
	lastLen    int
}

type pasteEncoding int

const (
	pasteEncodingUTF8 pasteEncoding = iota
	pasteEncodingGB18030
	pasteEncodingEUCJP
	pasteEncodingEUCKR
	pasteEncodingBig5
)

func NewPaste() *Paste {
	return &Paste{}
}

func (c *Paste) Name() string {
	return "paste"
}

func (c *Paste) Run(ctx context.Context, inv *Invocation) error {
	opts, names, err := parsePasteArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, _ = io.WriteString(inv.Stdout, pasteHelpText)
		return nil
	}
	if opts.showVersion {
		_, _ = io.WriteString(inv.Stdout, pasteVersionText)
		return nil
	}

	lineEnding := byte('\n')
	if opts.zeroTerminated {
		lineEnding = 0
	}

	inputs, err := loadPasteInputs(ctx, inv, names, lineEnding)
	if err != nil {
		return err
	}
	if opts.serial {
		return writePasteSerial(inv, inputs, opts.delimiters, lineEnding)
	}
	return writePasteParallel(inv, inputs, opts.delimiters, lineEnding)
}

func parsePasteArgs(inv *Invocation) (pasteOptions, []string, error) {
	opts := pasteOptions{
		delimiters: [][]byte{{'\t'}},
	}
	args := append([]string(nil), inv.Args...)

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if strings.HasPrefix(arg, "--") {
			consumed, err := parsePasteLongOption(inv, args, &opts)
			if err != nil {
				return pasteOptions{}, nil, err
			}
			args = args[consumed:]
			continue
		}
		consumed, err := parsePasteShortOptions(inv, args, &opts)
		if err != nil {
			return pasteOptions{}, nil, err
		}
		args = args[consumed:]
	}

	if len(args) == 0 {
		args = []string{"-"}
	}
	return opts, args, nil
}

func parsePasteLongOption(inv *Invocation, args []string, opts *pasteOptions) (int, error) {
	arg := args[0]
	name := strings.TrimPrefix(arg, "--")
	value := ""
	hasValue := false
	if before, after, ok := strings.Cut(name, "="); ok {
		name = before
		value = after
		hasValue = true
	}

	match, err := matchPasteLongOption(inv, name)
	if err != nil {
		return 0, err
	}
	switch match {
	case "help":
		opts.showHelp = true
		return 1, nil
	case "version":
		opts.showVersion = true
		return 1, nil
	case "serial":
		opts.serial = true
		return 1, nil
	case "zero-terminated":
		opts.zeroTerminated = true
		return 1, nil
	case "delimiters":
		if !hasValue {
			if len(args) < 2 {
				return 0, pasteUsageError(inv, "paste: option '--delimiters' requires an argument")
			}
			value = args[1]
			hasValue = true
		}
		delimiters, err := parsePasteDelimiters(inv, value)
		if err != nil {
			return 0, err
		}
		opts.delimiters = delimiters
		if hasValue && strings.Contains(arg, "=") {
			return 1, nil
		}
		return 2, nil
	default:
		return 0, pasteOptionf(inv, "paste: unrecognized option '%s'", arg)
	}
}

func parsePasteShortOptions(inv *Invocation, args []string, opts *pasteOptions) (int, error) {
	arg := args[0]
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 's':
			opts.serial = true
		case 'z':
			opts.zeroTerminated = true
		case 'd':
			value := arg[i+1:]
			consumed := 1
			if value == "" {
				if len(args) < 2 {
					return 0, pasteUsageError(inv, "paste: option requires an argument -- 'd'")
				}
				value = args[1]
				consumed = 2
			}
			delimiters, err := parsePasteDelimiters(inv, value)
			if err != nil {
				return 0, err
			}
			opts.delimiters = delimiters
			return consumed, nil
		default:
			return 0, pasteOptionf(inv, "paste: invalid option -- '%c'", arg[i])
		}
	}
	return 1, nil
}

func matchPasteLongOption(inv *Invocation, name string) (string, error) {
	candidates := []string{
		"delimiters",
		"help",
		"serial",
		"version",
		"zero-terminated",
	}
	for _, candidate := range candidates {
		if candidate == name {
			return candidate, nil
		}
	}
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, name) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return "", pasteOptionf(inv, "paste: unrecognized option '%s'", "--"+name)
	case 1:
		return matches[0], nil
	default:
		return "", pasteOptionf(inv, "paste: option '%s' is ambiguous", "--"+name)
	}
}

func parsePasteDelimiters(inv *Invocation, value string) ([][]byte, error) {
	raw := []byte(value)
	delimiters := make([][]byte, 0, len(raw))
	encoding := pasteEncodingFromEnv(inv.Env)

	for i := 0; i < len(raw); {
		if raw[i] == '\\' {
			i++
			if i >= len(raw) {
				return nil, exitf(inv, 1, "paste: delimiter list ends with an unescaped backslash: %s", value)
			}
			switch raw[i] {
			case '0':
				delimiters = append(delimiters, nil)
				i++
			case '\\':
				delimiters = append(delimiters, []byte{'\\'})
				i++
			case 'n':
				delimiters = append(delimiters, []byte{'\n'})
				i++
			case 't':
				delimiters = append(delimiters, []byte{'\t'})
				i++
			case 'b':
				delimiters = append(delimiters, []byte{'\b'})
				i++
			case 'f':
				delimiters = append(delimiters, []byte{'\f'})
				i++
			case 'r':
				delimiters = append(delimiters, []byte{'\r'})
				i++
			case 'v':
				delimiters = append(delimiters, []byte{'\v'})
				i++
			default:
				delim, size := nextPasteDelimiterBytes(raw[i:], encoding)
				delimiters = append(delimiters, append([]byte(nil), delim...))
				i += size
			}
			continue
		}

		delim, size := nextPasteDelimiterBytes(raw[i:], encoding)
		delimiters = append(delimiters, append([]byte(nil), delim...))
		i += size
	}

	return delimiters, nil
}

func nextPasteDelimiterBytes(raw []byte, encoding pasteEncoding) (delimiter []byte, size int) {
	if len(raw) == 0 {
		return nil, 0
	}
	size = pasteMultibyteLen(raw, encoding)
	return raw[:size], size
}

func pasteEncodingFromEnv(env map[string]string) pasteEncoding {
	locale := ""
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if value := strings.TrimSpace(env[key]); value != "" {
			locale = value
			break
		}
	}
	if locale == "" || locale == "C" || locale == "POSIX" {
		return pasteEncodingUTF8
	}

	charmap := locale
	if idx := strings.Index(charmap, "."); idx >= 0 {
		charmap = charmap[idx+1:]
	}
	if idx := strings.Index(charmap, "@"); idx >= 0 {
		charmap = charmap[:idx]
	}
	charmap = strings.ToLower(charmap)
	switch charmap {
	case "gb18030", "gbk", "gb2312":
		return pasteEncodingGB18030
	case "euc-jp", "eucjp":
		return pasteEncodingEUCJP
	case "euc-kr", "euckr":
		return pasteEncodingEUCKR
	case "big5", "big5-hkscs", "big5hkscs", "euc-tw", "euctw":
		return pasteEncodingBig5
	}

	localeName := locale
	if idx := strings.Index(localeName, "@"); idx >= 0 {
		localeName = localeName[:idx]
	}
	switch localeName {
	case "zh_CN", "zh_SG":
		return pasteEncodingGB18030
	case "zh_TW", "zh_HK":
		return pasteEncodingBig5
	default:
		return pasteEncodingUTF8
	}
}

func pasteMultibyteLen(raw []byte, encoding pasteEncoding) int {
	if len(raw) == 0 {
		return 0
	}
	b0 := raw[0]
	if b0 <= 0x7f {
		return 1
	}
	switch encoding {
	case pasteEncodingGB18030:
		return pasteGB18030Len(raw, b0)
	case pasteEncodingEUCJP:
		return pasteEUCJPLen(raw, b0)
	case pasteEncodingEUCKR:
		return pasteEUCKRLen(raw, b0)
	case pasteEncodingBig5:
		return pasteBig5Len(raw, b0)
	default:
		return pasteUTF8Len(raw, b0)
	}
}

func pasteUTF8Len(raw []byte, b0 byte) int {
	expected := 0
	switch {
	case b0 >= 0xc2 && b0 <= 0xdf:
		expected = 2
	case b0 >= 0xe0 && b0 <= 0xef:
		expected = 3
	case b0 >= 0xf0 && b0 <= 0xf4:
		expected = 4
	default:
		return 1
	}
	if len(raw) < expected {
		return 1
	}
	for _, b := range raw[1:expected] {
		if b&0xc0 != 0x80 {
			return 1
		}
	}
	return expected
}

func pasteGB18030Len(raw []byte, b0 byte) int {
	if b0 < 0x81 || b0 > 0xfe {
		return 1
	}
	if len(raw) >= 4 &&
		raw[1] >= 0x30 && raw[1] <= 0x39 &&
		raw[2] >= 0x81 && raw[2] <= 0xfe &&
		raw[3] >= 0x30 && raw[3] <= 0x39 {
		return 4
	}
	if len(raw) >= 2 && ((raw[1] >= 0x40 && raw[1] <= 0x7e) || (raw[1] >= 0x80 && raw[1] <= 0xfe)) {
		return 2
	}
	return 1
}

func pasteEUCJPLen(raw []byte, b0 byte) int {
	if b0 == 0x8f && len(raw) >= 3 && raw[1] >= 0xa1 && raw[1] <= 0xfe && raw[2] >= 0xa1 && raw[2] <= 0xfe {
		return 3
	}
	if len(raw) >= 2 {
		if b0 == 0x8e && raw[1] >= 0xa1 && raw[1] <= 0xdf {
			return 2
		}
		if b0 >= 0xa1 && b0 <= 0xfe && raw[1] >= 0xa1 && raw[1] <= 0xfe {
			return 2
		}
	}
	return 1
}

func pasteEUCKRLen(raw []byte, b0 byte) int {
	if b0 >= 0xa1 && b0 <= 0xfe && len(raw) >= 2 && raw[1] >= 0xa1 && raw[1] <= 0xfe {
		return 2
	}
	return 1
}

func pasteBig5Len(raw []byte, b0 byte) int {
	if b0 >= 0x81 && b0 <= 0xfe && len(raw) >= 2 &&
		((raw[1] >= 0x40 && raw[1] <= 0x7e) || (raw[1] >= 0xa1 && raw[1] <= 0xfe)) {
		return 2
	}
	return 1
}

func loadPasteInputs(ctx context.Context, inv *Invocation, names []string, lineEnding byte) ([]pasteInput, error) {
	var (
		stdinRecords [][]byte
		stdinIndex   int
		stdinLoaded  bool
		inputs       []pasteInput
	)
	for _, name := range names {
		if name == "-" {
			if !stdinLoaded {
				data, err := readAllStdin(inv)
				if err != nil {
					return nil, err
				}
				stdinRecords = splitPasteRecords(data, lineEnding)
				stdinLoaded = true
			}
			inputs = append(inputs, pasteInput{
				records: stdinRecords,
				index:   &stdinIndex,
			})
			continue
		}

		data, _, err := readAllFile(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		index := 0
		inputs = append(inputs, pasteInput{
			records: splitPasteRecords(data, lineEnding),
			index:   &index,
		})
	}
	return inputs, nil
}

func splitPasteRecords(data []byte, lineEnding byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	var records [][]byte
	start := 0
	for i, b := range data {
		if b != lineEnding {
			continue
		}
		records = append(records, append([]byte(nil), data[start:i]...))
		start = i + 1
	}
	if start < len(data) {
		records = append(records, append([]byte(nil), data[start:]...))
	}
	return records
}

func writePasteParallel(inv *Invocation, inputs []pasteInput, delimiters [][]byte, lineEnding byte) error {
	state := newPasteDelimiterState(delimiters)
	for {
		var out []byte
		eofCount := 0
		for i := range inputs {
			if *inputs[i].index < len(inputs[i].records) {
				out = append(out, inputs[i].records[*inputs[i].index]...)
				*inputs[i].index++
			} else {
				eofCount++
			}
			state.writeDelimiter(&out)
		}
		if eofCount == len(inputs) {
			return nil
		}
		state.removeTrailingDelimiter(&out)
		out = append(out, lineEnding)
		if _, err := inv.Stdout.Write(out); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		state.reset()
	}
}

func writePasteSerial(inv *Invocation, inputs []pasteInput, delimiters [][]byte, lineEnding byte) error {
	state := newPasteDelimiterState(delimiters)
	for _, input := range inputs {
		var out []byte
		for *input.index < len(input.records) {
			out = append(out, input.records[*input.index]...)
			*input.index++
			state.writeDelimiter(&out)
		}
		state.removeTrailingDelimiter(&out)
		out = append(out, lineEnding)
		if _, err := inv.Stdout.Write(out); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func newPasteDelimiterState(delimiters [][]byte) pasteDelimiterState {
	if len(delimiters) == 1 && len(delimiters[0]) == 0 {
		return pasteDelimiterState{}
	}
	return pasteDelimiterState{delimiters: delimiters}
}

func (s *pasteDelimiterState) writeDelimiter(out *[]byte) {
	if len(s.delimiters) == 0 {
		s.lastLen = 0
		return
	}
	delimiter := s.delimiters[0]
	if len(s.delimiters) > 1 {
		delimiter = s.delimiters[s.index]
		s.index = (s.index + 1) % len(s.delimiters)
	}
	*out = append(*out, delimiter...)
	s.lastLen = len(delimiter)
}

func (s *pasteDelimiterState) removeTrailingDelimiter(out *[]byte) {
	if s.lastLen > 0 && len(*out) >= s.lastLen {
		*out = (*out)[:len(*out)-s.lastLen]
	}
	s.lastLen = 0
}

func (s *pasteDelimiterState) reset() {
	if len(s.delimiters) > 1 {
		s.index = 0
	}
	s.lastLen = 0
}

func pasteOptionf(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'paste --help' for more information.", args...)
}

func pasteUsageError(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'paste --help' for more information.", args...)
}

const pasteHelpText = `Usage: paste [OPTION]... [FILE]...
Write lines consisting of the sequentially corresponding lines from each FILE.

  -d, --delimiters=LIST      reuse characters from LIST instead of TABs
  -s, --serial               paste one file at a time instead of in parallel
  -z, --zero-terminated      line delimiter is NUL, not newline
      --help                 display this help and exit
      --version              output version information and exit
`

const pasteVersionText = "paste (gbash) dev\n"

var _ Command = (*Paste)(nil)
