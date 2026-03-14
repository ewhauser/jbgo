package builtins

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Xan struct{}

type xanSubcommand struct {
	Summary     string
	Usage       string
	Description string
	Options     []string
	Run         func(context.Context, *Invocation, []string) error
}

type xanTable struct {
	Headers []string
	Rows    [][]string
}

type xanNamedExpr struct {
	Alias string
	Expr  xanExpr
}

type xanAggSpec struct {
	Func  string
	Expr  xanExpr
	Alias string
	Raw   string
}

var xanImplementedSubcommands = map[string]xanSubcommand{
	"agg": {
		Summary:     "Aggregate values",
		Usage:       "xan agg EXPR [FILE]",
		Description: "Aggregate values across CSV rows.",
		Run:         xanRunAgg,
	},
	"behead": {
		Summary:     "Remove header row",
		Usage:       "xan behead [FILE]",
		Description: "Output CSV data rows without the header row.",
		Run:         xanRunBehead,
	},
	"cat": {
		Summary:     "Concatenate CSV files",
		Usage:       "xan cat [OPTIONS] FILE1 FILE2 ...",
		Description: "Concatenate multiple CSV files.",
		Options: []string{
			"-p, --pad    pad missing columns with empty values",
		},
		Run: xanRunCat,
	},
	"count": {
		Summary:     "Count rows",
		Usage:       "xan count [FILE]",
		Description: "Count data rows excluding the header.",
		Run:         xanRunCount,
	},
	"dedup": {
		Summary:     "Remove duplicates",
		Usage:       "xan dedup [OPTIONS] [FILE]",
		Description: "Remove duplicate rows or duplicate values in one column.",
		Options: []string{
			"-s COL    deduplicate using this column",
		},
		Run: xanRunDedup,
	},
	"drop": {
		Summary:     "Drop columns",
		Usage:       "xan drop COLS [FILE]",
		Description: "Drop columns by name, index, range, or glob.",
		Run:         xanRunDrop,
	},
	"enum": {
		Summary:     "Add row index column",
		Usage:       "xan enum [OPTIONS] [FILE]",
		Description: "Add an index column to each row.",
		Options: []string{
			"-c NAME    set the index column name (default: index)",
		},
		Run: xanRunEnum,
	},
	"explode": {
		Summary:     "Split column into rows",
		Usage:       "xan explode COLUMN [OPTIONS] [FILE]",
		Description: "Split delimited column values into multiple rows.",
		Options: []string{
			"-s, --separator SEP  separator (default: |)",
			"--drop-empty         drop empty values",
			"-r, --rename NAME    rename column",
		},
		Run: xanRunExplode,
	},
	"filter": {
		Summary:     "Filter rows by expression",
		Usage:       "xan filter [OPTIONS] EXPR [FILE]",
		Description: "Filter rows using moonblade-like expressions.",
		Options: []string{
			"-v, --invert    invert match",
			"-l, --limit N   limit output rows",
		},
		Run: xanRunFilter,
	},
	"fixlengths": {
		Summary:     "Fix ragged CSV files",
		Usage:       "xan fixlengths [OPTIONS] [FILE]",
		Description: "Pad or truncate ragged CSV rows to a fixed width.",
		Options: []string{
			"-l, --length N   target number of columns",
			"-d, --default V  default value for missing cells",
		},
		Run: xanRunFixlengths,
	},
	"flatmap": {
		Summary:     "Map to multiple rows",
		Usage:       "xan flatmap EXPR [FILE]",
		Description: "Map rows where array results expand into multiple rows.",
		Run:         xanRunFlatmap,
	},
	"flatten": {
		Summary:     "Display records vertically",
		Usage:       "xan flatten [OPTIONS] [FILE]",
		Description: "Display records one field per line.",
		Options: []string{
			"-l, --limit N    limit the number of displayed rows",
			"-s, --select C   display only these columns",
		},
		Run: xanRunFlatten,
	},
	"fmt": {
		Summary:     "Format output",
		Usage:       "xan fmt [OPTIONS] [FILE]",
		Description: "Alias for xan view.",
		Run:         xanRunFmt,
	},
	"frequency": {
		Summary:     "Count value occurrences",
		Usage:       "xan frequency [OPTIONS] [FILE]",
		Description: "Count value frequencies per column.",
		Options: []string{
			"-s, --select COLS   limit to these columns",
			"-g, --groupby COL   compute frequencies inside groups",
			"-l, --limit N       limit results per field",
			"-A, --all           show all values",
			"--no-extra          omit empty values",
		},
		Run: xanRunFrequency,
	},
	"from": {
		Summary:     "Convert to CSV",
		Usage:       "xan from -f <format> [FILE]",
		Description: "Convert JSON input to CSV.",
		Run:         xanRunFrom,
	},
	"groupby": {
		Summary:     "Group and aggregate",
		Usage:       "xan groupby COLS EXPR [FILE]",
		Description: "Group rows and compute aggregations per group.",
		Options: []string{
			"--sorted    accept pre-sorted input (currently a no-op)",
		},
		Run: xanRunGroupby,
	},
	"head": {
		Summary:     "Show first N rows",
		Usage:       "xan head [OPTIONS] [FILE]",
		Description: "Show the first N data rows.",
		Options: []string{
			"-l, -n N  show the first N rows",
		},
		Run: xanRunHead,
	},
	"headers": {
		Summary:     "Show column names",
		Usage:       "xan headers [OPTIONS] [FILE]",
		Description: "Display column names from a CSV file.",
		Options: []string{
			"-j, --just-names    show names only (no index)",
		},
		Run: xanRunHeaders,
	},
	"implode": {
		Summary:     "Combine rows",
		Usage:       "xan implode COLUMN [OPTIONS] [FILE]",
		Description: "Combine consecutive rows, joining one column's values.",
		Options: []string{
			"-s, --sep SEP       separator (default: |)",
			"-r, --rename NAME   rename column",
		},
		Run: xanRunImplode,
	},
	"join": {
		Summary:     "Join CSV files",
		Usage:       "xan join KEY1 FILE1 KEY2 FILE2 [OPTIONS]",
		Description: "Join two CSV files on key columns.",
		Options: []string{
			"--left             left outer join",
			"--right            right outer join",
			"--full             full outer join",
			"-D, --default VAL  default value for missing fields",
		},
		Run: xanRunJoin,
	},
	"map": {
		Summary:     "Add computed columns",
		Usage:       "xan map [OPTIONS] EXPR [FILE]",
		Description: "Add computed columns using moonblade-like expressions.",
		Options: []string{
			"-O, --overwrite  overwrite columns with matching aliases",
			"--filter         drop rows where a computed value is null",
		},
		Run: xanRunMap,
	},
	"merge": {
		Summary:     "Merge CSV files",
		Usage:       "xan merge [OPTIONS] FILE1 FILE2 ...",
		Description: "Merge multiple CSV files with matching headers.",
		Options: []string{
			"-s, --sort COL  sort merged rows by this column",
		},
		Run: xanRunMerge,
	},
	"partition": {
		Summary:     "Split by column value",
		Usage:       "xan partition COLUMN [OPTIONS] [FILE]",
		Description: "Write one CSV file per unique value in a column.",
		Options: []string{
			"-o, --output DIR  output directory",
		},
		Run: xanRunPartition,
	},
	"pivot": {
		Summary:     "Reshape to columns",
		Usage:       "xan pivot COLUMN AGG_EXPR [OPTIONS] [FILE]",
		Description: "Turn row values into columns.",
		Options: []string{
			"-g, --groupby COLS  group by columns",
		},
		Run: xanRunPivot,
	},
	"rename": {
		Summary:     "Rename columns",
		Usage:       "xan rename NEW_NAMES [-s COLS] [FILE]",
		Description: "Rename all columns or only selected columns.",
		Options: []string{
			"-s COLS  rename only these columns",
		},
		Run: xanRunRename,
	},
	"reverse": {
		Summary:     "Reverse row order",
		Usage:       "xan reverse [FILE]",
		Description: "Reverse the order of data rows.",
		Run:         xanRunReverse,
	},
	"sample": {
		Summary:     "Random sample of rows",
		Usage:       "xan sample [OPTIONS] <sample-size> [FILE]",
		Description: "Sample N rows from the input CSV.",
		Options: []string{
			"--seed N  use a fixed random seed",
		},
		Run: xanRunSample,
	},
	"search": {
		Summary:     "Filter rows by regex",
		Usage:       "xan search [OPTIONS] PATTERN [FILE]",
		Description: "Filter rows by regular expression match.",
		Options: []string{
			"-s, --select COLS  search only these columns",
			"-v, --invert       invert match",
			"-i, --ignore-case  ignore case",
		},
		Run: xanRunSearch,
	},
	"select": {
		Summary:     "Select columns",
		Usage:       "xan select COLS [FILE]",
		Description: "Select columns by name, index, range, or glob.",
		Run:         xanRunSelect,
	},
	"shuffle": {
		Summary:     "Randomly reorder rows",
		Usage:       "xan shuffle [OPTIONS] [FILE]",
		Description: "Shuffle data rows.",
		Options: []string{
			"--seed N  use a fixed random seed",
		},
		Run: xanRunShuffle,
	},
	"slice": {
		Summary:     "Extract row range",
		Usage:       "xan slice [OPTIONS] [FILE]",
		Description: "Extract a row range using JavaScript-like slice semantics.",
		Options: []string{
			"-s, --start N  start row index",
			"-e, --end N    end row index",
			"-l, --len N    number of rows to take",
		},
		Run: xanRunSlice,
	},
	"sort": {
		Summary:     "Sort rows",
		Usage:       "xan sort [OPTIONS] [FILE]",
		Description: "Sort rows by a column.",
		Options: []string{
			"-s COL          sort using this column",
			"-N, --numeric   numeric sort",
			"-R, -r          reverse order",
		},
		Run: xanRunSort,
	},
	"split": {
		Summary:     "Split into multiple files",
		Usage:       "xan split [OPTIONS] FILE",
		Description: "Split a CSV into multiple CSV files.",
		Options: []string{
			"-c, --chunks N  split into N chunks",
			"-S, --size N    split into chunks of N rows",
			"-o, --output D  output directory",
		},
		Run: xanRunSplit,
	},
	"stats": {
		Summary:     "Show column statistics",
		Usage:       "xan stats [OPTIONS] [FILE]",
		Description: "Show simple column statistics.",
		Options: []string{
			"-s COLS  restrict stats to these columns",
		},
		Run: xanRunStats,
	},
	"tail": {
		Summary:     "Show last N rows",
		Usage:       "xan tail [OPTIONS] [FILE]",
		Description: "Show the last N data rows.",
		Options: []string{
			"-l, -n N  show the last N rows",
		},
		Run: xanRunTail,
	},
	"to": {
		Summary:     "Convert from CSV",
		Usage:       "xan to <format> [FILE]",
		Description: "Convert CSV input to another format.",
		Run:         xanRunTo,
	},
	"top": {
		Summary:     "Get top rows by column",
		Usage:       "xan top COLUMN [OPTIONS] [FILE]",
		Description: "Show the top N rows by a numeric column.",
		Options: []string{
			"-l, -n N  number of rows (default: 10)",
			"-R, -r    show the bottom N instead",
		},
		Run: xanRunTop,
	},
	"transform": {
		Summary:     "Modify existing columns",
		Usage:       "xan transform COLUMN EXPR [FILE]",
		Description: "Modify existing columns in place using expressions.",
		Options: []string{
			"-r, --rename NAME  rename transformed columns",
		},
		Run: xanRunTransform,
	},
	"transpose": {
		Summary:     "Swap rows and columns",
		Usage:       "xan transpose [FILE]",
		Description: "Transpose the CSV.",
		Run:         xanRunTranspose,
	},
	"view": {
		Summary:     "Pretty print as table",
		Usage:       "xan view [OPTIONS] [FILE]",
		Description: "Render the CSV as a box-drawing table.",
		Options: []string{
			"-n N  limit the number of displayed rows",
		},
		Run: xanRunView,
	},
}

var xanAliases = map[string]string{
	"f":    "flatten",
	"freq": "frequency",
}

var xanReservedSubcommands = map[string]struct{}{
	"fuzzy-join": {},
	"glob":       {},
	"hist":       {},
	"input":      {},
	"parallel":   {},
	"plot":       {},
	"progress":   {},
	"range":      {},
	"scrape":     {},
	"tokenize":   {},
	"union-find": {},
}

const xanTopLevelHelp = `Usage: xan <COMMAND> [OPTIONS] [FILE]

xan is a collection of commands for working with CSV data.
It provides a simple, ergonomic interface for common data operations.

COMMANDS:
  Core:
    headers    Show column names
    count      Count rows
    head       Show first N rows
    tail       Show last N rows
    slice      Extract row range
    reverse    Reverse row order
    behead     Remove header row
    sample     Random sample of rows

  Column operations:
    select     Select columns (supports glob, ranges, negation)
    drop       Drop columns
    rename     Rename columns
    enum       Add row index column

  Row operations:
    filter     Filter rows by expression
    search     Filter rows by regex match
    sort       Sort rows
    dedup      Remove duplicates
    top        Get top N by column

  Transformations:
    map        Add computed columns
    transform  Modify existing columns
    explode    Split column into multiple rows
    implode    Combine rows, join column values
    flatmap    Map returning multiple rows
    pivot      Reshape rows into columns
    transpose  Swap rows and columns

  Aggregation:
    agg        Aggregate values
    groupby    Group and aggregate
    frequency  Count value occurrences
    stats      Show column statistics

  Multi-file:
    cat        Concatenate CSV files
    join       Join two CSV files on key
    merge      Merge sorted CSV files
    split      Split into multiple files
    partition  Split by column value

  Data conversion:
    to         Convert CSV to other formats (json)
    from       Convert other formats to CSV (json)
    shuffle    Randomly reorder rows
    fixlengths Fix ragged CSV files

  Output:
    view       Pretty print as table
    flatten    Display records vertically (alias: f)
    fmt        Format output

EXAMPLES:
  xan headers data.csv
  xan count data.csv
  xan head -n 5 data.csv
  xan select name,email data.csv
  xan select 'vec_*' data.csv
  xan select 'a:c' data.csv
  xan filter 'age > 30' data.csv
  xan search -r '^foo' data.csv
  xan sort -N price data.csv
  xan agg 'sum(amount) as total' data.csv
  xan groupby region 'count() as n' data.csv
  xan explode tags data.csv
  xan join id file1.csv id file2.csv
  xan pivot year 'sum(sales)' data.csv
`

func NewXan() *Xan {
	return &Xan{}
}

func (c *Xan) Name() string {
	return "xan"
}

func (c *Xan) Run(ctx context.Context, inv *Invocation) error {
	args := append([]string(nil), inv.Args...)
	if len(args) == 0 || xanHasHelpFlag(args) {
		return xanWriteHelp(inv.Stdout, "")
	}

	if args[0] == "help" {
		if len(args) > 1 {
			return xanWriteHelp(inv.Stdout, xanResolveAlias(args[1]))
		}
		return xanWriteHelp(inv.Stdout, "")
	}

	subcommand := xanResolveAlias(args[0])
	subArgs := args[1:]
	if xanHasHelpFlag(subArgs) {
		return xanWriteHelp(inv.Stdout, subcommand)
	}

	if impl, ok := xanImplementedSubcommands[subcommand]; ok {
		return impl.Run(ctx, inv, subArgs)
	}
	if _, ok := xanReservedSubcommands[subcommand]; ok {
		return exitf(inv, 1, "xan %s: not yet implemented", subcommand)
	}
	return exitf(inv, 1, "xan: unknown command '%s'\nRun 'xan --help' for usage.", args[0])
}

func xanWriteHelp(w io.Writer, subcommand string) error {
	if subcommand == "" {
		_, err := io.WriteString(w, xanTopLevelHelp)
		return err
	}

	impl, ok := xanImplementedSubcommands[subcommand]
	if !ok {
		_, err := io.WriteString(w, xanTopLevelHelp)
		return err
	}

	if _, err := fmt.Fprintf(w, "Usage: %s\n\n%s\n", impl.Usage, impl.Description); err != nil {
		return err
	}
	for _, line := range impl.Options {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func xanReadTable(ctx context.Context, inv *Invocation, name string) (*xanTable, error) {
	data, err := xanReadText(ctx, inv, name, "xan")
	if err != nil {
		return nil, err
	}
	return xanParseTable(data, inv)
}

func xanReadText(ctx context.Context, inv *Invocation, name, prefix string) ([]byte, error) {
	if name == "" || name == "-" {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return nil, exitf(inv, exitCodeForError(err), "%s: %s", prefix, err)
		}
		return data, nil
	}

	data, _, err := readAllFile(ctx, inv, name)
	if err != nil {
		return nil, exitf(inv, exitCodeForError(err), "%s: %s: %s", prefix, name, readAllErrorText(err))
	}
	return data, nil
}

func xanParseTable(data []byte, inv *Invocation) (*xanTable, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return nil, exitf(inv, 1, "xan: failed to parse CSV: %v", err)
	}
	if len(records) == 0 {
		return &xanTable{}, nil
	}

	headers := append([]string(nil), records[0]...)
	rows := make([][]string, 0, max(len(records)-1, 0))
	for _, record := range records[1:] {
		row := make([]string, len(headers))
		copy(row, record)
		rows = append(rows, row)
	}

	return &xanTable{Headers: headers, Rows: rows}, nil
}

func xanReadRawCSVRecords(ctx context.Context, inv *Invocation, name, prefix string) ([][]string, error) {
	data, err := xanReadText(ctx, inv, name, prefix)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return nil, exitf(inv, 1, "%s: failed to parse CSV: %v", prefix, err)
	}

	filtered := make([][]string, 0, len(records))
	for _, record := range records {
		if len(record) == 1 && record[0] == "" {
			continue
		}
		filtered = append(filtered, append([]string(nil), record...))
	}
	return filtered, nil
}

func xanWriteCSV(w io.Writer, headers []string, rows [][]string) error {
	if len(headers) == 0 && len(rows) == 0 {
		return nil
	}
	writer := csv.NewWriter(w)
	if len(headers) > 0 {
		if err := writer.Write(headers); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func xanWriteRows(w io.Writer, rows [][]string) error {
	writer := csv.NewWriter(w)
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func xanWriteJSON(w io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func xanWriteSandboxFile(ctx context.Context, inv *Invocation, name string, data []byte) error {
	abs := inv.FS.Resolve(name)
	if err := inv.FS.MkdirAll(ctx, path.Dir(abs), 0o755); err != nil {
		return err
	}
	file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Close()
}

func xanSelectColumns(table *xanTable, indices []int) *xanTable {
	headers := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx >= 0 && idx < len(table.Headers) {
			headers = append(headers, table.Headers[idx])
		}
	}

	rows := make([][]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		next := make([]string, 0, len(indices))
		for _, idx := range indices {
			if idx >= 0 && idx < len(table.Headers) {
				if idx < len(row) {
					next = append(next, row[idx])
				} else {
					next = append(next, "")
				}
			}
		}
		rows = append(rows, next)
	}
	return &xanTable{Headers: headers, Rows: rows}
}

func xanParseColumnSpec(spec string, headers []string) []int {
	var result []int
	excludes := make(map[int]struct{})

	addUnique := func(idx int) {
		if idx < 0 || idx >= len(headers) || slices.Contains(result, idx) {
			return
		}
		result = append(result, idx)
	}

	for _, part := range strings.Split(spec, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "!") {
			for _, idx := range xanParseColumnSpec(trimmed[1:], headers) {
				excludes[idx] = struct{}{}
			}
			continue
		}
		if trimmed == "*" {
			for idx := range headers {
				addUnique(idx)
			}
			continue
		}
		if strings.Contains(trimmed, "*") {
			matcher := xanGlobMatcher(trimmed)
			for idx, header := range headers {
				if matcher.MatchString(header) {
					addUnique(idx)
				}
			}
			continue
		}
		if startCol, endCol, ok := xanParseColumnRange(trimmed); ok {
			startIdx := 0
			endIdx := len(headers) - 1
			if startCol != "" {
				startIdx = slices.Index(headers, startCol)
			}
			if endCol != "" {
				endIdx = slices.Index(headers, endCol)
			}
			if startIdx >= 0 && endIdx >= 0 {
				step := 1
				if startIdx > endIdx {
					step = -1
				}
				for idx := startIdx; ; idx += step {
					addUnique(idx)
					if idx == endIdx {
						break
					}
				}
			}
			continue
		}
		if start, end, ok := xanParseNumericRange(trimmed); ok {
			for idx := start; idx <= end && idx < len(headers); idx++ {
				if idx >= 0 {
					result = append(result, idx)
				}
			}
			continue
		}
		if idx, err := strconv.Atoi(trimmed); err == nil {
			if idx >= 0 && idx < len(headers) {
				result = append(result, idx)
			}
			continue
		}
		if idx := slices.Index(headers, trimmed); idx >= 0 {
			result = append(result, idx)
		}
	}

	if len(excludes) == 0 {
		return result
	}

	filtered := make([]int, 0, len(result))
	for _, idx := range result {
		if _, ok := excludes[idx]; !ok {
			filtered = append(filtered, idx)
		}
	}
	return filtered
}

func xanParseColumnRange(value string) (string, string, bool) {
	if strings.Count(value, ":") != 1 {
		return "", "", false
	}
	parts := strings.SplitN(value, ":", 2)
	if parts[0] == "" && parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

var xanNumericRangePattern = regexp.MustCompile(`^(\d+)-(\d+)$`)

func xanParseNumericRange(value string) (int, int, bool) {
	match := xanNumericRangePattern.FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(match[1])
	end, err2 := strconv.Atoi(match[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return start, end, true
}

func xanGlobMatcher(pattern string) *regexp.Regexp {
	expr := regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, `\*`, ".*")
	return regexp.MustCompile("^" + expr + "$")
}

func xanResolveAlias(name string) string {
	if resolved, ok := xanAliases[name]; ok {
		return resolved
	}
	return name
}

func xanHasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func xanSplitPrimaryOperand(args []string) (string, string) {
	primary := ""
	fileArg := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if primary == "" {
			primary = arg
			continue
		}
		if fileArg == "" {
			fileArg = arg
		}
	}
	return primary, fileArg
}

func xanFirstOperand(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func xanCloneRow(row []string) []string {
	return append([]string(nil), row...)
}

func xanSliceRows(rows [][]string, start, end *int) [][]string {
	n := len(rows)
	startIdx := 0
	if start != nil {
		startIdx = *start
		if startIdx < 0 {
			startIdx += n
		}
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx > n {
			startIdx = n
		}
	}

	endIdx := n
	if end != nil {
		endIdx = *end
		if endIdx < 0 {
			endIdx += n
		}
		if endIdx < 0 {
			endIdx = 0
		}
		if endIdx > n {
			endIdx = n
		}
	}

	if endIdx < startIdx {
		endIdx = startIdx
	}
	return rows[startIdx:endIdx]
}

func xanSeededStep(seed *int64) float64 {
	*seed = (*seed*1103515245 + 12345) & 0x7fffffff
	return float64(*seed) / float64(0x7fffffff)
}

func xanDefaultSeed() int64 {
	return time.Now().UnixNano() & 0x7fffffff
}

func xanParseScalar(raw string) any {
	if raw == "" {
		return ""
	}
	if raw == "true" {
		return true
	}
	if raw == "false" {
		return false
	}
	if strings.TrimSpace(raw) != raw {
		return raw
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return f
	}
	return raw
}

func xanValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	default:
		return fmt.Sprint(v)
	}
}

func xanValueJSON(value any) any {
	switch v := value.(type) {
	case int64:
		return v
	case float64:
		return v
	case bool:
		return v
	case string:
		return v
	default:
		return xanParseScalar(xanValueString(v))
	}
}

func xanQuotedCSV(rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	if err := xanWriteRows(&buf, rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xanTableCSV(table *xanTable) ([]byte, error) {
	var buf bytes.Buffer
	if err := xanWriteCSV(&buf, table.Headers, table.Rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var _ Command = (*Xan)(nil)
