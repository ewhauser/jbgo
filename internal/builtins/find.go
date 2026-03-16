package builtins

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"path"
	"sort"
	"strings"
)

type Find struct{}

const findHelpText = `find - search for files in a directory hierarchy

Usage:
  find [path ...] [expression]

Supported predicates:
  -name PATTERN       file name matches shell pattern
  -iname PATTERN      case-insensitive file name match
  -path PATTERN       displayed path matches shell pattern
  -ipath PATTERN      case-insensitive displayed path match
  -regex PATTERN      displayed path matches regular expression
  -iregex PATTERN     case-insensitive regular expression match
  -type f|d           filter by file or directory
  -empty              match empty files and empty directories
  -mtime N            match modification age in days (+N, -N, N)
  -newer FILE         match files newer than FILE
  -size N[ckMGb]      match file size
  -perm MODE          match file permissions
  -maxdepth N         descend at most N levels
  -mindepth N         skip matches above N levels
  -depth              process directory contents before the directory
  -prune              do not descend into matching directories
  -exec CMD {} ;      execute CMD for each match
  -exec CMD {} +      execute CMD once with all matches
  -print              print matched paths
  -print0             print matched paths separated by NUL
  -printf FORMAT      print matches with formatted fields
  -delete             delete matched paths
  -a, -and            logical AND
  -o, -or             logical OR
  -not, !             negate the following expression
  --help              show this help text
`

type findTraversalState struct {
	results   []string
	printData []findPrintData
}

func NewFind() *Find {
	return &Find{}
}

func (c *Find) Name() string {
	return "find"
}

func (c *Find) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Find) Spec() CommandSpec {
	return CommandSpec{
		Name:  "find",
		About: "search for files in a directory hierarchy",
		Usage: "find [path ...] [expression]",
		Options: []OptionSpec{
			{Name: "help", Long: "help", Help: "show this help text"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		HelpRenderer: renderStaticHelp(findHelpText),
	}
}

func (c *Find) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil {
		return nil
	}
	if len(inv.Args) == 1 && inv.Args[0] == "--help" {
		return inv
	}
	clone := *inv
	clone.Args = append([]string{"--"}, inv.Args...)
	return &clone
}

func (c *Find) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return renderStaticHelp(findHelpText)(inv.Stdout, c.Spec())
	}

	paths, opts, expr, actions, err := parseFindCommandArgs(inv, matches.Args("arg"))
	if err != nil {
		return err
	}
	if err := resolveFindExpr(ctx, inv, expr); err != nil {
		return err
	}

	state := &findTraversalState{}
	hasExplicitPrint := findHasPrintAction(actions)
	hasPrintfAction := findHasPrintfAction(actions)

	exitCode := 0
	for _, root := range paths {
		rootAbs := path.Join(inv.Cwd, root)
		if strings.HasPrefix(root, "/") {
			rootAbs = root
		}
		if _, _, exists, err := statMaybe(ctx, inv, rootAbs); err != nil {
			return err
		} else if !exists {
			_, _ = fmt.Fprintf(inv.Stderr, "find: %s: No such file or directory\n", root)
			exitCode = 1
			continue
		}
		if err := c.walk(ctx, inv, root, rootAbs, rootAbs, nil, 0, opts, expr, state, hasExplicitPrint, hasPrintfAction); err != nil {
			return err
		}
	}

	actionExitCode, err := c.runActions(ctx, inv, actions, state)
	if err != nil {
		return err
	}
	if actionExitCode > exitCode {
		exitCode = actionExitCode
	}

	if len(actions) == 0 {
		if err := writeFindSeparated(inv, state.results, "\n", true); err != nil {
			return err
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *Find) walk(
	ctx context.Context,
	inv *Invocation,
	rootArg, rootAbs, currentAbs string,
	currentInfo stdfs.FileInfo,
	depth int,
	opts findCommandOptions,
	expr findExpr,
	state *findTraversalState,
	hasExplicitPrint, hasPrintfAction bool,
) error {
	info := currentInfo
	var err error
	if info == nil {
		info, _, err = statPath(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
	}

	displayPath := walkDisplayPath(rootArg, rootAbs, currentAbs)
	name := findNodeName(rootArg, rootAbs, currentAbs, info)

	needsEntries := info.IsDir() && (findExprNeedsEmptyCheck(expr) || (!opts.hasMaxDepth || depth < opts.maxDepth))
	var entries []stdfs.DirEntry
	entriesLoaded := false
	if needsEntries {
		entries, err = readDir(ctx, inv, currentAbs)
		if err != nil {
			return err
		}
		entriesLoaded = true
	}

	isEmpty := false
	if info.IsDir() {
		if entriesLoaded {
			isEmpty = len(entries) == 0
		}
	} else {
		isEmpty = info.Size() == 0
	}

	matchCtx := &findEvalContext{
		displayPath: displayPath,
		name:        name,
		isDir:       info.IsDir(),
		isEmpty:     isEmpty,
		mtime:       info.ModTime(),
		size:        info.Size(),
		mode:        info.Mode(),
	}

	shouldCollect, eval := shouldCollectFindNode(expr, matchCtx, depth, opts, hasExplicitPrint)
	if !opts.depthFirst && shouldCollect {
		recordFindNode(state, displayPath, name, info, depth, rootArg, hasPrintfAction)
	}

	if info.IsDir() && (!opts.hasMaxDepth || depth < opts.maxDepth) && !eval.pruned {
		if !entriesLoaded {
			entries, err = readDir(ctx, inv, currentAbs)
			if err != nil {
				return err
			}
		}
		for _, entry := range entries {
			childAbs := joinChildPath(currentAbs, entry.Name())
			var childInfo stdfs.FileInfo
			if entry.Type()&stdfs.ModeSymlink == 0 {
				childInfo, _ = entry.Info()
			}
			if err := c.walk(ctx, inv, rootArg, rootAbs, childAbs, childInfo, depth+1, opts, expr, state, hasExplicitPrint, hasPrintfAction); err != nil {
				return err
			}
		}
	}

	if opts.depthFirst && shouldCollect {
		recordFindNode(state, displayPath, name, info, depth, rootArg, hasPrintfAction)
	}
	return nil
}

func shouldCollectFindNode(expr findExpr, matchCtx *findEvalContext, depth int, opts findCommandOptions, hasExplicitPrint bool) (bool, findEvalResult) {
	if opts.hasMinDepth && depth < opts.minDepth {
		return false, findEvalResult{}
	}
	if expr == nil {
		return true, findEvalResult{matches: true}
	}
	result := evaluateFindExpr(expr, matchCtx)
	if hasExplicitPrint {
		return result.printed, result
	}
	return result.matches, result
}

func recordFindNode(state *findTraversalState, displayPath, name string, info stdfs.FileInfo, depth int, rootArg string, includePrintf bool) {
	state.results = append(state.results, displayPath)
	if !includePrintf {
		return
	}
	state.printData = append(state.printData, findPrintData{
		path:          displayPath,
		name:          name,
		size:          info.Size(),
		mtime:         info.ModTime(),
		mode:          info.Mode(),
		isDirectory:   info.IsDir(),
		depth:         depth,
		startingPoint: rootArg,
	})
}

func (c *Find) runActions(ctx context.Context, inv *Invocation, actions []findAction, state *findTraversalState) (int, error) {
	exitCode := 0
	for _, action := range actions {
		switch a := action.(type) {
		case *findPrintAction:
			if err := writeFindSeparated(inv, state.results, "\n", true); err != nil {
				return 0, err
			}
		case *findPrint0Action:
			if err := writeFindSeparated(inv, state.results, "\x00", true); err != nil {
				return 0, err
			}
		case *findPrintfAction:
			for _, item := range state.printData {
				if _, err := fmt.Fprint(inv.Stdout, formatFindPrintf(a.format, &item)); err != nil {
					return 0, &ExitError{Code: 1, Err: err}
				}
			}
		case *findDeleteAction:
			deleteExitCode := deleteFindResults(ctx, inv, state.results)
			if deleteExitCode > exitCode {
				exitCode = deleteExitCode
			}
		case *findExecAction:
			execExitCode, err := executeFindAction(ctx, inv, a, state.results)
			if err != nil {
				return 0, err
			}
			if execExitCode > exitCode {
				exitCode = execExitCode
			}
		}
	}
	return exitCode, nil
}

func executeFindAction(ctx context.Context, inv *Invocation, action *findExecAction, results []string) (int, error) {
	if action.batchMode {
		argv := replaceFindExecPlaceholders(action.command, results)
		result, err := executeCommand(ctx, inv, &executeCommandOptions{Argv: argv})
		if err != nil {
			return 0, err
		}
		if err := writeExecutionOutputs(inv, result); err != nil {
			return 0, err
		}
		if result != nil {
			return result.ExitCode, nil
		}
		return 0, nil
	}

	exitCode := 0
	for _, item := range results {
		argv := replaceFindExecPlaceholders(action.command, []string{item})
		result, err := executeCommand(ctx, inv, &executeCommandOptions{Argv: argv})
		if err != nil {
			return 0, err
		}
		if err := writeExecutionOutputs(inv, result); err != nil {
			return 0, err
		}
		if result != nil && result.ExitCode != 0 {
			exitCode = result.ExitCode
		}
	}
	return exitCode, nil
}

func replaceFindExecPlaceholders(parts, replacements []string) []string {
	argv := make([]string, 0, len(parts)+len(replacements))
	for _, part := range parts {
		if part == "{}" {
			argv = append(argv, replacements...)
			continue
		}
		argv = append(argv, part)
	}
	return argv
}

func deleteFindResults(ctx context.Context, inv *Invocation, results []string) int {
	exitCode := 0
	sorted := append([]string(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if len(sorted[i]) == len(sorted[j]) {
			return sorted[i] > sorted[j]
		}
		return len(sorted[i]) > len(sorted[j])
	})

	for _, name := range sorted {
		abs := allowPath(inv, name)
		if err := inv.FS.Remove(ctx, abs, false); err != nil {
			_, _ = fmt.Fprintf(inv.Stderr, "find: cannot delete '%s': %v\n", name, err)
			exitCode = 1
			continue
		}
	}
	return exitCode
}

func writeFindSeparated(inv *Invocation, values []string, sep string, trailing bool) error {
	if len(values) == 0 {
		return nil
	}
	output := strings.Join(values, sep)
	if trailing {
		output += sep
	}
	if _, err := fmt.Fprint(inv.Stdout, output); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func walkDisplayPath(rootArg, rootAbs, currentAbs string) string {
	if currentAbs == rootAbs {
		if strings.HasPrefix(rootArg, "/") {
			return rootAbs
		}
		if rootArg == "" {
			return "."
		}
		return rootArg
	}

	rel := strings.TrimPrefix(currentAbs, rootAbs+"/")
	if strings.HasPrefix(rootArg, "/") {
		return currentAbs
	}
	if rootArg == "." {
		return "./" + rel
	}
	return path.Join(rootArg, rel)
}

func findNodeName(rootArg, rootAbs, currentAbs string, info stdfs.FileInfo) string {
	if currentAbs != rootAbs {
		return info.Name()
	}
	switch rootArg {
	case "", ".":
		return "."
	case "/":
		return "/"
	default:
		return path.Base(rootArg)
	}
}

func findHasPrintAction(actions []findAction) bool {
	for _, action := range actions {
		if _, ok := action.(*findPrintAction); ok {
			return true
		}
	}
	return false
}

func findHasPrintfAction(actions []findAction) bool {
	for _, action := range actions {
		if _, ok := action.(*findPrintfAction); ok {
			return true
		}
	}
	return false
}

func formatFindPrintf(format string, item *findPrintData) string {
	processed, _, _ := decodeEscapes(format)

	var out strings.Builder
	for i := 0; i < len(processed); {
		if processed[i] != '%' || i+1 >= len(processed) {
			out.WriteByte(processed[i])
			i++
			continue
		}

		if processed[i+1] == '%' {
			out.WriteByte('%')
			i += 2
			continue
		}

		width, precision, consumed := parseFindWidthPrecision(processed, i+1)
		i += 1 + consumed
		if i >= len(processed) {
			out.WriteByte('%')
			break
		}

		var value string
		switch processed[i] {
		case 'f':
			value = item.name
			i++
		case 'h':
			value = path.Dir(item.path)
			if value == "" {
				value = "."
			}
			i++
		case 'p':
			value = item.path
			i++
		case 'P':
			value = trimFindStartingPoint(item.path, item.startingPoint)
			i++
		case 's':
			value = fmt.Sprintf("%d", item.size)
			i++
		case 'd':
			value = fmt.Sprintf("%d", item.depth)
			i++
		case 'm':
			value = fmt.Sprintf("%o", item.mode.Perm())
			i++
		case 'M':
			value = formatModeLong(item.mode)
			i++
		case 't':
			value = item.mtime.Format("Mon Jan _2 15:04:05 2006")
			i++
		default:
			out.WriteByte('%')
			out.WriteByte(processed[i])
			i++
			continue
		}
		out.WriteString(applyFindWidth(value, width, precision))
	}
	return out.String()
}

func trimFindStartingPoint(pathValue, startingPoint string) string {
	switch {
	case pathValue == startingPoint:
		return ""
	case strings.HasPrefix(pathValue, startingPoint+"/"):
		return strings.TrimPrefix(pathValue, startingPoint+"/")
	case startingPoint == "." && strings.HasPrefix(pathValue, "./"):
		return strings.TrimPrefix(pathValue, "./")
	default:
		return pathValue
	}
}

func parseFindWidthPrecision(format string, start int) (width, precision, consumed int) {
	i := start
	leftJustify := false
	precision = -1

	if i < len(format) && format[i] == '-' {
		leftJustify = true
		i++
	}
	for i < len(format) && format[i] >= '0' && format[i] <= '9' {
		width = width*10 + int(format[i]-'0')
		i++
	}
	if i < len(format) && format[i] == '.' {
		i++
		precision = 0
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			precision = precision*10 + int(format[i]-'0')
			i++
		}
	}
	if leftJustify && width > 0 {
		width = -width
	}
	return width, precision, i - start
}

func applyFindWidth(value string, width, precision int) string {
	if precision >= 0 && len(value) > precision {
		value = value[:precision]
	}
	absWidth := width
	if absWidth < 0 {
		absWidth = -absWidth
	}
	if absWidth <= len(value) {
		return value
	}
	padding := strings.Repeat(" ", absWidth-len(value))
	if width < 0 {
		return value + padding
	}
	return padding + value
}

var _ Command = (*Find)(nil)
var _ SpecProvider = (*Find)(nil)
var _ ParsedRunner = (*Find)(nil)
var _ ParseInvocationNormalizer = (*Find)(nil)
