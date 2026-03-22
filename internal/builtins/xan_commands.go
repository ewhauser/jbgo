package builtins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	xanPartitionValueOnce sync.Once
	xanPartitionValueRe   *regexp.Regexp
)

func getXanPartitionValueRe() *regexp.Regexp {
	xanPartitionValueOnce.Do(func() {
		xanPartitionValueRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	})
	return xanPartitionValueRe
}

func xanRunHeaders(ctx context.Context, inv *Invocation, args []string) error {
	justNames := false
	fileArgs := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-j", "--just-names":
			justNames = true
		default:
			fileArgs = append(fileArgs, arg)
		}
	}

	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}

	for i, header := range table.Headers {
		if justNames {
			if _, err := fmt.Fprintln(inv.Stdout, header); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(inv.Stdout, "%d   %s\n", i, header); err != nil {
			return err
		}
	}
	return nil
}

func xanRunCount(ctx context.Context, inv *Invocation, args []string) error {
	table, err := xanReadTable(ctx, inv, xanFirstOperand(args))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(inv.Stdout, "%d\n", len(table.Rows))
	return err
}

func xanRunHead(ctx context.Context, inv *Invocation, args []string) error {
	n := 10
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "-n":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					n = parsed
					i++
					continue
				}
			}
		}
		fileArgs = append(fileArgs, args[i])
	}

	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	end := n
	rows := xanSliceRows(table.Rows, nil, &end)
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunTail(ctx context.Context, inv *Invocation, args []string) error {
	n := 10
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-l", "-n":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					n = parsed
					i++
					continue
				}
			}
		}
		fileArgs = append(fileArgs, args[i])
	}

	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	start := -n
	rows := xanSliceRows(table.Rows, &start, nil)
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunSlice(ctx context.Context, inv *Invocation, args []string) error {
	var (
		start  *int
		end    *int
		length *int
	)
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-s", "--start":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					start = &parsed
					i++
					continue
				}
			}
		case "-e", "--end":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					end = &parsed
					i++
					continue
				}
			}
		case "-l", "--len":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					length = &parsed
					i++
					continue
				}
			}
		}
		if !strings.HasPrefix(args[i], "-") {
			fileArgs = append(fileArgs, args[i])
		}
	}

	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	if length != nil {
		base := 0
		if start != nil {
			base = *start
		}
		value := base + *length
		end = &value
	}
	return xanWriteCSV(inv.Stdout, table.Headers, xanSliceRows(table.Rows, start, end))
}

func xanRunReverse(ctx context.Context, inv *Invocation, args []string) error {
	table, err := xanReadTable(ctx, inv, xanFirstOperand(args))
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(table.Rows))
	for i := len(table.Rows) - 1; i >= 0; i-- {
		rows = append(rows, xanCloneRow(table.Rows[i]))
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunBehead(ctx context.Context, inv *Invocation, args []string) error {
	table, err := xanReadTable(ctx, inv, xanFirstOperand(args))
	if err != nil {
		return err
	}
	return xanWriteRows(inv.Stdout, table.Rows)
}

func xanRunSample(ctx context.Context, inv *Invocation, args []string) error {
	var (
		size    *int
		seed    *int64
		fileArg string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--seed" && i+1 < len(args) {
			if parsed, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
				seed = &parsed
				i++
				continue
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if size == nil {
			if parsed, err := strconv.Atoi(arg); err == nil && parsed > 0 {
				size = &parsed
				continue
			}
		}
		if fileArg == "" {
			fileArg = arg
		}
	}

	if size == nil {
		return exitf(inv, 1, "xan sample: usage: xan sample <sample-size> [FILE]")
	}

	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	if len(table.Rows) <= *size {
		return xanWriteCSV(inv.Stdout, table.Headers, table.Rows)
	}

	rng := xanDefaultSeed()
	if seed != nil {
		rng = *seed
	}
	indices := make([]int, len(table.Rows))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := int(xanSeededStep(&rng) * float64(i+1))
		indices[i], indices[j] = indices[j], indices[i]
	}
	indices = indices[:*size]
	sort.Ints(indices)

	rows := make([][]string, 0, len(indices))
	for _, idx := range indices {
		rows = append(rows, xanCloneRow(table.Rows[idx]))
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunSelect(ctx context.Context, inv *Invocation, args []string) error {
	colSpec, fileArg := xanSplitPrimaryOperand(args)
	if colSpec == "" {
		return exitf(inv, 1, "xan select: no columns specified")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	return xanWriteCSV(inv.Stdout, xanSelectColumns(table, xanParseColumnSpec(colSpec, table.Headers)).Headers, xanSelectColumns(table, xanParseColumnSpec(colSpec, table.Headers)).Rows)
}

func xanRunDrop(ctx context.Context, inv *Invocation, args []string) error {
	colSpec, fileArg := xanSplitPrimaryOperand(args)
	if colSpec == "" {
		return exitf(inv, 1, "xan drop: no columns specified")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	drop := make(map[int]struct{})
	for _, idx := range xanParseColumnSpec(colSpec, table.Headers) {
		drop[idx] = struct{}{}
	}
	keep := make([]int, 0, len(table.Headers))
	for idx := range table.Headers {
		if _, ok := drop[idx]; !ok {
			keep = append(keep, idx)
		}
	}
	selected := xanSelectColumns(table, keep)
	return xanWriteCSV(inv.Stdout, selected.Headers, selected.Rows)
}

func xanRunRename(ctx context.Context, inv *Invocation, args []string) error {
	var (
		newNamesArg string
		selectSpec  string
		fileArg     string
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			selectSpec = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if newNamesArg == "" {
			newNamesArg = arg
			continue
		}
		if fileArg == "" {
			fileArg = arg
		}
	}
	if newNamesArg == "" {
		return exitf(inv, 1, "xan rename: no new name(s) specified")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	newHeaders := append([]string(nil), table.Headers...)
	newNames := strings.Split(newNamesArg, ",")
	if selectSpec != "" {
		oldCols := strings.Split(selectSpec, ",")
		renames := make(map[string]string, len(oldCols))
		for i := 0; i < len(oldCols) && i < len(newNames); i++ {
			renames[strings.TrimSpace(oldCols[i])] = strings.TrimSpace(newNames[i])
		}
		for i, header := range newHeaders {
			if replacement, ok := renames[header]; ok {
				newHeaders[i] = replacement
			}
		}
	} else {
		for i := 0; i < len(newHeaders) && i < len(newNames); i++ {
			newHeaders[i] = strings.TrimSpace(newNames[i])
		}
	}
	return xanWriteCSV(inv.Stdout, newHeaders, table.Rows)
}

func xanRunEnum(ctx context.Context, inv *Invocation, args []string) error {
	colName := "index"
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) {
			colName = args[i+1]
			i++
			continue
		}
		fileArgs = append(fileArgs, args[i])
	}
	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	headers := append([]string{colName}, table.Headers...)
	rows := make([][]string, 0, len(table.Rows))
	for idx, row := range table.Rows {
		next := make([]string, 0, len(row)+1)
		next = append(next, strconv.Itoa(idx))
		next = append(next, row...)
		rows = append(rows, next)
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunFilter(ctx context.Context, inv *Invocation, args []string) error {
	invert := false
	limit := 0
	exprText := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-v", "--invert":
			invert = true
		case "-l", "--limit":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					limit = parsed
					i++
					continue
				}
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if exprText == "" {
			exprText = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if exprText == "" {
		return exitf(inv, 1, "xan filter: no expression specified")
	}
	expr, err := xanParseExpr(exprText)
	if err != nil {
		return exitf(inv, 1, "xan filter: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	rows := make([][]string, 0)
	for rowIndex, row := range table.Rows {
		if limit > 0 && len(rows) >= limit {
			break
		}
		value, err := xanEvalRowExpr(table.Headers, row, rowIndex, expr, nil)
		if err != nil {
			return exitf(inv, 1, "xan filter: %v", err)
		}
		match := xanTruthy(value)
		if invert {
			match = !match
		}
		if match {
			rows = append(rows, xanCloneRow(row))
		}
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunSort(ctx context.Context, inv *Invocation, args []string) error {
	column := ""
	numeric := false
	reverse := false
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-N", "--numeric":
			numeric = true
		case "-R", "-r", "--reverse":
			reverse = true
		case "-s":
			if i+1 < len(args) {
				column = args[i+1]
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") {
			fileArgs = append(fileArgs, arg)
		}
	}
	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	if column == "" && len(table.Headers) > 0 {
		column = table.Headers[0]
	}
	colIdx := slices.Index(table.Headers, column)
	rows := make([][]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		rows = append(rows, xanCloneRow(row))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left := ""
		right := ""
		if colIdx >= 0 && colIdx < len(rows[i]) {
			left = rows[i][colIdx]
		}
		if colIdx >= 0 && colIdx < len(rows[j]) {
			right = rows[j][colIdx]
		}
		cmp := 0
		if numeric {
			lnum, _ := strconv.ParseFloat(left, 64)
			rnum, _ := strconv.ParseFloat(right, 64)
			switch {
			case lnum < rnum:
				cmp = -1
			case lnum > rnum:
				cmp = 1
			}
		} else {
			cmp = strings.Compare(left, right)
		}
		if reverse {
			return cmp > 0
		}
		return cmp < 0
	})
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunDedup(ctx context.Context, inv *Invocation, args []string) error {
	column := ""
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			column = args[i+1]
			i++
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			fileArgs = append(fileArgs, arg)
		}
	}
	table, err := xanReadTable(ctx, inv, xanFirstOperand(fileArgs))
	if err != nil {
		return err
	}
	colIdx := slices.Index(table.Headers, column)
	seen := make(map[string]struct{})
	rows := make([][]string, 0)
	for _, row := range table.Rows {
		key := strings.Join(row, "\x00")
		if column != "" {
			value := ""
			if colIdx >= 0 && colIdx < len(row) {
				value = row[colIdx]
			}
			key = value
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, xanCloneRow(row))
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunTop(ctx context.Context, inv *Invocation, args []string) error {
	n := 10
	column := ""
	reverse := false
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-l", "-n":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					n = parsed
					i++
					continue
				}
			}
		case "-R", "-r", "--reverse":
			reverse = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if column == "" {
			column = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	if column == "" && len(table.Headers) > 0 {
		column = table.Headers[0]
	}
	colIdx := slices.Index(table.Headers, column)
	rows := make([][]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		rows = append(rows, xanCloneRow(row))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left := 0.0
		right := 0.0
		if colIdx >= 0 && colIdx < len(rows[i]) {
			left, _ = strconv.ParseFloat(rows[i][colIdx], 64)
		}
		if colIdx >= 0 && colIdx < len(rows[j]) {
			right, _ = strconv.ParseFloat(rows[j][colIdx], 64)
		}
		if reverse {
			return left < right
		}
		return left > right
	})
	if n < len(rows) {
		rows = rows[:n]
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunMap(ctx context.Context, inv *Invocation, args []string) error {
	mapExpr := ""
	overwrite := false
	filter := false
	fileArg := ""
	for _, arg := range args {
		switch arg {
		case "-O", "--overwrite":
			overwrite = true
			continue
		case "--filter":
			filter = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if mapExpr == "" {
			mapExpr = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if mapExpr == "" {
		return exitf(inv, 1, "xan map: no expression specified")
	}
	specs, err := xanParseNamedExpressions(mapExpr)
	if err != nil {
		return exitf(inv, 1, "xan map: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}

	newHeaders := append([]string(nil), table.Headers...)
	if overwrite {
		for _, spec := range specs {
			if !slices.Contains(newHeaders, spec.Alias) {
				newHeaders = append(newHeaders, spec.Alias)
			}
		}
	} else {
		for _, spec := range specs {
			newHeaders = append(newHeaders, spec.Alias)
		}
	}

	rows := make([][]string, 0)
	for rowIndex, row := range table.Rows {
		computed := make(map[string]any, len(specs))
		skip := false
		for _, spec := range specs {
			value, err := xanEvalRowExpr(table.Headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return exitf(inv, 1, "xan map: %v", err)
			}
			if filter && value == nil {
				skip = true
				break
			}
			computed[spec.Alias] = value
		}
		if skip {
			continue
		}

		out := xanCloneRow(row)
		if overwrite {
			headerPos := make(map[string]int, len(newHeaders))
			for i, header := range newHeaders {
				headerPos[header] = i
			}
			if len(out) < len(newHeaders) {
				grown := make([]string, len(newHeaders))
				copy(grown, out)
				out = grown
			}
			for _, spec := range specs {
				out[headerPos[spec.Alias]] = xanValueString(computed[spec.Alias])
			}
		} else {
			for _, spec := range specs {
				out = append(out, xanValueString(computed[spec.Alias]))
			}
		}
		rows = append(rows, out)
	}
	return xanWriteCSV(inv.Stdout, newHeaders, rows)
}

func xanRunTransform(ctx context.Context, inv *Invocation, args []string) error {
	targetCol := ""
	transformExpr := ""
	rename := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-r" || arg == "--rename") && i+1 < len(args) {
			rename = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch {
		case targetCol == "":
			targetCol = arg
		case transformExpr == "":
			transformExpr = arg
		case fileArg == "":
			fileArg = arg
		}
	}
	if targetCol == "" || transformExpr == "" {
		return exitf(inv, 1, "xan transform: usage: xan transform COLUMN EXPR [FILE]")
	}

	expr, err := xanParseExpr(transformExpr)
	if err != nil {
		return exitf(inv, 1, "xan transform: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}

	targetCols := strings.Split(targetCol, ",")
	targetIdxs := make([]int, 0, len(targetCols))
	for _, col := range targetCols {
		col = strings.TrimSpace(col)
		idx := slices.Index(table.Headers, col)
		if idx < 0 {
			return exitf(inv, 1, "xan transform: column '%s' not found", col)
		}
		targetIdxs = append(targetIdxs, idx)
	}
	renameCols := []string{}
	if rename != "" {
		renameCols = strings.Split(rename, ",")
	}

	newHeaders := append([]string(nil), table.Headers...)
	for i, idx := range targetIdxs {
		if i < len(renameCols) && strings.TrimSpace(renameCols[i]) != "" {
			newHeaders[idx] = strings.TrimSpace(renameCols[i])
		}
	}

	rows := make([][]string, 0, len(table.Rows))
	for rowIndex, row := range table.Rows {
		out := xanCloneRow(row)
		for _, idx := range targetIdxs {
			current := ""
			if idx < len(row) {
				current = row[idx]
			}
			value, err := xanEvalRowExpr(table.Headers, row, rowIndex, expr, xanParseScalar(current))
			if err != nil {
				return exitf(inv, 1, "xan transform: %v", err)
			}
			out[idx] = xanValueString(value)
		}
		rows = append(rows, out)
	}
	return xanWriteCSV(inv.Stdout, newHeaders, rows)
}

func xanRunAgg(ctx context.Context, inv *Invocation, args []string) error {
	exprText := ""
	fileArg := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if exprText == "" {
			exprText = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if exprText == "" {
		return exitf(inv, 1, "xan agg: no aggregation expression")
	}
	specs, err := xanParseAggSpecs(exprText)
	if err != nil {
		return exitf(inv, 1, "xan agg: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	headers := make([]string, 0, len(specs))
	row := make([]string, 0, len(specs))
	for _, spec := range specs {
		value, err := xanComputeAgg(table.Headers, table.Rows, spec)
		if err != nil {
			return exitf(inv, 1, "xan agg: %v", err)
		}
		headers = append(headers, spec.Alias)
		row = append(row, xanValueString(value))
	}
	return xanWriteCSV(inv.Stdout, headers, [][]string{row})
}

func xanRunGroupby(ctx context.Context, inv *Invocation, args []string) error {
	groupCols := ""
	aggExpr := ""
	fileArg := ""
	for _, arg := range args {
		if arg == "--sorted" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch {
		case groupCols == "":
			groupCols = arg
		case aggExpr == "":
			aggExpr = arg
		case fileArg == "":
			fileArg = arg
		}
	}
	if groupCols == "" || aggExpr == "" {
		return exitf(inv, 1, "xan groupby: usage: xan groupby COLS EXPR [FILE]")
	}
	specs, err := xanParseAggSpecs(aggExpr)
	if err != nil {
		return exitf(inv, 1, "xan groupby: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}

	groupKeys := strings.Split(groupCols, ",")
	groupNames := make([]string, 0, len(groupKeys))
	groupIdxs := make([]int, 0, len(groupKeys))
	for _, key := range groupKeys {
		key = strings.TrimSpace(key)
		idx := slices.Index(table.Headers, key)
		if idx < 0 {
			return exitf(inv, 1, "xan groupby: column '%s' not found", key)
		}
		groupNames = append(groupNames, key)
		groupIdxs = append(groupIdxs, idx)
	}
	groupOrder := make([]string, 0)
	groups := make(map[string][][]string)
	for _, row := range table.Rows {
		parts := make([]string, 0, len(groupIdxs))
		for _, idx := range groupIdxs {
			value := ""
			if idx < len(row) {
				value = row[idx]
			}
			parts = append(parts, value)
		}
		groupKey := strings.Join(parts, "\x00")
		if _, ok := groups[groupKey]; !ok {
			groupOrder = append(groupOrder, groupKey)
		}
		groups[groupKey] = append(groups[groupKey], xanCloneRow(row))
	}

	headers := make([]string, 0, len(groupNames)+len(specs))
	headers = append(headers, groupNames...)
	for _, spec := range specs {
		headers = append(headers, spec.Alias)
	}

	rows := make([][]string, 0, len(groupOrder))
	for _, groupKey := range groupOrder {
		groupRows := groups[groupKey]
		next := strings.Split(groupKey, "\x00")
		for _, spec := range specs {
			value, err := xanComputeAgg(table.Headers, groupRows, spec)
			if err != nil {
				return exitf(inv, 1, "xan groupby: %v", err)
			}
			next = append(next, xanValueString(value))
		}
		rows = append(rows, next)
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunFrequency(ctx context.Context, inv *Invocation, args []string) error {
	selectCols := []string{}
	groupCol := ""
	limit := 10
	noExtra := false
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s", "--select":
			if i+1 < len(args) {
				selectCols = strings.Split(args[i+1], ",")
				i++
				continue
			}
		case "-g", "--groupby":
			if i+1 < len(args) {
				groupCol = args[i+1]
				i++
				continue
			}
		case "-l", "--limit":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					limit = parsed
					i++
					continue
				}
			}
		case "--no-extra":
			noExtra = true
			continue
		case "-A", "--all":
			limit = 0
			continue
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}

	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}

	targetCols := selectCols
	if len(targetCols) == 0 {
		targetCols = append([]string(nil), table.Headers...)
		if groupCol != "" {
			filtered := targetCols[:0]
			for _, header := range targetCols {
				if header != groupCol {
					filtered = append(filtered, header)
				}
			}
			targetCols = filtered
		}
	}

	var (
		headers []string
		rows    [][]string
	)
	if groupCol != "" {
		headers = []string{"field", groupCol, "value", "count"}
		groupIdx := slices.Index(table.Headers, groupCol)
		groupOrder := make([]string, 0)
		groups := make(map[string][][]string)
		for _, row := range table.Rows {
			groupValue := ""
			if groupIdx >= 0 && groupIdx < len(row) {
				groupValue = row[groupIdx]
			}
			if _, ok := groups[groupValue]; !ok {
				groupOrder = append(groupOrder, groupValue)
			}
			groups[groupValue] = append(groups[groupValue], row)
		}

		for _, col := range targetCols {
			col = strings.TrimSpace(col)
			colIdx := slices.Index(table.Headers, col)
			for _, groupValue := range groupOrder {
				entries := xanFrequencyEntries(groups[groupValue], colIdx, noExtra)
				if limit > 0 && len(entries) > limit {
					entries = entries[:limit]
				}
				for _, entry := range entries {
					rows = append(rows, []string{col, groupValue, entry.Value, strconv.Itoa(entry.Count)})
				}
			}
		}
	} else {
		headers = []string{"field", "value", "count"}
		for _, col := range targetCols {
			col = strings.TrimSpace(col)
			colIdx := slices.Index(table.Headers, col)
			entries := xanFrequencyEntries(table.Rows, colIdx, noExtra)
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			for _, entry := range entries {
				rows = append(rows, []string{col, entry.Value, strconv.Itoa(entry.Count)})
			}
		}
	}

	return xanWriteCSV(inv.Stdout, headers, rows)
}

type xanFreqEntry struct {
	Value string
	Count int
}

func xanFrequencyEntries(rows [][]string, colIdx int, noExtra bool) []xanFreqEntry {
	counts := make(map[string]int)
	for _, row := range rows {
		value := ""
		if colIdx >= 0 && colIdx < len(row) {
			value = row[colIdx]
		}
		counts[value]++
	}
	entries := make([]xanFreqEntry, 0, len(counts))
	for value, count := range counts {
		if noExtra && value == "" {
			continue
		}
		display := value
		if display == "" {
			display = "<empty>"
		}
		entries = append(entries, xanFreqEntry{Value: display, Count: count})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Value < entries[j].Value
	})
	return entries
}

func xanRunStats(ctx context.Context, inv *Invocation, args []string) error {
	columns := []string{}
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-s" && i+1 < len(args) {
			columns = strings.Split(args[i+1], ",")
			i++
			continue
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	targetCols := columns
	if len(targetCols) == 0 {
		targetCols = append([]string(nil), table.Headers...)
	}
	rows := make([][]string, 0, len(targetCols))
	for _, col := range targetCols {
		col = strings.TrimSpace(col)
		colIdx := slices.Index(table.Headers, col)
		values := make([]string, 0, len(table.Rows))
		nums := make([]float64, 0, len(table.Rows))
		for _, row := range table.Rows {
			value := ""
			if colIdx >= 0 && colIdx < len(row) {
				value = row[colIdx]
			}
			values = append(values, value)
			if value == "" {
				continue
			}
			if num, err := strconv.ParseFloat(value, 64); err == nil {
				nums = append(nums, num)
			}
		}
		row := []string{col, "String", strconv.Itoa(len(values)), "", "", ""}
		if len(values) > 0 && len(nums) == len(values) {
			row[1] = "Number"
			minValue := nums[0]
			maxValue := nums[0]
			sum := 0.0
			for _, num := range nums {
				if num < minValue {
					minValue = num
				}
				if num > maxValue {
					maxValue = num
				}
				sum += num
			}
			row[3] = xanValueString(minValue)
			row[4] = xanValueString(maxValue)
			row[5] = xanValueString(sum / float64(len(nums)))
		}
		rows = append(rows, row)
	}
	return xanWriteCSV(inv.Stdout, []string{"field", "type", "count", "min", "max", "mean"}, rows)
}

func xanRunSearch(ctx context.Context, inv *Invocation, args []string) error {
	pattern := ""
	selectCols := []string{}
	invert := false
	ignoreCase := false
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s", "--select":
			if i+1 < len(args) {
				selectCols = strings.Split(args[i+1], ",")
				i++
				continue
			}
		case "-v", "--invert":
			invert = true
			continue
		case "-i", "--ignore-case":
			ignoreCase = true
			continue
		case "-r", "--regex":
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if pattern == "" {
			pattern = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if pattern == "" {
		return exitf(inv, 1, "xan search: no pattern specified")
	}
	prefix := ""
	if ignoreCase {
		prefix = "(?i)"
	}
	re, err := regexp.Compile(prefix + pattern)
	if err != nil {
		return exitf(inv, 1, "xan search: invalid regex pattern '%s'", pattern)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	searchHeaders := selectCols
	if len(searchHeaders) == 0 {
		searchHeaders = append([]string(nil), table.Headers...)
	}
	searchIdxs := make([]int, 0, len(searchHeaders))
	for _, header := range searchHeaders {
		if idx := slices.Index(table.Headers, strings.TrimSpace(header)); idx >= 0 {
			searchIdxs = append(searchIdxs, idx)
		}
	}
	rows := make([][]string, 0)
	for _, row := range table.Rows {
		matches := false
		for _, idx := range searchIdxs {
			if idx < len(row) && re.MatchString(row[idx]) {
				matches = true
				break
			}
		}
		if invert {
			matches = !matches
		}
		if matches {
			rows = append(rows, xanCloneRow(row))
		}
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunFlatmap(ctx context.Context, inv *Invocation, args []string) error {
	exprText := ""
	fileArg := ""
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if exprText == "" {
			exprText = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if exprText == "" {
		return exitf(inv, 1, "xan flatmap: no expression specified")
	}
	specs, err := xanParseNamedExpressions(exprText)
	if err != nil {
		return exitf(inv, 1, "xan flatmap: %v", err)
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	headers := append([]string(nil), table.Headers...)
	for _, spec := range specs {
		headers = append(headers, spec.Alias)
	}
	rows := make([][]string, 0)
	for rowIndex, row := range table.Rows {
		results := make([][]any, 0, len(specs))
		maxLen := 1
		for _, spec := range specs {
			value, err := xanEvalRowExpr(table.Headers, row, rowIndex, spec.Expr, nil)
			if err != nil {
				return exitf(inv, 1, "xan flatmap: %v", err)
			}
			var expanded []any
			switch v := value.(type) {
			case []any:
				expanded = append(expanded, v...)
			case []string:
				for _, item := range v {
					expanded = append(expanded, item)
				}
			case nil:
				expanded = nil
			default:
				expanded = []any{v}
			}
			if len(expanded) > maxLen {
				maxLen = len(expanded)
			}
			results = append(results, expanded)
		}
		for i := 0; i < maxLen; i++ {
			next := xanCloneRow(row)
			for _, expanded := range results {
				value := ""
				if i < len(expanded) {
					value = xanValueString(expanded[i])
				}
				next = append(next, value)
			}
			rows = append(rows, next)
		}
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunExplode(ctx context.Context, inv *Invocation, args []string) error {
	column := ""
	separator := "|"
	dropEmpty := false
	rename := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s", "--separator":
			if i+1 < len(args) {
				separator = args[i+1]
				i++
				continue
			}
		case "--drop-empty":
			dropEmpty = true
			continue
		case "-r", "--rename":
			if i+1 < len(args) {
				rename = args[i+1]
				i++
				continue
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if column == "" {
			column = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if column == "" {
		return exitf(inv, 1, "xan explode: usage: xan explode COLUMN [FILE]")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	colIdx := slices.Index(table.Headers, column)
	if colIdx < 0 {
		return exitf(inv, 1, "xan explode: column '%s' not found", column)
	}
	headers := append([]string(nil), table.Headers...)
	if rename != "" {
		headers[colIdx] = rename
	}
	rows := make([][]string, 0)
	for _, row := range table.Rows {
		value := ""
		if colIdx < len(row) {
			value = row[colIdx]
		}
		if value == "" {
			if dropEmpty {
				continue
			}
			next := xanCloneRow(row)
			rows = append(rows, next)
			continue
		}
		for part := range strings.SplitSeq(value, separator) {
			next := xanCloneRow(row)
			if colIdx >= len(next) {
				grown := make([]string, len(table.Headers))
				copy(grown, next)
				next = grown
			}
			next[colIdx] = part
			rows = append(rows, next)
		}
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunImplode(ctx context.Context, inv *Invocation, args []string) error {
	column := ""
	separator := "|"
	rename := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-s", "--sep":
			if i+1 < len(args) {
				separator = args[i+1]
				i++
				continue
			}
		case "-r", "--rename":
			if i+1 < len(args) {
				rename = args[i+1]
				i++
				continue
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if column == "" {
			column = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if column == "" {
		return exitf(inv, 1, "xan implode: usage: xan implode COLUMN [FILE]")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	colIdx := slices.Index(table.Headers, column)
	if colIdx < 0 {
		return exitf(inv, 1, "xan implode: column '%s' not found", column)
	}
	headers := append([]string(nil), table.Headers...)
	if rename != "" {
		headers[colIdx] = rename
	}

	keyIdxs := make([]int, 0, len(table.Headers)-1)
	for idx := range table.Headers {
		if idx != colIdx {
			keyIdxs = append(keyIdxs, idx)
		}
	}

	rows := make([][]string, 0)
	var (
		currentKey    string
		currentValues []string
		currentRow    []string
	)
	flush := func() {
		if currentRow == nil {
			return
		}
		next := xanCloneRow(currentRow)
		next[colIdx] = strings.Join(currentValues, separator)
		rows = append(rows, next)
	}
	for _, row := range table.Rows {
		parts := make([]string, 0, len(keyIdxs))
		for _, idx := range keyIdxs {
			if idx < len(row) {
				parts = append(parts, row[idx])
			} else {
				parts = append(parts, "")
			}
		}
		key := strings.Join(parts, "\x00")
		value := ""
		if colIdx < len(row) {
			value = row[colIdx]
		}
		if currentRow == nil || key != currentKey {
			flush()
			currentKey = key
			currentValues = []string{value}
			currentRow = xanCloneRow(row)
			continue
		}
		currentValues = append(currentValues, value)
	}
	flush()
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunPivot(ctx context.Context, inv *Invocation, args []string) error {
	pivotCol := ""
	aggExpr := ""
	groupCols := []string{}
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-g" || arg == "--groupby") && i+1 < len(args) {
			groupCols = strings.Split(args[i+1], ",")
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch {
		case pivotCol == "":
			pivotCol = arg
		case aggExpr == "":
			aggExpr = arg
		case fileArg == "":
			fileArg = arg
		}
	}
	if pivotCol == "" || aggExpr == "" {
		return exitf(inv, 1, "xan pivot: usage: xan pivot COLUMN AGG_EXPR [OPTIONS] [FILE]")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	pivotIdx := slices.Index(table.Headers, pivotCol)
	if pivotIdx < 0 {
		return exitf(inv, 1, "xan pivot: column '%s' not found", pivotCol)
	}
	funcName, aggInner, err := xanParseAggCall(aggExpr)
	if err != nil {
		return exitf(inv, 1, "xan pivot: invalid aggregation expression '%s'", aggExpr)
	}
	aggTarget := strings.TrimSpace(aggInner)
	aggIdx := slices.Index(table.Headers, aggTarget)
	if aggIdx < 0 {
		return exitf(inv, 1, "xan pivot: invalid aggregation expression '%s'", aggExpr)
	}
	if len(groupCols) == 0 {
		for _, header := range table.Headers {
			if header != pivotCol && header != aggTarget {
				groupCols = append(groupCols, header)
			}
		}
	}
	groupIdxs := make([]int, 0, len(groupCols))
	for _, col := range groupCols {
		groupIdxs = append(groupIdxs, slices.Index(table.Headers, strings.TrimSpace(col)))
	}
	pivotValues := make([]string, 0)
	pivotSeen := make(map[string]struct{})
	groupOrder := make([]string, 0)
	groups := make(map[string]map[string][]string)
	for _, row := range table.Rows {
		pivotValue := ""
		if pivotIdx < len(row) {
			pivotValue = row[pivotIdx]
		}
		if _, ok := pivotSeen[pivotValue]; !ok {
			pivotSeen[pivotValue] = struct{}{}
			pivotValues = append(pivotValues, pivotValue)
		}
		groupParts := make([]string, 0, len(groupIdxs))
		for _, idx := range groupIdxs {
			if idx >= 0 && idx < len(row) {
				groupParts = append(groupParts, row[idx])
			} else {
				groupParts = append(groupParts, "")
			}
		}
		groupKey := strings.Join(groupParts, "\x00")
		if _, ok := groups[groupKey]; !ok {
			groups[groupKey] = make(map[string][]string)
			groupOrder = append(groupOrder, groupKey)
		}
		value := ""
		if aggIdx < len(row) {
			value = row[aggIdx]
		}
		groups[groupKey][pivotValue] = append(groups[groupKey][pivotValue], value)
	}
	headers := append(append([]string(nil), groupCols...), pivotValues...)
	rows := make([][]string, 0, len(groupOrder))
	for _, groupKey := range groupOrder {
		next := strings.Split(groupKey, "\x00")
		for _, pivotValue := range pivotValues {
			value := xanComputeSimpleAgg(strings.ToLower(funcName), groups[groupKey][pivotValue])
			next = append(next, xanValueString(value))
		}
		rows = append(rows, next)
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanComputeSimpleAgg(name string, values []string) any {
	switch name {
	case "count":
		return len(values)
	case "first":
		if len(values) == 0 {
			return nil
		}
		return values[0]
	case "last":
		if len(values) == 0 {
			return nil
		}
		return values[len(values)-1]
	}
	nums := make([]float64, 0, len(values))
	for _, value := range values {
		if num, err := strconv.ParseFloat(value, 64); err == nil {
			nums = append(nums, num)
		}
	}
	switch name {
	case "sum":
		total := 0.0
		for _, num := range nums {
			total += num
		}
		return total
	case "mean", "avg":
		if len(nums) == 0 {
			return nil
		}
		total := 0.0
		for _, num := range nums {
			total += num
		}
		return total / float64(len(nums))
	case "min":
		if len(nums) == 0 {
			return nil
		}
		best := nums[0]
		for _, num := range nums[1:] {
			if num < best {
				best = num
			}
		}
		return best
	case "max":
		if len(nums) == 0 {
			return nil
		}
		best := nums[0]
		for _, num := range nums[1:] {
			if num > best {
				best = num
			}
		}
		return best
	default:
		return nil
	}
}

func xanRunCat(ctx context.Context, inv *Invocation, args []string) error {
	pad := false
	fileArgs := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-p", "--pad":
			pad = true
		default:
			if !strings.HasPrefix(arg, "-") {
				fileArgs = append(fileArgs, arg)
			}
		}
	}
	if len(fileArgs) == 0 {
		return exitf(inv, 1, "xan cat: no files specified")
	}
	tables := make([]*xanTable, 0, len(fileArgs))
	allHeaders := make([]string, 0)
	for _, fileArg := range fileArgs {
		table, err := xanReadTable(ctx, inv, fileArg)
		if err != nil {
			return err
		}
		tables = append(tables, table)
		for _, header := range table.Headers {
			if !slices.Contains(allHeaders, header) {
				allHeaders = append(allHeaders, header)
			}
		}
	}
	if !pad {
		base := strings.Join(tables[0].Headers, "\x00")
		for _, table := range tables[1:] {
			if strings.Join(table.Headers, "\x00") != base {
				return exitf(inv, 1, "xan cat: headers do not match (use -p to pad)")
			}
		}
		allHeaders = append([]string(nil), tables[0].Headers...)
	}
	rows := make([][]string, 0)
	for _, table := range tables {
		headerPos := make(map[string]int, len(table.Headers))
		for i, header := range table.Headers {
			headerPos[header] = i
		}
		for _, row := range table.Rows {
			next := make([]string, len(allHeaders))
			for i, header := range allHeaders {
				if idx, ok := headerPos[header]; ok && idx < len(row) {
					next[i] = row[idx]
				}
			}
			rows = append(rows, next)
		}
	}
	return xanWriteCSV(inv.Stdout, allHeaders, rows)
}

func xanRunJoin(ctx context.Context, inv *Invocation, args []string) error {
	key1, file1, key2, file2 := "", "", "", ""
	joinType := "inner"
	defaultValue := ""
	positional := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--left":
			joinType = "left"
			continue
		case "--right":
			joinType = "right"
			continue
		case "--full":
			joinType = "full"
			continue
		case "-D", "--default":
			if i+1 < len(args) {
				defaultValue = args[i+1]
				i++
				continue
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positional++
		switch positional {
		case 1:
			key1 = arg
		case 2:
			file1 = arg
		case 3:
			key2 = arg
		case 4:
			file2 = arg
		}
	}
	if key1 == "" || file1 == "" || key2 == "" || file2 == "" {
		return exitf(inv, 1, "xan join: usage: xan join KEY1 FILE1 KEY2 FILE2 [OPTIONS]")
	}
	left, err := xanReadTable(ctx, inv, file1)
	if err != nil {
		return err
	}
	right, err := xanReadTable(ctx, inv, file2)
	if err != nil {
		return err
	}
	key1Idx := slices.Index(left.Headers, key1)
	if key1Idx < 0 {
		return exitf(inv, 1, "xan join: column '%s' not found in first file", key1)
	}
	key2Idx := slices.Index(right.Headers, key2)
	if key2Idx < 0 {
		return exitf(inv, 1, "xan join: column '%s' not found in second file", key2)
	}

	indexRight := make(map[string][][]string)
	for _, row := range right.Rows {
		key := ""
		if key2Idx < len(row) {
			key = row[key2Idx]
		}
		indexRight[key] = append(indexRight[key], row)
	}

	rightUniqueHeaders := make([]string, 0, len(right.Headers))
	leftHeaderSet := make(map[string]struct{}, len(left.Headers))
	for _, header := range left.Headers {
		leftHeaderSet[header] = struct{}{}
	}
	for _, header := range right.Headers {
		if _, ok := leftHeaderSet[header]; !ok {
			rightUniqueHeaders = append(rightUniqueHeaders, header)
		}
	}
	headers := append(append([]string(nil), left.Headers...), rightUniqueHeaders...)

	matchedRight := make(map[string]struct{})
	rows := make([][]string, 0)
	for _, leftRow := range left.Rows {
		key := ""
		if key1Idx < len(leftRow) {
			key = leftRow[key1Idx]
		}
		matches := indexRight[key]
		if len(matches) > 0 {
			matchedRight[key] = struct{}{}
			for _, rightRow := range matches {
				next := make([]string, len(headers))
				copy(next, leftRow)
				for i, header := range rightUniqueHeaders {
					idx := slices.Index(right.Headers, header)
					if idx >= 0 && idx < len(rightRow) {
						next[len(left.Headers)+i] = rightRow[idx]
					}
				}
				rows = append(rows, next)
			}
			continue
		}
		if joinType == "left" || joinType == "full" {
			next := make([]string, len(headers))
			copy(next, leftRow)
			for i := len(left.Headers); i < len(next); i++ {
				next[i] = defaultValue
			}
			rows = append(rows, next)
		}
	}
	if joinType == "right" || joinType == "full" {
		for _, rightRow := range right.Rows {
			key := ""
			if key2Idx < len(rightRow) {
				key = rightRow[key2Idx]
			}
			if _, ok := matchedRight[key]; ok {
				continue
			}
			next := make([]string, len(headers))
			for i, header := range left.Headers {
				if idx := slices.Index(right.Headers, header); idx >= 0 && idx < len(rightRow) {
					next[i] = rightRow[idx]
				} else {
					next[i] = defaultValue
				}
			}
			for i, header := range rightUniqueHeaders {
				if idx := slices.Index(right.Headers, header); idx >= 0 && idx < len(rightRow) {
					next[len(left.Headers)+i] = rightRow[idx]
				}
			}
			rows = append(rows, next)
		}
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunMerge(ctx context.Context, inv *Invocation, args []string) error {
	sortCol := ""
	fileArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-s" || arg == "--sort") && i+1 < len(args) {
			sortCol = args[i+1]
			i++
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			fileArgs = append(fileArgs, arg)
		}
	}
	if len(fileArgs) < 2 {
		return exitf(inv, 1, "xan merge: usage: xan merge [OPTIONS] FILE1 FILE2 ...")
	}
	var (
		commonHeaders []string
		rows          [][]string
	)
	for _, fileArg := range fileArgs {
		table, err := xanReadTable(ctx, inv, fileArg)
		if err != nil {
			return err
		}
		if commonHeaders == nil {
			commonHeaders = append([]string(nil), table.Headers...)
		} else if strings.Join(commonHeaders, "\x00") != strings.Join(table.Headers, "\x00") {
			return exitf(inv, 1, "xan merge: all files must have the same headers")
		}
		for _, row := range table.Rows {
			rows = append(rows, xanCloneRow(row))
		}
	}
	if sortCol != "" {
		colIdx := slices.Index(commonHeaders, sortCol)
		if colIdx < 0 {
			return exitf(inv, 1, "xan merge: column '%s' not found", sortCol)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			left := ""
			right := ""
			if colIdx < len(rows[i]) {
				left = rows[i][colIdx]
			}
			if colIdx < len(rows[j]) {
				right = rows[j][colIdx]
			}
			lnum, lerr := strconv.ParseFloat(left, 64)
			rnum, rerr := strconv.ParseFloat(right, 64)
			if lerr == nil && rerr == nil {
				return lnum < rnum
			}
			return left < right
		})
	}
	return xanWriteCSV(inv.Stdout, commonHeaders, rows)
}

func xanRunTo(ctx context.Context, inv *Invocation, args []string) error {
	if len(args) == 0 {
		return exitf(inv, 1, "xan to: usage: xan to <format> [FILE]")
	}
	format := args[0]
	if format != "json" {
		return exitf(inv, 1, "xan to: unsupported format '%s'", format)
	}
	table, err := xanReadTable(ctx, inv, xanFirstOperand(args[1:]))
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(table.Rows))
	for _, row := range table.Rows {
		item := make(map[string]any, len(table.Headers))
		for i, header := range table.Headers {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			item[header] = xanValueJSON(xanParseScalar(value))
		}
		items = append(items, item)
	}
	return xanWriteJSON(inv.Stdout, items)
}

func xanRunFrom(ctx context.Context, inv *Invocation, args []string) error {
	format := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-f" || arg == "--format") && i+1 < len(args) {
			format = args[i+1]
			i++
			continue
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	if format == "" {
		return exitf(inv, 1, "xan from: usage: xan from -f <format> [FILE]")
	}
	if format != "json" {
		return exitf(inv, 1, "xan from: unsupported format '%s'", format)
	}
	data, err := xanReadText(ctx, inv, fileArg, "xan from")
	if err != nil {
		return err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return exitf(inv, 1, "xan from: invalid JSON input")
	}
	items, ok := parsed.([]any)
	if !ok {
		return exitf(inv, 1, "xan from: JSON input must be an array")
	}
	if len(items) == 0 {
		_, err := io.WriteString(inv.Stdout, "\n")
		return err
	}

	if firstRow, ok := items[0].([]any); ok {
		headers := make([]string, len(firstRow))
		for i, value := range firstRow {
			headers[i] = xanValueString(value)
		}
		rows := make([][]string, 0, len(items)-1)
		for _, item := range items[1:] {
			values, ok := item.([]any)
			if !ok {
				continue
			}
			row := make([]string, len(headers))
			for i := range headers {
				if i < len(values) {
					row[i] = xanValueString(values[i])
				}
			}
			rows = append(rows, row)
		}
		return xanWriteCSV(inv.Stdout, headers, rows)
	}

	firstObj, ok := items[0].(map[string]any)
	if !ok {
		return exitf(inv, 1, "xan from: invalid JSON input")
	}
	headers := make([]string, 0, len(firstObj))
	for key := range firstObj {
		headers = append(headers, key)
	}
	sort.Strings(headers)
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		row := make([]string, len(headers))
		for i, header := range headers {
			row[i] = xanValueString(obj[header])
		}
		rows = append(rows, row)
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunTranspose(ctx context.Context, inv *Invocation, args []string) error {
	table, err := xanReadTable(ctx, inv, xanFirstOperand(args))
	if err != nil {
		return err
	}
	if len(table.Headers) == 0 {
		return nil
	}
	if len(table.Rows) == 0 {
		rows := make([][]string, 0, len(table.Headers))
		for _, header := range table.Headers {
			rows = append(rows, []string{header})
		}
		return xanWriteCSV(inv.Stdout, []string{"column"}, rows)
	}

	firstCol := table.Headers[0]
	headers := []string{firstCol}
	for i, row := range table.Rows {
		value := fmt.Sprintf("row_%d", i)
		if len(row) > 0 && row[0] != "" {
			value = row[0]
		}
		headers = append(headers, value)
	}
	rows := make([][]string, 0, max(len(table.Headers)-1, 0))
	for col := 1; col < len(table.Headers); col++ {
		next := []string{table.Headers[col]}
		for _, row := range table.Rows {
			value := ""
			if col < len(row) {
				value = row[col]
			}
			next = append(next, value)
		}
		rows = append(rows, next)
	}
	return xanWriteCSV(inv.Stdout, headers, rows)
}

func xanRunShuffle(ctx context.Context, inv *Invocation, args []string) error {
	var seed *int64
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--seed" && i+1 < len(args) {
			if parsed, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
				seed = &parsed
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		rows = append(rows, xanCloneRow(row))
	}
	rng := xanDefaultSeed()
	if seed != nil {
		rng = *seed
	}
	for i := len(rows) - 1; i > 0; i-- {
		j := int(xanSeededStep(&rng) * float64(i+1))
		rows[i], rows[j] = rows[j], rows[i]
	}
	return xanWriteCSV(inv.Stdout, table.Headers, rows)
}

func xanRunFixlengths(ctx context.Context, inv *Invocation, args []string) error {
	var targetLen *int
	defaultValue := ""
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-l", "--length":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					targetLen = &parsed
					i++
					continue
				}
			}
		case "-d", "--default":
			if i+1 < len(args) {
				defaultValue = args[i+1]
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	records, err := xanReadRawCSVRecords(ctx, inv, fileArg, "xan fixlengths")
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	width := 0
	for _, record := range records {
		if len(record) > width {
			width = len(record)
		}
	}
	if targetLen != nil {
		width = *targetLen
	}
	fixed := make([][]string, 0, len(records))
	for _, record := range records {
		next := append([]string(nil), record...)
		if len(next) < width {
			for len(next) < width {
				next = append(next, defaultValue)
			}
		} else if len(next) > width {
			next = next[:width]
		}
		fixed = append(fixed, next)
	}
	return xanWriteRows(inv.Stdout, fixed)
}

func xanRunSplit(ctx context.Context, inv *Invocation, args []string) error {
	var numParts, partSize *int
	outputDir := "."
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-c", "--chunks":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					numParts = &parsed
					i++
					continue
				}
			}
		case "-S", "--size":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					partSize = &parsed
					i++
					continue
				}
			}
		case "-o", "--output":
			if i+1 < len(args) {
				outputDir = args[i+1]
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	if numParts == nil && partSize == nil {
		return exitf(inv, 1, "xan split: must specify -c or -S")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	var parts [][][]string
	if numParts != nil {
		size := 0
		if *numParts > 0 {
			size = (len(table.Rows) + *numParts - 1) / *numParts
		}
		for i := 0; i < *numParts; i++ {
			start := i * size
			end := start + size
			if start >= len(table.Rows) {
				break
			}
			end = min(end, len(table.Rows))
			parts = append(parts, table.Rows[start:end])
		}
	} else if partSize != nil && *partSize > 0 {
		for i := 0; i < len(table.Rows); i += *partSize {
			end := i + *partSize
			end = min(end, len(table.Rows))
			parts = append(parts, table.Rows[i:end])
		}
	}
	if len(parts) == 0 {
		_, err := fmt.Fprintln(inv.Stdout, "Split into 0 parts")
		return err
	}
	baseName := "part"
	if fileArg != "" {
		baseName = path.Base(fileArg)
		baseName = strings.TrimSuffix(baseName, ".csv")
	}
	outputPath := inv.FS.Resolve(outputDir)
	if err := inv.FS.MkdirAll(ctx, outputPath, 0o755); err == nil {
		writeErr := false
		for i, partRows := range parts {
			part := &xanTable{Headers: table.Headers, Rows: partRows}
			data, err := xanTableCSV(part)
			if err != nil {
				return err
			}
			name := path.Join(outputPath, fmt.Sprintf("%s_%03d.csv", baseName, i+1))
			if err := xanWriteSandboxFile(ctx, inv, name, data); err != nil {
				writeErr = true
				break
			}
		}
		if !writeErr {
			_, err := fmt.Fprintf(inv.Stdout, "Split into %d parts\n", len(parts))
			return err
		}
	}
	for i, partRows := range parts {
		if _, err := fmt.Fprintf(inv.Stdout, "Part %d: %d rows\n", i+1, len(partRows)); err != nil {
			return err
		}
	}
	return nil
}

func xanRunPartition(ctx context.Context, inv *Invocation, args []string) error {
	column := ""
	outputDir := "."
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-o" || arg == "--output") && i+1 < len(args) {
			outputDir = args[i+1]
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if column == "" {
			column = arg
		} else if fileArg == "" {
			fileArg = arg
		}
	}
	if column == "" {
		return exitf(inv, 1, "xan partition: usage: xan partition COLUMN [FILE]")
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	colIdx := slices.Index(table.Headers, column)
	if colIdx < 0 {
		return exitf(inv, 1, "xan partition: column '%s' not found", column)
	}
	groupOrder := make([]string, 0)
	groups := make(map[string][][]string)
	for _, row := range table.Rows {
		value := ""
		if colIdx < len(row) {
			value = row[colIdx]
		}
		if _, ok := groups[value]; !ok {
			groupOrder = append(groupOrder, value)
		}
		groups[value] = append(groups[value], xanCloneRow(row))
	}
	outputPath := inv.FS.Resolve(outputDir)
	if err := inv.FS.MkdirAll(ctx, outputPath, 0o755); err == nil {
		filenames := make(map[string]string, len(groupOrder))
		namesByValue := make(map[string]string, len(groupOrder))
		for _, value := range groupOrder {
			name := xanSanitizePartitionValue(value)
			if previous, ok := filenames[name]; ok && previous != value {
				return exitf(inv, 1, "xan partition: values %q and %q map to the same output file %q", previous, value, name+".csv")
			}
			filenames[name] = value
			namesByValue[value] = name
		}
		writeErr := false
		for _, value := range groupOrder {
			part := &xanTable{Headers: table.Headers, Rows: groups[value]}
			data, err := xanTableCSV(part)
			if err != nil {
				return err
			}
			if err := xanWriteSandboxFile(ctx, inv, path.Join(outputPath, namesByValue[value]+".csv"), data); err != nil {
				writeErr = true
				break
			}
		}
		if !writeErr {
			_, err := fmt.Fprintf(inv.Stdout, "Partitioned into %d files by '%s'\n", len(groupOrder), column)
			return err
		}
	}
	for _, value := range groupOrder {
		if _, err := fmt.Fprintf(inv.Stdout, "%s: %d rows\n", value, len(groups[value])); err != nil {
			return err
		}
	}
	return nil
}

func xanSanitizePartitionValue(value string) string {
	value = getXanPartitionValueRe().ReplaceAllString(value, "_")
	if value == "" {
		return "empty"
	}
	return value
}

func xanRunFlatten(ctx context.Context, inv *Invocation, args []string) error {
	limit := 0
	selectCols := []string{}
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-l", "--limit":
			if i+1 < len(args) {
				if parsed, err := strconv.Atoi(args[i+1]); err == nil {
					limit = parsed
					i++
					continue
				}
			}
		case "-s", "--select":
			if i+1 < len(args) {
				selectCols = strings.Split(args[i+1], ",")
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	displayHeaders := table.Headers
	if len(selectCols) > 0 {
		filtered := make([]string, 0, len(selectCols))
		for _, header := range selectCols {
			header = strings.TrimSpace(header)
			if slices.Contains(table.Headers, header) {
				filtered = append(filtered, header)
			}
		}
		displayHeaders = filtered
	}
	rows := table.Rows
	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	width := 0
	for _, header := range displayHeaders {
		if len(header) > width {
			width = len(header)
		}
	}
	separator := strings.Repeat("─", 80)
	var b strings.Builder
	for rowIndex, row := range rows {
		fmt.Fprintf(&b, "Row n°%d\n", rowIndex)
		b.WriteString(separator)
		b.WriteByte('\n')
		for _, header := range displayHeaders {
			idx := slices.Index(table.Headers, header)
			value := ""
			if idx >= 0 && idx < len(row) {
				value = row[idx]
			}
			fmt.Fprintf(&b, "%-*s %s\n", width, header, value)
		}
		if rowIndex < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	_, err = io.WriteString(inv.Stdout, b.String())
	return err
}

func xanRunView(ctx context.Context, inv *Invocation, args []string) error {
	n := 0
	fileArg := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-n" && i+1 < len(args) {
			if parsed, err := strconv.Atoi(args[i+1]); err == nil {
				n = parsed
				i++
				continue
			}
		}
		if !strings.HasPrefix(arg, "-") && fileArg == "" {
			fileArg = arg
		}
	}
	table, err := xanReadTable(ctx, inv, fileArg)
	if err != nil {
		return err
	}
	rows := table.Rows
	if n > 0 && n < len(rows) {
		rows = rows[:n]
	}
	if len(table.Headers) == 0 {
		return nil
	}
	widths := make([]int, len(table.Headers))
	for i, header := range table.Headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i := range table.Headers {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}
	var b strings.Builder
	b.WriteString("┌")
	for i, width := range widths {
		if i > 0 {
			b.WriteString("┬")
		}
		b.WriteString(strings.Repeat("─", width+2))
	}
	b.WriteString("┐\n")
	b.WriteString("│")
	for i, header := range table.Headers {
		if i > 0 {
			b.WriteString("│")
		}
		fmt.Fprintf(&b, " %-*s ", widths[i], header)
	}
	b.WriteString("│\n")
	b.WriteString("├")
	for i, width := range widths {
		if i > 0 {
			b.WriteString("┼")
		}
		b.WriteString(strings.Repeat("─", width+2))
	}
	b.WriteString("┤\n")
	for _, row := range rows {
		b.WriteString("│")
		for i := range table.Headers {
			if i > 0 {
				b.WriteString("│")
			}
			value := ""
			if i < len(row) {
				value = row[i]
			}
			fmt.Fprintf(&b, " %-*s ", widths[i], value)
		}
		b.WriteString("│\n")
	}
	b.WriteString("└")
	for i, width := range widths {
		if i > 0 {
			b.WriteString("┴")
		}
		b.WriteString(strings.Repeat("─", width+2))
	}
	b.WriteString("┘\n")
	_, err = io.WriteString(inv.Stdout, b.String())
	return err
}

func xanRunFmt(ctx context.Context, inv *Invocation, args []string) error {
	return xanRunView(ctx, inv, args)
}
