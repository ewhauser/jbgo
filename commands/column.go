package commands

import (
	"context"
	"errors"
	"fmt"
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

const columnHelpText = `column - columnate lists

Usage: column [OPTION]... [FILE]...

Description:
  Format input into multiple columns. By default, fills rows first. Use -t to create a table based on whitespace-delimited input.

Options:
  -t           Create a table (determine columns from input)
  -s SEP       Input field delimiter (default: whitespace)
  -o SEP       Output field delimiter (default: two spaces)
  -c WIDTH     Output width for fill mode (default: 80)
  -n           Don't merge multiple adjacent delimiters

Examples:
  ls | column              # Fill columns with ls output
  cat data | column -t     # Format as table
  column -t -s ',' file    # Format CSV as table
  column -c 40 file        # Fill 40-char wide columns
`

func NewColumn() *Column {
	return &Column{}
}

func (c *Column) Name() string {
	return "column"
}

func (c *Column) Run(ctx context.Context, inv *Invocation) error {
	if hasColumnHelpFlag(inv.Args) {
		if _, err := io.WriteString(inv.Stdout, columnHelpText); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	opts, files, err := parseColumnArgs(inv)
	if err != nil {
		return err
	}

	content, err := readColumnContent(ctx, inv, files)
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

func hasColumnHelpFlag(args []string) bool {
	return slices.Contains(args, "--help")
}

func parseColumnArgs(inv *Invocation) (columnOptions, []string, error) {
	opts := columnOptions{
		width:      80,
		widthValid: true,
	}

	args := inv.Args
	positionals := make([]string, 0, len(args))
	stopParsing := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if stopParsing || !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			stopParsing = true
			continue
		}

		if strings.HasPrefix(arg, "--") {
			switch {
			case arg == "--table":
				opts.table = true
			case strings.HasPrefix(arg, "--table="):
				opts.table = true
			default:
				return columnOptions{}, nil, writeColumnUnknownOption(inv, arg)
			}
			continue
		}

		chars := arg[1:]
		for j := 0; j < len(chars); j++ {
			switch chars[j] {
			case 't':
				opts.table = true
			case 'n':
				opts.noMerge = true
			case 's', 'o', 'c':
				value, nextIndex, err := readColumnShortValue(inv, args, i, chars, j)
				if err != nil {
					return columnOptions{}, nil, err
				}
				switch chars[j] {
				case 's':
					opts.separator = value
				case 'o':
					opts.outputSep = value
					opts.outputSepSet = true
				case 'c':
					opts.width, opts.widthValid = parseColumnNumber(value)
				}
				i = nextIndex
				j = len(chars)
			default:
				return columnOptions{}, nil, writeColumnUnknownOption(inv, "-"+string(chars[j]))
			}
		}
	}

	return opts, positionals, nil
}

func readColumnShortValue(inv *Invocation, args []string, argIndex int, chars string, charIndex int) (value string, nextArgIndex int, err error) {
	if charIndex+1 < len(chars) {
		return chars[charIndex+1:], argIndex, nil
	}
	if argIndex+1 >= len(args) {
		return "", argIndex, exitf(inv, 1, "column: option requires an argument -- '%c'", chars[charIndex])
	}
	return args[argIndex+1], argIndex + 1, nil
}

func writeColumnUnknownOption(inv *Invocation, option string) error {
	var msg string
	if strings.HasPrefix(option, "--") {
		msg = fmt.Sprintf("column: unrecognized option '%s'\n", option)
	} else {
		msg = fmt.Sprintf("column: invalid option -- '%s'\n", strings.TrimPrefix(option, "-"))
	}
	if _, err := io.WriteString(inv.Stderr, msg); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return &ExitError{Code: 1}
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
		data, err := readAllStdin(inv)
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
				stdinData, err = readAllStdin(inv)
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
