package commands

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/ewhauser/gbash/policy"
)

type Column struct{}

type columnOptions struct {
	table        bool
	separator    string
	outputSep    string
	outputSepSet bool
	width        int
	widthValid   bool
	noMerge      bool
}

func NewColumn() *Column {
	return &Column{}
}

func (c *Column) Name() string {
	return "column"
}

func (c *Column) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Column) Spec() CommandSpec {
	return CommandSpec{
		Name:  "column",
		About: "column - columnate lists",
		Usage: "column [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "table", Short: 't', Long: "table", Help: "create a table based on whitespace-delimited input"},
			{Name: "separator", Short: 's', ValueName: "SEP", Arity: OptionRequiredValue, Help: "input field delimiter (default: whitespace)"},
			{Name: "output-separator", Short: 'o', ValueName: "SEP", Arity: OptionRequiredValue, Help: "output field delimiter (default: two spaces)"},
			{Name: "width", Short: 'c', ValueName: "WIDTH", Arity: OptionRequiredValue, Help: "output width for fill mode (default: 80)"},
			{Name: "no-merge", Short: 'n', Help: "don't merge multiple adjacent delimiters"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true, Help: "input files"},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			StopAtFirstPositional:    true,
		},
	}
}

func (c *Column) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || !hasColumnHelpFlag(inv.Args) {
		return inv
	}
	clone := *inv
	clone.Args = []string{"--help"}
	return &clone
}

func (c *Column) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	spec := c.Spec()
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &spec)
	}

	opts, err := parseColumnMatches(matches)
	if err != nil {
		return err
	}

	content, err := readColumnContent(ctx, inv, matches.Positionals())
	if err != nil {
		return err
	}
	if content == "" || strings.TrimSpace(content) == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	nonEmptyLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}

	outSep := "  "
	if opts.outputSepSet {
		outSep = opts.outputSep
	}

	var output string
	if opts.table {
		rows := make([][]string, 0, len(nonEmptyLines))
		for _, line := range nonEmptyLines {
			rows = append(rows, splitColumnFields(line, opts.separator, opts.noMerge))
		}
		output = formatColumnTable(rows, outSep)
	} else {
		items := make([]string, 0, len(nonEmptyLines))
		for _, line := range nonEmptyLines {
			items = append(items, splitColumnFields(line, opts.separator, opts.noMerge)...)
		}
		output = formatColumnFill(items, opts.width, opts.widthValid, outSep)
	}

	if output != "" {
		output += "\n"
	}
	if _, err := io.WriteString(inv.Stdout, output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseColumnMatches(matches *ParsedCommand) (columnOptions, error) {
	opts := columnOptions{
		width:      80,
		widthValid: true,
	}
	if matches == nil {
		return opts, nil
	}
	if matches.Has("table") {
		opts.table = true
	}
	if matches.Has("separator") {
		opts.separator = matches.Value("separator")
	}
	if matches.Has("output-separator") {
		opts.outputSep = matches.Value("output-separator")
		opts.outputSepSet = true
	}
	if matches.Has("width") {
		opts.width, opts.widthValid = parseColumnNumber(matches.Value("width"))
	}
	if matches.Has("no-merge") {
		opts.noMerge = true
	}
	return opts, nil
}

func hasColumnHelpFlag(args []string) bool {
	return slices.Contains(args, "--help")
}

func (c *Column) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSuffix(err.Error(), "\nTry 'column --help' for more information.")
		if message != err.Error() {
			trimmed := strings.TrimSuffix(message, "\n")
			if inv != nil && inv.Stderr != nil {
				_, _ = io.WriteString(inv.Stderr, trimmed+"\n")
			}
			return &ExitError{Code: exitErr.Code}
		}
	}
	return err
}

func parseColumnNumber(value string) (int, bool) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" {
		return 0, false
	}

	sign := 1
	switch value[0] {
	case '+':
		value = value[1:]
	case '-':
		sign = -1
		value = value[1:]
	}
	if value == "" || value[0] < '0' || value[0] > '9' {
		return 0, false
	}

	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	limit := maxInt
	if sign < 0 {
		limit = -minInt
	}

	n := 0
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < '0' || ch > '9' {
			break
		}
		digit := int(ch - '0')
		if n > (limit-digit)/10 {
			if sign < 0 {
				return minInt, true
			}
			return maxInt, true
		}
		n = (n * 10) + digit
	}
	if sign < 0 {
		n = -n
	}
	return n, true
}

func readColumnContent(ctx context.Context, inv *Invocation, files []string) (string, error) {
	if len(files) == 0 {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	var (
		builder     strings.Builder
		stdinData   []byte
		stdinLoaded bool
	)

	for _, name := range files {
		var data []byte
		if name == "-" {
			if !stdinLoaded {
				var err error
				stdinData, err = readAllStdin(ctx, inv)
				if err != nil {
					return "", err
				}
				stdinLoaded = true
			}
			data = stdinData
		} else {
			fileData, _, err := readAllFile(ctx, inv, name)
			if err != nil {
				if policy.IsDenied(err) {
					return "", err
				}
				if errors.Is(err, stdfs.ErrNotExist) {
					return "", exitf(inv, 1, "column: %s: No such file or directory", name)
				}
				return "", err
			}
			data = fileData
		}
		if _, err := builder.Write(data); err != nil {
			return "", &ExitError{Code: 1, Err: err}
		}
	}

	return builder.String(), nil
}

func splitColumnFields(line, separator string, noMerge bool) []string {
	if separator != "" {
		fields := strings.Split(line, separator)
		if noMerge {
			return fields
		}
		return filterEmptyColumnFields(fields)
	}

	if noMerge {
		return splitColumnWhitespaceNoMerge(line)
	}
	return strings.FieldsFunc(line, func(r rune) bool {
		return r == ' ' || r == '\t'
	})
}

func splitColumnWhitespaceNoMerge(line string) []string {
	fields := make([]string, 0, strings.Count(line, " ")+strings.Count(line, "\t")+1)
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			continue
		}
		fields = append(fields, line[start:i])
		start = i + 1
	}
	fields = append(fields, line[start:])
	return fields
}

func filterEmptyColumnFields(fields []string) []string {
	filtered := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			filtered = append(filtered, field)
		}
	}
	return filtered
}

func formatColumnTable(rows [][]string, outputSep string) string {
	if len(rows) == 0 {
		return ""
	}

	widths := calculateColumnWidths(rows)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		cells := make([]string, 0, len(row))
		for i, cell := range row {
			if i == len(row)-1 {
				cells = append(cells, cell)
				continue
			}
			cells = append(cells, padColumnCell(cell, widths[i]))
		}
		lines = append(lines, strings.Join(cells, outputSep))
	}
	return strings.Join(lines, "\n")
}

func calculateColumnWidths(rows [][]string) []int {
	widths := []int{}
	for _, row := range rows {
		for i, cell := range row {
			cellWidth := utf16Len(cell)
			if i >= len(widths) {
				widths = append(widths, cellWidth)
				continue
			}
			if cellWidth > widths[i] {
				widths[i] = cellWidth
			}
		}
	}
	return widths
}

func formatColumnFill(items []string, width int, widthValid bool, outputSep string) string {
	if len(items) == 0 || !widthValid {
		return ""
	}

	maxItemWidth := 0
	for _, item := range items {
		if itemWidth := utf16Len(item); itemWidth > maxItemWidth {
			maxItemWidth = itemWidth
		}
	}

	sepWidth := utf16Len(outputSep)
	columnWidth := maxItemWidth + sepWidth
	if columnWidth == 0 {
		return ""
	}

	numColumns := max(1, (width+sepWidth)/columnWidth)
	numRows := (len(items) + numColumns - 1) / numColumns

	lines := make([]string, 0, numRows)
	for row := range numRows {
		cells := make([]string, 0, numColumns)
		for col := range numColumns {
			index := (col * numRows) + row
			if index >= len(items) {
				continue
			}
			item := items[index]
			isLastInRow := col == numColumns-1 || ((col+1)*numRows)+row >= len(items)
			if isLastInRow {
				cells = append(cells, item)
			} else {
				cells = append(cells, padColumnCell(item, maxItemWidth))
			}
		}
		lines = append(lines, strings.Join(cells, outputSep))
	}
	return strings.Join(lines, "\n")
}

func padColumnCell(value string, width int) string {
	padding := width - utf16Len(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func utf16Len(value string) int {
	return len(utf16.Encode([]rune(value)))
}

var _ Command = (*Column)(nil)
var _ SpecProvider = (*Column)(nil)
var _ ParsedRunner = (*Column)(nil)
var _ ParseInvocationNormalizer = (*Column)(nil)
var _ ParseErrorNormalizer = (*Column)(nil)
