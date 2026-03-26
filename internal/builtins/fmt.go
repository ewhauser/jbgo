package builtins

import (
	"context"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/runewidth"
)

const (
	fmtDefaultWidth  = 75
	fmtDefaultLeeway = 7
	fmtMaxChars      = 5000
	fmtTabWidth      = 8
)

const fmtHelpText = `Usage: fmt [-WIDTH] [OPTION]... [FILE]...
Reformat each paragraph in the FILE(s), writing to standard output.
The option -WIDTH is an abbreviated form of --width=DIGITS.

  -c, --crown-margin
         preserve indentation of first two lines
  -p, --prefix=STRING
         reformat only lines beginning with STRING,
         reattaching the prefix to reformatted lines
  -s, --split-only
         split long lines, but do not refill
  -t, --tagged-paragraph
         indentation of first line different from second
  -u, --uniform-spacing
         one space between words, two after sentences
  -w, --width=WIDTH
         maximum line width (default of 75 columns)
  -g, --goal=WIDTH
         goal width (default of 93% of width)
      --help          display this help and exit
      --version       output version information and exit
`

type fmtOptions struct {
	crown          bool
	splitOnly      bool
	tagged         bool
	uniformSpacing bool
	showHelp       bool
	showVersion    bool

	prefix          string
	prefixLeadSpace int
	prefixWidth     int

	maxWidthRaw  string
	goalWidthRaw string

	files []string
}

type fmtParsedLine struct {
	raw          string
	leader       string
	prefixIndent int
	indent       int
	body         string
}

type fmtWord struct {
	text       string
	length     int
	space      int
	paren      bool
	period     bool
	punct      bool
	final      bool
	lineLength int
	bestCost   int64
	nextBreak  int
}

type Fmt struct{}

func NewFmt() *Fmt {
	return &Fmt{}
}

func (c *Fmt) Name() string {
	return "fmt"
}

func (c *Fmt) Run(ctx context.Context, inv *Invocation) error {
	if inv == nil {
		return nil
	}

	opts, maxWidth, goalWidth, err := parseFmtOptions(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, err := io.WriteString(inv.Stdout, fmtHelpText)
		return err
	}
	if opts.showVersion {
		_, err := io.WriteString(inv.Stdout, c.Name()+" (gbash)\n")
		return err
	}

	files := opts.files
	if len(files) == 0 {
		files = []string{"-"}
	}

	var hadErrors bool
	for _, name := range files {
		var (
			data    []byte
			readErr error
		)
		if name == "-" {
			data, readErr = readAllStdin(ctx, inv)
		} else {
			data, _, readErr = readAllFile(ctx, inv, name)
		}
		if readErr != nil {
			hadErrors = true
			if _, err := io.WriteString(inv.Stderr, "fmt: cannot open "+quoteGNUOperand(name)+" for reading: "+readAllErrorText(readErr)+"\n"); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		if _, err := io.WriteString(inv.Stdout, formatFmtInput(string(data), &opts, maxWidth, goalWidth)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if hadErrors {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseFmtOptions(inv *Invocation) (fmtOptions, int, int, error) {
	opts := fmtOptions{}
	args := append([]string(nil), inv.Args...)

	if len(args) > 0 && fmtLooksLikeObsoleteWidth(args[0]) {
		opts.maxWidthRaw = args[0][1:]
		args = args[1:]
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.files = append(opts.files, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") && arg != "--" {
			name := arg[2:]
			value := ""
			hasValue := false
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				value = name[idx+1:]
				name = name[:idx]
				hasValue = true
			}
			switch name {
			case "help":
				if hasValue {
					return fmtOptions{}, 0, 0, commandUsageError(inv, "fmt", "unrecognized option '--%s'", arg[2:])
				}
				opts.showHelp = true
				return opts, 0, 0, nil
			case "version":
				if hasValue {
					return fmtOptions{}, 0, 0, commandUsageError(inv, "fmt", "unrecognized option '--%s'", arg[2:])
				}
				opts.showVersion = true
				return opts, 0, 0, nil
			case "crown-margin":
				opts.crown = true
			case "split-only":
				opts.splitOnly = true
			case "tagged-paragraph":
				opts.tagged = true
			case "uniform-spacing":
				opts.uniformSpacing = true
			case "width":
				if !hasValue {
					if i+1 >= len(args) {
						return fmtOptions{}, 0, 0, fmtMissingOptionValue(inv, 'w')
					}
					i++
					value = args[i]
				}
				opts.maxWidthRaw = value
			case "goal":
				if !hasValue {
					if i+1 >= len(args) {
						return fmtOptions{}, 0, 0, fmtMissingOptionValue(inv, 'g')
					}
					i++
					value = args[i]
				}
				opts.goalWidthRaw = value
			case "prefix":
				if !hasValue {
					if i+1 >= len(args) {
						return fmtOptions{}, 0, 0, fmtMissingOptionValue(inv, 'p')
					}
					i++
					value = args[i]
				}
				opts.prefix, opts.prefixLeadSpace = fmtNormalizePrefix(value)
				opts.prefixWidth = fmtStringWidth(opts.prefix)
			default:
				return fmtOptions{}, 0, 0, commandUsageError(inv, "fmt", "unrecognized option '--%s'", name)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			shorts := arg[1:]
			for shorts != "" {
				ch := shorts[0]
				shorts = shorts[1:]
				switch ch {
				case 'c':
					opts.crown = true
				case 's':
					opts.splitOnly = true
				case 't':
					opts.tagged = true
				case 'u':
					opts.uniformSpacing = true
				case 'w', 'g', 'p':
					value := shorts
					shorts = ""
					if value == "" {
						if i+1 >= len(args) {
							return fmtOptions{}, 0, 0, fmtMissingOptionValue(inv, rune(ch))
						}
						i++
						value = args[i]
					}
					switch ch {
					case 'w':
						opts.maxWidthRaw = value
					case 'g':
						opts.goalWidthRaw = value
					case 'p':
						opts.prefix, opts.prefixLeadSpace = fmtNormalizePrefix(value)
						opts.prefixWidth = fmtStringWidth(opts.prefix)
					}
				default:
					if ch >= '0' && ch <= '9' {
						return fmtOptions{}, 0, 0, exitf(inv, 1, "fmt: invalid option -- %c; -WIDTH is recognized only when it is the first\noption; use -w N instead\nTry 'fmt --help' for more information.", ch)
					}
					return fmtOptions{}, 0, 0, commandUsageError(inv, "fmt", "invalid option -- %c", ch)
				}
			}
			continue
		}
		opts.files = append(opts.files, arg)
	}

	maxWidth := fmtDefaultWidth
	if opts.maxWidthRaw != "" {
		width, err := fmtParseWidth(inv, opts.maxWidthRaw, fmtMaxChars/2)
		if err != nil {
			return fmtOptions{}, 0, 0, err
		}
		maxWidth = width
	}

	goalWidth := maxWidth * (2*(100-fmtDefaultLeeway) + 1) / 200
	if opts.goalWidthRaw != "" {
		goal, err := fmtParseWidth(inv, opts.goalWidthRaw, maxWidth)
		if err != nil {
			return fmtOptions{}, 0, 0, err
		}
		goalWidth = goal
		if opts.maxWidthRaw == "" {
			maxWidth = goal + 10
		}
	}

	return opts, maxWidth, goalWidth, nil
}

func fmtLooksLikeObsoleteWidth(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[1] < '0' || arg[1] > '9' {
		return false
	}
	return true
}

func fmtMissingOptionValue(inv *Invocation, short rune) error {
	return exitf(inv, 1, "fmt: option requires an argument -- %c\nTry 'fmt --help' for more information.", short)
}

func fmtParseWidth(inv *Invocation, raw string, upper int) (int, error) {
	width, err := strconv.Atoi(raw)
	if err != nil || width < 0 || width > upper {
		return 0, exitf(inv, 1, "fmt: invalid width: %s", quoteGNUOperand(raw))
	}
	return width, nil
}

func fmtNormalizePrefix(raw string) (string, int) {
	lead := 0
	for lead < len(raw) && raw[lead] == ' ' {
		lead++
	}
	return raw[lead:], lead
}

func formatFmtInput(data string, opts *fmtOptions, maxWidth, goalWidth int) string {
	lines := fmtSplitLines(data)
	if len(lines) == 0 {
		return ""
	}

	var out strings.Builder
	taggedSecondaryIndent := 0

	for i := 0; i < len(lines); {
		current, ok := fmtParseInputLine(lines[i], opts)
		if !ok {
			fmtWriteRawLine(&out, lines[i])
			i++
			continue
		}

		paragraph := []fmtParsedLine{current}
		i++
		if !opts.splitOnly {
			for i < len(lines) {
				next, ok := fmtParseInputLine(lines[i], opts)
				if !ok || next.prefixIndent != current.prefixIndent {
					break
				}
				if !fmtSameParagraphIndent(opts, paragraph, next) {
					break
				}
				paragraph = append(paragraph, next)
				i++
			}
		}

		firstIndent, otherIndent := fmtParagraphIndentation(paragraph, opts, taggedSecondaryIndent)
		if opts.tagged && len(paragraph) > 1 {
			taggedSecondaryIndent = otherIndent
		}

		words := fmtParagraphWords(paragraph, opts.uniformSpacing)
		if len(words) == 0 {
			for _, line := range paragraph {
				fmtWriteRawLine(&out, line.raw)
			}
			continue
		}

		words[len(words)-1].period = true
		words[len(words)-1].final = true

		fmtBreakParagraph(words, firstIndent, otherIndent, maxWidth, goalWidth)
		firstLeader, firstLeaderWidth, otherLeader, otherLeaderWidth := fmtParagraphLeaders(paragraph)
		fmtWriteParagraph(&out, words, firstLeader, firstLeaderWidth, otherLeader, otherLeaderWidth, firstIndent, otherIndent)
	}

	return out.String()
}

func fmtSplitLines(data string) []string {
	if data == "" {
		return nil
	}

	lines := make([]string, 0, strings.Count(data, "\n")+1)
	start := 0
	for start < len(data) {
		idx := strings.IndexByte(data[start:], '\n')
		if idx < 0 {
			lines = append(lines, data[start:])
			break
		}
		end := start + idx
		lines = append(lines, data[start:end])
		start = end + 1
	}
	return lines
}

func fmtParseInputLine(raw string, opts *fmtOptions) (fmtParsedLine, bool) {
	trimmed, indentCols, indentEnd := fmtTrimLeadingBlanks(raw)
	if opts.prefix == "" && opts.prefixLeadSpace == 0 {
		if trimmed == "" {
			return fmtParsedLine{}, false
		}
		return fmtParsedLine{
			raw:          raw,
			leader:       raw[:indentEnd],
			prefixIndent: 0,
			indent:       indentCols,
			body:         trimmed,
		}, true
	}
	if opts.prefix == "" {
		if indentCols < opts.prefixLeadSpace || trimmed == "" {
			return fmtParsedLine{}, false
		}
		return fmtParsedLine{
			raw:          raw,
			leader:       raw[:indentEnd],
			prefixIndent: indentCols,
			indent:       indentCols,
			body:         trimmed,
		}, true
	}

	if indentCols < opts.prefixLeadSpace || !strings.HasPrefix(trimmed, opts.prefix) {
		return fmtParsedLine{}, false
	}

	afterPrefix := trimmed[len(opts.prefix):]
	body, postPrefixIndent, postPrefixEnd := fmtTrimLeadingBlanks(afterPrefix)
	if body == "" {
		return fmtParsedLine{}, false
	}

	return fmtParsedLine{
		raw:          raw,
		leader:       raw[:indentEnd+len(opts.prefix)+postPrefixEnd],
		prefixIndent: indentCols,
		indent:       indentCols + opts.prefixWidth + postPrefixIndent,
		body:         body,
	}, true
}

func fmtSameParagraphIndent(opts *fmtOptions, paragraph []fmtParsedLine, next fmtParsedLine) bool {
	switch {
	case opts.crown:
		if len(paragraph) == 1 {
			return true
		}
		return next.indent == paragraph[1].indent
	case opts.tagged:
		if len(paragraph) == 1 {
			return next.indent != paragraph[0].indent
		}
		return next.indent == paragraph[1].indent
	default:
		return next.indent == paragraph[0].indent
	}
}

func fmtParagraphIndentation(paragraph []fmtParsedLine, opts *fmtOptions, taggedSecondaryIndent int) (int, int) {
	firstIndent := paragraph[0].indent
	switch {
	case opts.splitOnly:
		return firstIndent, firstIndent
	case opts.crown:
		if len(paragraph) > 1 {
			return firstIndent, paragraph[1].indent
		}
		return firstIndent, firstIndent
	case opts.tagged:
		if len(paragraph) > 1 {
			return firstIndent, paragraph[1].indent
		}
		otherIndent := taggedSecondaryIndent
		if otherIndent == firstIndent {
			if firstIndent == 0 {
				otherIndent = 3
			} else {
				otherIndent = 0
			}
		}
		return firstIndent, otherIndent
	default:
		return firstIndent, firstIndent
	}
}

func fmtParagraphLeaders(paragraph []fmtParsedLine) (string, int, string, int) {
	firstLeader := paragraph[0].leader
	firstLeaderWidth := paragraph[0].indent
	otherLeader := firstLeader
	otherLeaderWidth := firstLeaderWidth
	if len(paragraph) > 1 {
		otherLeader = paragraph[1].leader
		otherLeaderWidth = paragraph[1].indent
	}
	return firstLeader, firstLeaderWidth, otherLeader, otherLeaderWidth
}

func fmtParagraphWords(paragraph []fmtParsedLine, uniform bool) []fmtWord {
	words := make([]fmtWord, 0, len(paragraph)*8)

	for lineIndex, line := range paragraph {
		body := line.body
		position := 0
		column := line.indent

		for position < len(body) {
			wordStart := position
			for position < len(body) && !fmtIsBlank(body[position]) {
				position++
			}
			if wordStart == position {
				position++
				continue
			}

			text := body[wordStart:position]
			word := fmtWord{
				text:   text,
				length: fmtStringWidth(text),
			}
			word.paren, word.punct, word.period = fmtWordPunctuation(text)
			column += word.length

			startCol := column
			for position < len(body) && fmtIsBlank(body[position]) {
				if body[position] == '\t' {
					column = fmtNextTabStop(column)
				} else {
					column++
				}
				position++
			}

			space := column - startCol
			atLineEnd := position >= len(body)
			word.final = lineIndex == len(paragraph)-1 && atLineEnd
			if word.period && (atLineEnd || space > 1) {
				word.final = true
			}
			if atLineEnd || uniform {
				if word.final {
					space = 2
				} else {
					space = 1
				}
			}
			word.space = space
			words = append(words, word)
		}
	}

	return words
}

func fmtWordPunctuation(text string) (paren, punct, period bool) {
	first, _ := utf8.DecodeRuneInString(text)
	last, _ := utf8.DecodeLastRuneInString(text)

	paren = strings.ContainsRune("([`\"", first)
	punct = unicode.IsPunct(last)

	trimmed := strings.TrimRightFunc(text, func(r rune) bool {
		return strings.ContainsRune(")]'\"", r)
	})
	if trimmed == "" {
		return paren, punct, false
	}
	lastMeaningful, _ := utf8.DecodeLastRuneInString(trimmed)
	period = strings.ContainsRune(".?!", lastMeaningful)
	return paren, punct, period
}

func fmtBreakParagraph(words []fmtWord, firstIndent, otherIndent, maxWidth, goalWidth int) {
	const maxCost = int64(^uint64(0) >> 2)

	bestCost := make([]int64, len(words)+1)
	lineLength := make([]int, len(words)+1)
	nextBreak := make([]int, len(words)+1)
	lineLength[len(words)] = maxWidth
	nextBreak[len(words)] = len(words)

	for start := len(words) - 1; start >= 0; start-- {
		best := maxCost
		lineLen := otherIndent
		if start == 0 {
			lineLen = firstIndent
		}
		lineLen += words[start].length

		next := start + 1
		for {
			cost := fmtLineCost(nextBreak, lineLength, next, len(words), lineLen, goalWidth) + bestCost[next]
			if cost < best {
				best = cost
				nextBreak[start] = next
				lineLength[start] = lineLen
			}

			if next == len(words) {
				break
			}
			lineLen += words[next-1].space + words[next].length
			if lineLen > maxWidth {
				break
			}
			next++
		}

		bestCost[start] = best + fmtBaseCost(words, start)
	}

	for i := range words {
		words[i].bestCost = bestCost[i]
		words[i].lineLength = lineLength[i]
		words[i].nextBreak = nextBreak[i]
	}
}

func fmtBaseCost(words []fmtWord, idx int) int64 {
	const (
		lineCost      = int64(70 * 70)
		sentenceBonus = int64(50 * 50)
		noBreakCost   = int64(600 * 600)
		parenBonus    = int64(40 * 40)
		punctBonus    = int64(40 * 40)
	)

	cost := lineCost
	if idx > 0 {
		prev := words[idx-1]
		switch {
		case prev.period && prev.final:
			cost -= sentenceBonus
		case prev.period:
			cost += noBreakCost
		case prev.punct:
			cost -= punctBonus
		case idx > 1 && words[idx-2].final:
			cost += fmtEquiv(200) / int64(prev.length+2)
		}
	}

	if words[idx].paren {
		cost -= parenBonus
	} else if words[idx].final {
		cost += fmtEquiv(150) / int64(words[idx].length+2)
	}
	return cost
}

func fmtLineCost(nextBreak, lineLength []int, next, sentinel, lineLen, goalWidth int) int64 {
	if next == sentinel {
		return 0
	}

	n := goalWidth - lineLen
	cost := fmtShortCost(n)
	if nextBreak[next] != sentinel {
		cost += fmtRaggedCost(lineLen - lineLength[next])
	}
	return cost
}

func fmtWriteParagraph(out *strings.Builder, words []fmtWord, firstLeader string, firstLeaderWidth int, otherLeader string, otherLeaderWidth, firstIndent, otherIndent int) {
	for idx := 0; idx < len(words); {
		indent := otherIndent
		leader := otherLeader
		leaderWidth := otherLeaderWidth
		if idx == 0 {
			indent = firstIndent
			leader = firstLeader
			leaderWidth = firstLeaderWidth
		}

		out.WriteString(leader)
		if indent > leaderWidth {
			out.WriteString(strings.Repeat(" ", indent-leaderWidth))
		}

		end := words[idx].nextBreak
		if end == 0 {
			end = len(words)
		}
		for i := idx; i < end; i++ {
			out.WriteString(words[i].text)
			if i+1 < end {
				out.WriteString(strings.Repeat(" ", words[i].space))
			}
		}
		out.WriteByte('\n')
		idx = end
	}
}

func fmtWriteRawLine(out *strings.Builder, raw string) {
	out.WriteString(raw)
	out.WriteByte('\n')
}

func fmtTrimLeadingBlanks(raw string) (string, int, int) {
	cols := 0
	index := 0
	for index < len(raw) {
		switch raw[index] {
		case ' ':
			cols++
			index++
		case '\t':
			cols = fmtNextTabStop(cols)
			index++
		default:
			return raw[index:], cols, index
		}
	}
	return "", cols, index
}

func fmtStringWidth(value string) int {
	width := 0
	for _, r := range value {
		width += runewidth.RuneWidth(r)
	}
	return width
}

func fmtIsBlank(b byte) bool {
	return b == ' ' || b == '\t'
}

func fmtNextTabStop(col int) int {
	return col + fmtTabWidth - col%fmtTabWidth
}

func fmtEquiv(n int) int64 {
	value := int64(n)
	return value * value
}

func fmtShortCost(n int) int64 {
	value := int64(n * 10)
	return value * value
}

func fmtRaggedCost(n int) int64 {
	return fmtShortCost(n) / 2
}

var _ Command = (*Fmt)(nil)
