// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

type declCommand struct {
	runner         *Runner
	clause         *syntax.DeclClause
	tracingEnabled bool
	trace          *tracer

	local          bool
	global         bool
	modes          []string
	valType        string
	declQuery      string
	underscore     string
	allowPlusFlags bool

	builtinTraceFields []string
	leadingTraceLines  []string
	trailingTraceLines []string

	declName         string
	onlyFlagOperands bool
	declEvalEnv      *overlayEnviron
}

func newDeclCommand(r *Runner, clause *syntax.DeclClause, tracingEnabled bool, trace *tracer) *declCommand {
	return &declCommand{
		runner:         r,
		clause:         clause,
		tracingEnabled: tracingEnabled,
		trace:          trace,
	}
}

func (d *declCommand) run() {
	if !d.initVariantState() {
		return
	}
	if !d.processOperands() {
		return
	}
	d.emitListingOrQueryOutput()
	d.finalizeUnderscore()
	d.emitXTraceOutput()
}

func (d *declCommand) initVariantState() bool {
	d.declName = d.clause.Variant.Value
	d.underscore = d.declName
	d.onlyFlagOperands = true
	d.builtinTraceFields = []string{d.declName}
	d.declEvalEnv = newOverlayEnviron(d.runner.writeEnv, true, d.runner.platform.UsesCaseInsensitiveEnv())

	switch d.declName {
	case "declare":
		// When used in a function, "declare" acts as "local"
		// unless the "-g" option is used.
		d.local = d.runner.inFunc
		d.allowPlusFlags = true
	case "local":
		if !d.runner.inFunc {
			d.runner.errf("local: can only be used in a function\n")
			d.runner.exit.code = 1
			return false
		}
		d.local = true
	case "export":
		d.modes = append(d.modes, "-x")
	case "readonly":
		d.modes = append(d.modes, "-r")
	case "nameref":
		d.valType = "-n"
	case "typeset":
		d.allowPlusFlags = true
	}
	return true
}

func (d *declCommand) processOperands() bool {
	operands := d.runner.mergeDeclOperands(d.clause.Variant.Value, d.clause.Operands)
	for _, operand := range operands {
		if !d.processOperand(operand) {
			return false
		}
	}
	return true
}

func (d *declCommand) processOperand(operand syntax.DeclOperand) bool {
	switch operand := operand.(type) {
	case *syntax.DeclFlag:
		return d.processFlagOperand(operand)
	case *syntax.DeclName:
		return d.processNamedOperand(operand.Ref, nil, false, declOperandString(operand))
	case *syntax.DeclAssign:
		raw := declOperandString(operand)
		if operand.Assign.Array != nil && operand.Assign.Ref != nil {
			d.underscore = operand.Assign.Ref.Name.Value
		} else {
			d.underscore = raw
		}
		return d.processNamedOperand(operand.Assign.Ref, operand.Assign, true, raw)
	case *syntax.DeclDynamicWord:
		return d.processDynamicWordOperand(operand)
	default:
		panic(fmt.Sprintf("unexpected declaration operand: %T", operand))
	}
}

func (d *declCommand) processFlagOperand(operand *syntax.DeclFlag) bool {
	name := d.runner.literal(operand.Word)
	d.underscore = name
	if d.allowPlusFlags || !strings.HasPrefix(name, "+") {
		fp := flagParser{remaining: []string{name}}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-x", "-r", "-i", "-l", "-t", "-u":
				d.modes = append(d.modes, flag)
			case "+x", "+r", "+i", "+l", "+t", "+u":
				d.modes = append(d.modes, flag)
			case "-a", "-A":
				d.valType = flag
			case "-n":
				if d.declName == "export" {
					d.modes = append(d.modes, "+x")
				} else {
					d.valType = flag
				}
			case "+a", "+A", "+n":
				d.valType = flag
			case "-g":
				d.global = true
			case "-f", "-F", "-p":
				d.declQuery = flag
			default:
				d.runner.errf("%s: %s: invalid option\n", d.declName, flag)
				if usage := declUsage(d.declName); usage != "" {
					d.runner.errf("%s", usage)
				}
				d.runner.exit.code = 2
				return false
			}
		}
		if d.tracingEnabled {
			d.builtinTraceFields = append(d.builtinTraceFields, name)
		}
		return true
	}
	return d.processNamedOperand(&syntax.VarRef{Name: &syntax.Lit{Value: name}}, nil, false, name)
}

func (d *declCommand) processDynamicWordOperand(operand *syntax.DeclDynamicWord) bool {
	var fields []string
	d.runWithWriteEnv(d.declEvalEnv, func() bool {
		fields = d.runner.fields(operand.Word)
		if len(fields) == 0 {
			fields = []string{d.runner.literal(operand.Word)}
		}
		return true
	})
	for _, field := range fields {
		parsed, err := parseDeclOperandField(d.runner.parserLangVariant(), d.clause.Variant.Value, field)
		splitFields := []string{field}
		if strings.ContainsAny(field, "[]") && (err != nil || parsed == nil) {
			splitFields = splitDeclDynamicField(d.runner.parserLangVariant(), field)
		}
		for i, splitField := range splitFields {
			if i > 0 || len(splitFields) > 1 {
				parsed, err = parseDeclOperandField(d.runner.parserLangVariant(), d.clause.Variant.Value, splitField)
			}
			if err != nil {
				d.onlyFlagOperands = false
				if strings.ContainsAny(splitField, "[]") {
					parsed = nil
					err = nil
				} else {
					d.runner.errf("%s: %v\n", d.clause.Variant.Value, err)
					d.runner.exit.code = 1
					continue
				}
			}
			if parsed == nil {
				d.onlyFlagOperands = false
				d.runner.errf("%s: `%s': not a valid identifier\n", d.clause.Variant.Value, splitField)
				d.runner.exit.code = 1
				continue
			}
			subMode := subscriptModeFromArrayExprMode(declArrayModeFromValueType(d.valType))
			switch parsed := parsed.(type) {
			case *syntax.DeclName:
				stampVarRefSubscriptMode(parsed.Ref, subMode)
			case *syntax.DeclAssign:
				stampVarRefSubscriptMode(parsed.Assign.Ref, subMode)
				stampArrayExprSubscriptModes(parsed.Assign.Array, subMode)
			}
			if as, ok := parsed.(*syntax.DeclAssign); ok && as.Assign.Array != nil {
				if mode := declArrayModeFromValueType(d.valType); mode != syntax.ArrayExprInherit {
					as.Assign.Array.Mode = mode
				}
			}
			if as, ok := parsed.(*syntax.DeclAssign); ok && as.Assign.Array != nil &&
				as.Assign.Array.Mode == syntax.ArrayExprInherit {
				// Bash only keeps runtime-parsed compound assignments structural
				// when an explicit array attribute is active.
				parsed = declStringifiedArrayAssign(as.Assign)
			}
			if dyn, ok := parsed.(*syntax.DeclDynamicWord); ok {
				parsed = &syntax.DeclName{
					Ref: &syntax.VarRef{Name: &syntax.Lit{Value: d.runner.literal(dyn.Word)}},
				}
			}
			switch parsed := parsed.(type) {
			case *syntax.DeclName:
				if !d.processNamedOperand(parsed.Ref, nil, false, splitField) {
					return false
				}
			case *syntax.DeclAssign:
				if !d.processNamedOperand(parsed.Assign.Ref, parsed.Assign, true, splitField) {
					return false
				}
			default:
				if !d.processOperand(parsed) {
					return false
				}
			}
			if d.runner.exit.fatalExit || d.runner.exit.exiting {
				return false
			}
		}
	}
	return true
}

func (d *declCommand) processNamedOperand(ref *syntax.VarRef, as *syntax.Assign, isAssign bool, raw string) bool {
	r := d.runner
	name := ref.Name.Value

	if d.declQuery != "-f" && d.declQuery != "-F" && ref.Index != nil &&
		(d.declName == "export" || d.declName == "readonly") {
		r.errf("%s: `%s': not a valid identifier\n", d.declName, printVarRef(ref))
		r.exit.code = 1
		return false
	}
	if d.declQuery != "-f" && d.declQuery != "-F" && !syntax.ValidName(name) {
		if d.allowPlusFlags && strings.HasPrefix(raw, "+") && !strings.ContainsAny(name, "[]") {
			r.errf("%s: invalid name %q\n", d.declName, name)
		} else {
			r.errf("%s: `%s': not a valid identifier\n", d.declName, name)
		}
		r.exit.code = 1
		return false
	}

	d.onlyFlagOperands = false
	if isAssign {
		switch {
		case as != nil && as.Array != nil:
			d.underscore = name
		case raw != "":
			d.underscore = raw
		default:
			d.underscore = name
		}
	} else {
		d.underscore = name
	}
	if d.tracingEnabled && !isAssign {
		d.builtinTraceFields = append(d.builtinTraceFields, name)
	}
	if raw == "" {
		raw = name
	}

	if d.declQuery == "-f" && functionTraceOnlyModes(d.modes) {
		info, ok := r.funcInfo(name)
		if !ok || info.body == nil {
			r.exit.code = 1
			return true
		}
		for _, mode := range d.modes {
			switch mode {
			case "-t":
				info.trace = true
			case "+t":
				info.trace = false
			}
		}
		r.setFuncInfo(name, info)
		return true
	}
	if d.declQuery == "-f" {
		// declare -f name: print function definition.
		// Bash silently returns exit 1 for missing functions.
		if body := r.funcBody(name); body != nil {
			d.printFunction(name, body)
		} else {
			r.exit.code = 1
		}
		return true
	}
	if d.declQuery == "-F" {
		if body := r.funcBody(name); body != nil {
			if r.opts[optExtDebug] {
				source := r.funcSource(name)
				if source == "" {
					source = "main"
				}
				r.outf("%s %d %s\n", name, body.Pos().Line(), source)
			} else {
				r.outf("%s\n", name)
			}
		} else {
			r.exit.code = 1
		}
		return true
	}
	if d.declQuery == "-p" {
		if d.declName == "readonly" || d.declName == "export" {
			return true
		}
		var vr expand.Variable
		if d.declName == "local" {
			var ok bool
			vr, ok = currentScopeVar(localScopeEnv(r.writeEnv), name)
			if !ok || !vr.Local {
				vr = expand.Variable{}
			}
		} else {
			vr = r.lookupVar(name)
		}
		if !vr.Declared() {
			d.declErrf("%s: %s: not found\n", d.declName, raw)
			r.exit.code = 1
			return true
		}
		d.printDeclaredVar(name, vr)
		return true
	}
	if body := r.funcBody(name); body != nil && as == nil && d.valType == "" && functionTraceOnlyModes(d.modes) {
		info, _ := r.funcInfo(name)
		for _, mode := range d.modes {
			switch mode {
			case "-t":
				info.trace = true
			case "+t":
				info.trace = false
			}
		}
		r.setFuncInfo(name, info)
		return true
	}

	targetEnv, frameTempOwner, hasFrameTemp := d.targetEnv(name)
	return d.runWithWriteEnv(targetEnv, func() bool {
		vr := d.lookupVar(targetEnv, name)
		declaredBefore := vr.Declared()
		if msg := d.arrayConversionError(name, vr); msg != "" {
			if r.evalDepth > 0 {
				d.declErrf("%s\n", msg)
			} else {
				d.declErrf("%s: %s\n", d.declName, msg)
			}
			r.exit.code = 1
			return true
		}

		clearReadonly := false
		for _, mode := range d.modes {
			if mode == "+r" {
				clearReadonly = true
				break
			}
		}
		if clearReadonly && vr.ReadOnly {
			switch d.declName {
			case "local":
				d.declErrf("local: %s: readonly variable\n", name)
			case "declare", "typeset":
				d.declErrf("%s: %s: readonly variable\n", d.declName, name)
			default:
				d.declErrf("%s: readonly variable\n", name)
			}
			r.exit.code = 1
			return true
		}

		arrayAssignTrace := ""
		if !isAssign {
			switch d.valType {
			case "-A":
				vr.Kind = expand.Associative
			case "-a":
				vr.Kind = expand.Indexed
			case "-n":
				switch vr.Kind {
				case expand.Indexed, expand.Associative:
					vr.Kind = expand.NameRef
					vr.Str = vr.String()
					vr.List = nil
					vr.Indices = nil
					vr.Map = nil
				case expand.NameRef, expand.String:
					vr.Kind = expand.NameRef
				default:
					vr.Kind = expand.NameRef
					vr.Str = ""
				}
			case "+n":
				if vr.Kind == expand.NameRef {
					vr.Kind = expand.String
					vr.List = nil
					vr.Indices = nil
					vr.Map = nil
				} else {
					vr.Kind = expand.KeepValue
				}
			case "+a", "+A":
				// Remove array/assoc attribute, convert to string.
				if vr.Kind == expand.Indexed || vr.Kind == expand.Associative {
					vr.Kind = expand.String
					vr.Str = vr.String()
					vr.List = nil
					vr.Indices = nil
					vr.Map = nil
				}
			default:
				if !vr.Declared() {
					vr.Kind = expand.String
				} else {
					vr.Kind = expand.KeepValue
				}
			}
		} else {
			var ok bool
			d.runWithWriteEnv(d.declEvalEnv, func() bool {
				if d.valType == "+a" || d.valType == "+A" {
					// +a/+A with a value: treat as string assignment.
					vr, arrayAssignTrace, ok = r.assignVal(vr, as, "")
				} else {
					vr, arrayAssignTrace, ok = r.assignVal(vr, as, d.valType)
					if d.valType == "-a" && as.Value != nil && as.Array == nil && as.Ref != nil && as.Ref.Index == nil {
						vr.Kind = expand.Indexed
						vr.List = []string{vr.Str}
						vr.Str = ""
						vr.Map = nil
					}
				}
				return ok
			})
			if !ok || r.exit.fatalExit || r.exit.exiting {
				return false
			}
			// For integer append in declare context, redo as arithmetic addition.
			if as.Append && as.Value != nil && vr.Kind == expand.String {
				isInt := vr.Integer
				if !isInt {
					for _, mode := range d.modes {
						if mode == "-i" {
							isInt = true
						} else if mode == "+i" {
							isInt = false
						}
					}
				}
				if isInt {
					oldVal := r.evalIntegerAttr(r.lookupVar(name).String())
					newVal := 0
					d.runWithWriteEnv(d.declEvalEnv, func() bool {
						newVal = r.evalIntegerAttr(r.assignLiteral(as))
						return true
					})
					vr.Str = strconv.Itoa(oldVal + newVal)
				}
			}
		}

		updates := attrUpdate{}
		// Apply attribute modes before transforming the value,
		// so that "declare -i foo=2+3" evaluates arithmetic.
		for _, mode := range d.modes {
			switch mode {
			case "-i":
				updates.hasInteger = true
				updates.integer = true
				vr.Integer = true
			case "+i":
				updates.hasInteger = true
				updates.integer = false
				vr.Integer = false
			case "-l":
				updates.hasLower = true
				updates.lower = true
				updates.hasUpper = true
				updates.upper = false
				vr.Lower = true
				vr.Upper = false // -l and -u are mutually exclusive
			case "+l":
				updates.hasLower = true
				updates.lower = false
				vr.Lower = false
			case "-t":
				updates.hasTrace = true
				updates.trace = true
				vr.Trace = true
			case "+t":
				updates.hasTrace = true
				updates.trace = false
				vr.Trace = false
			case "-u":
				updates.hasUpper = true
				updates.upper = true
				updates.hasLower = true
				updates.lower = false
				vr.Upper = true
				vr.Lower = false // -l and -u are mutually exclusive
			case "+u":
				updates.hasUpper = true
				updates.upper = false
				vr.Upper = false
			}
		}
		if d.global {
			updates.hasLocal = true
			updates.local = false
			vr.Local = false
		} else if d.local {
			updates.hasLocal = true
			updates.local = true
			vr.Local = true
		}
		for _, mode := range d.modes {
			switch mode {
			case "-x":
				updates.hasExported = true
				updates.exported = true
				vr.Exported = true
			case "+x":
				updates.hasExported = true
				updates.exported = false
				vr.Exported = false
			case "-r":
				updates.hasReadOnly = true
				updates.readOnly = true
				vr.ReadOnly = true
			case "+r":
				updates.hasReadOnly = true
				updates.readOnly = false
				vr.ReadOnly = false
			}
		}
		if info, ok := r.funcInfo(name); ok && info.body != nil {
			switch {
			case vr.Trace:
				info.trace = true
				r.setFuncInfo(name, info)
			case updates.hasTrace && !updates.trace:
				info.trace = false
				r.setFuncInfo(name, info)
			}
		}

		r.applyVarAttrs(&vr)
		var nameRefErr error
		if vr.Kind == expand.NameRef {
			nameRefErr = validateNameRefTarget(d.runner.parserLangVariant(), vr.Str)
		}
		if !isAssign {
			r.setVar(name, vr)
			if d.local && hasFrameTemp && frameTempOwner != targetEnv && tempScopeConsumesLocals(frameTempOwner) {
				deleteCurrentScopeVar(frameTempOwner, name)
			}
			if d.declName == "readonly" && !declaredBefore && !vr.IsSet() &&
				(vr.Kind == expand.Indexed || vr.Kind == expand.Associative) {
				r.setHiddenReadonlyArrayDecl(name, vr.Kind)
			} else {
				r.clearHiddenReadonlyArrayDecl(name)
			}
			if nameRefErr != nil {
				r.errf("%s: %v\n", d.clause.Variant.Value, nameRefErr)
				r.exit.code = 1
			}
			return true
		}
		if vr.Kind == expand.NameRef && as.Ref != nil && as.Ref.Index == nil {
			r.setVar(name, vr)
			r.clearHiddenReadonlyArrayDecl(name)
			if nameRefErr != nil {
				r.errf("%s: %v\n", d.clause.Variant.Value, nameRefErr)
				r.exit.code = 1
			}
			if d.tracingEnabled {
				d.builtinTraceFields = append(d.builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
			}
			return true
		}
		if err := r.setVarByRef(r.lookupVar(as.Ref.Name.Value), as.Ref, vr, as.Append, updates); err != nil {
			if d.declName == "local" && strings.HasSuffix(err.Error(), ": readonly variable") {
				d.declErrf("local: %v\n", err)
			} else {
				r.errf("%v\n", err)
			}
			r.exit.code = 1
			return false
		}
		if d.local && hasFrameTemp && frameTempOwner != targetEnv && tempScopeConsumesLocals(frameTempOwner) {
			deleteCurrentScopeVar(frameTempOwner, name)
		}
		r.clearHiddenReadonlyArrayDecl(name)
		if d.tracingEnabled {
			switch {
			case d.clause.Variant.Value == "readonly":
				d.builtinTraceFields = append(d.builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
				if as.Array != nil {
					d.trailingTraceLines = append(d.trailingTraceLines, arrayAssignTrace)
				} else if as.Value != nil {
					d.trailingTraceLines = append(d.trailingTraceLines, r.traceAssignString(as.Ref, vr, as.Append))
				}
			case (d.clause.Variant.Value == "declare" || d.clause.Variant.Value == "typeset") && as.Array != nil:
				d.leadingTraceLines = append(d.leadingTraceLines, arrayAssignTrace)
				d.builtinTraceFields = append(d.builtinTraceFields, name)
			default:
				d.builtinTraceFields = append(d.builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
			}
		}
		return true
	})
}

func (d *declCommand) emitListingOrQueryOutput() {
	if !d.onlyFlagOperands {
		return
	}
	switch d.declQuery {
	case "-f":
		names := make([]string, 0, len(d.runner.funcs))
		for name := range d.runner.funcs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d.printFunction(name, d.runner.funcBody(name))
		}
	case "-F":
		names := make([]string, 0, len(d.runner.funcs))
		for name := range d.runner.funcs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d.runner.outf("declare -f %s\n", name)
		}
	case "-p":
		d.printListedVars(d.declName == "local", false)
	case "":
		switch d.declName {
		case "declare", "typeset":
			d.printListedVars(false, true)
		case "local":
			d.printListedVars(true, false)
		case "readonly", "export", "nameref":
			d.printListedVars(false, false)
		}
	}
}

func (d *declCommand) finalizeUnderscore() {
	d.runner.setSpecialUnderscore(d.underscore)
}

func (d *declCommand) emitXTraceOutput() {
	if !d.tracingEnabled {
		return
	}
	for _, line := range d.leadingTraceLines {
		d.trace.string(line)
		d.trace.newLineFlush()
	}
	d.trace.call(d.builtinTraceFields[0], d.builtinTraceFields[1:]...)
	d.trace.newLineFlush()
	for _, line := range d.trailingTraceLines {
		d.trace.string(line)
		d.trace.newLineFlush()
	}
}

func (d *declCommand) declErrf(format string, a ...any) {
	if d.runner.evalDepth > 0 {
		d.runner.errf("eval: ")
	}
	d.runner.errf(format, a...)
}

func (d *declCommand) printFunction(name string, body *syntax.Stmt) {
	d.runner.outf("%s()\n", name)
	printer := syntax.NewPrinter()
	var buf bytes.Buffer
	printer.Print(&buf, body)
	d.runner.outf("%s\n", buf.String())
}

func (d *declCommand) printDeclaredVar(name string, vr expand.Variable) {
	declVR := vr
	if d.runner.hideReadonlyArrayDeclKind(name, declVR) {
		declVR.Kind = expand.Unknown
	}
	flags := declVR.Flags()
	if flags == "" {
		flags = "--"
	} else {
		flags = "-" + flags
	}
	switch vr.Kind {
	case expand.Indexed:
		d.runner.outf("declare %s %s", flags, name)
		if !vr.IsSet() {
			d.runner.out("\n")
			return
		}
		d.runner.out("=(")
		for i, index := range vr.IndexedIndices() {
			if i > 0 {
				d.runner.out(" ")
			}
			val, _ := vr.IndexedGet(index)
			d.runner.outf("[%d]=%s", index, bashDeclPrintValue(val))
		}
		d.runner.out(")\n")
	case expand.Associative:
		d.runner.outf("declare %s %s", flags, name)
		if !vr.IsSet() {
			d.runner.out("\n")
			return
		}
		d.runner.out("=(")
		first := true
		for _, k := range expand.AssociativeKeys(vr.Map) {
			v := vr.Map[k]
			if !first {
				d.runner.out(" ")
			}
			d.runner.outf("[%s]=%s", bashDeclAssocKey(k), bashDeclPrintValue(v))
			first = false
		}
		if !first {
			d.runner.out(" ")
		}
		d.runner.out(")\n")
	default:
		d.runner.outf("declare %s %s", flags, name)
		if !vr.IsSet() {
			d.runner.out("\n")
			return
		}
		d.runner.outf("=%s\n", bashDeclPrintValue(vr.Str))
	}
}

func (d *declCommand) printPlainVar(name string, vr expand.Variable) {
	switch vr.Kind {
	case expand.Indexed, expand.Associative:
		d.printDeclaredVar(name, vr)
	default:
		if !vr.IsSet() {
			d.runner.outf("%s\n", name)
			return
		}
		d.runner.outf("%s=%s\n", name, bashDeclPlainValue(d.runner.parserLangVariant(), vr.Str))
	}
}

func (d *declCommand) listedVars(currentOnly bool) map[string]expand.Variable {
	seen := make(map[string]expand.Variable)
	if currentOnly {
		for name, vr := range currentScopeVars(localScopeEnv(d.runner.writeEnv)) {
			seen[name] = vr
		}
		return seen
	}
	for name, vr := range d.runner.writeEnv.Each() {
		seen[name] = vr
	}
	return seen
}

func (d *declCommand) matchesVarFilter(name string, vr expand.Variable) bool {
	if !vr.Declared() {
		return false
	}
	if name == "BASH_EXECUTION_STRING" && len(d.modes) > 0 {
		return false
	}
	switch d.valType {
	case "-a":
		if vr.Kind != expand.Indexed {
			return false
		}
	case "-A":
		if vr.Kind != expand.Associative {
			return false
		}
	case "-n":
		if vr.Kind != expand.NameRef {
			return false
		}
	}
	if d.declQuery == "" || (d.declQuery == "-p" && d.declName == "local") {
		switch d.declName {
		case "readonly":
			return vr.ReadOnly
		case "export":
			return vr.Exported
		case "local":
			return vr.Local
		case "nameref":
			return vr.Kind == expand.NameRef
		}
	}
	for _, mode := range d.modes {
		switch mode {
		case "-r":
			if !vr.ReadOnly {
				return false
			}
		case "-x":
			if !vr.Exported {
				return false
			}
		case "-i":
			if !vr.Integer {
				return false
			}
		case "-l":
			if !vr.Lower {
				return false
			}
		case "-t":
			if !vr.Trace {
				return false
			}
		case "-u":
			if !vr.Upper {
				return false
			}
		}
	}
	return true
}

func (d *declCommand) printListedVars(currentOnly, plain bool) {
	seen := d.listedVars(currentOnly)
	names := make([]string, 0, len(seen))
	for name, vr := range seen {
		if !d.matchesVarFilter(name, vr) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		vr := seen[name]
		if plain {
			d.printPlainVar(name, vr)
		} else {
			d.printDeclaredVar(name, vr)
		}
	}
}

func (d *declCommand) arrayConversionError(name string, vr expand.Variable) string {
	switch {
	case d.valType == "-A" && vr.Kind == expand.Indexed:
		return fmt.Sprintf("%s: cannot convert indexed to associative array", name)
	case d.valType == "-a" && vr.Kind == expand.Associative:
		return fmt.Sprintf("%s: cannot convert associative to indexed array", name)
	default:
		return ""
	}
}

func (d *declCommand) runWithWriteEnv(env expand.WriteEnviron, fn func() bool) bool {
	if env == d.runner.writeEnv {
		return fn()
	}
	origEnv := d.runner.writeEnv
	d.runner.writeEnv = env
	defer func() {
		d.runner.writeEnv = origEnv
	}()
	return fn()
}

func (d *declCommand) lookupVar(env expand.WriteEnviron, name string) expand.Variable {
	if !d.local || d.global {
		return env.Get(name)
	}
	if owner, vr, ok := currentScopeBinding(localScopeEnv(env), name); ok {
		if !vr.Local && !envHasTempScope(owner) {
			return expand.Variable{}
		}
		return vr
	}
	return expand.Variable{}
}

func (d *declCommand) targetEnv(name string) (targetEnv, frameTempOwner expand.WriteEnviron, hasFrameTemp bool) {
	r := d.runner
	frameTempOwner, _, hasFrameTemp = currentFrameTempBinding(r.writeEnv, name)
	targetEnv = r.writeEnv
	if d.local {
		targetEnv = localScopeEnv(r.writeEnv)
		if hasFrameTemp && frameTempOwner == r.writeEnv && tempScopeConsumesLocals(frameTempOwner) {
			targetEnv = frameTempOwner
		}
	}
	if d.global && r.inFunc {
		targetEnv = globalWriteEnv(r.writeEnv)
	} else if !d.local {
		if ownerEnv, _, ok := visibleBindingWriteEnv(r.writeEnv, name); ok {
			targetEnv = ownerEnv
		}
	}
	return targetEnv, frameTempOwner, hasFrameTemp
}

func (r *Runner) mergeDeclOperands(variant string, operands []syntax.DeclOperand) []syntax.DeclOperand {
	if len(operands) < 2 || r.currentChunkSource == "" {
		return operands
	}
	rebuilt := make([]syntax.DeclOperand, 0, len(operands))
	for i := 0; i < len(operands); i++ {
		merged := operands[i]
		mergedSrc := r.sourceForNode(merged)
		if mergedSrc == "" {
			rebuilt = append(rebuilt, merged)
			continue
		}
		for j := i + 1; j < len(operands); j++ {
			if gap := r.sourceForOffsets(operands[j-1].End().Offset(), operands[j].Pos().Offset()); gap != "" {
				break
			}
			nextSrc := r.sourceForNode(operands[j])
			if nextSrc == "" {
				break
			}
			candidateSrc := mergedSrc + nextSrc
			reparsed, err := r.parserForVariant().DeclOperand(strings.NewReader(candidateSrc))
			if err != nil || reparsed == nil {
				reparsed, err = parseDeclOperandField(r.parserLangVariant(), variant, candidateSrc)
				if err != nil || reparsed == nil {
					break
				}
			}
			merged = reparsed
			mergedSrc = candidateSrc
			i = j
		}
		rebuilt = append(rebuilt, merged)
	}
	return rebuilt
}

func declUsage(name string) string {
	switch name {
	case "declare":
		return "declare: usage: declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]\n"
	case "typeset":
		return "typeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n"
	default:
		return ""
	}
}

func functionTraceOnlyModes(modes []string) bool {
	if len(modes) == 0 {
		return false
	}
	for _, mode := range modes {
		if mode != "-t" && mode != "+t" {
			return false
		}
	}
	return true
}

func bashDeclDoubleQuote(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch c := value[i]; c {
		case '"', '\\', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

func bashDeclPrintValue(value string) string {
	if needsTraceANSIQuote(value) {
		return traceANSIQuote(value, true)
	}
	return bashDeclDoubleQuote(value)
}

func bashDeclPlainValue(lang syntax.LangVariant, value string) string {
	if needsTraceANSIQuote(value) {
		return traceANSIQuote(value, true)
	}
	if lang == 0 || lang == syntax.LangAuto {
		lang = syntax.LangBash
	}
	quoted, err := syntax.Quote(value, lang)
	if err != nil {
		return bashDeclDoubleQuote(value)
	}
	return quoted
}

func bashDeclAssocKey(key string) string {
	if key != "" && !needsTraceANSIQuote(key) && !strings.ContainsAny(key, "\\]\"'\n\r") {
		return key
	}
	return bashDeclPrintValue(key)
}

func declOperandString(operand syntax.DeclOperand) string {
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, operand); err != nil {
		return ""
	}
	return buf.String()
}

func declClauseFromFields(name string, fields []string, lang syntax.LangVariant) *syntax.DeclClause {
	decl := &syntax.DeclClause{
		Variant: &syntax.Lit{Value: name},
	}
	for _, field := range fields {
		if operand, err := parseDeclOperandField(lang, name, field); err == nil {
			switch operand.(type) {
			case *syntax.DeclFlag, *syntax.DeclName, *syntax.DeclAssign:
				decl.Operands = append(decl.Operands, operand)
				continue
			}
		}
		decl.Operands = append(decl.Operands, &syntax.DeclDynamicWord{
			Word: &syntax.Word{
				Parts: []syntax.WordPart{
					&syntax.DblQuoted{Parts: []syntax.WordPart{&syntax.Lit{Value: field}}},
				},
			},
		})
	}
	return decl
}

func parseDeclOperandField(lang syntax.LangVariant, variant, field string) (syntax.DeclOperand, error) {
	if variant == "export" || variant == "local" {
		if eqIndex := strings.IndexByte(field, '='); eqIndex > 0 {
			name := field[:eqIndex]
			if strings.HasSuffix(name, "+") {
				name = name[:len(name)-1]
			}
			if !syntax.ValidName(name) && !strings.ContainsAny(name, "[]") && !strings.Contains(name, "{") {
				return &syntax.DeclDynamicWord{
					Word: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: field}}},
				}, nil
			}
		}
	}
	if lang == 0 || lang == syntax.LangAuto {
		lang = syntax.LangBash
	}
	p := syntax.NewParser(syntax.Variant(lang))
	return p.DeclOperandField(strings.NewReader(field))
}

func splitDeclDynamicField(lang syntax.LangVariant, field string) []string {
	if lang == 0 || lang == syntax.LangAuto {
		lang = syntax.LangBash
	}
	p := syntax.NewParser(syntax.Variant(lang))
	var words []string
	err := p.Words(strings.NewReader(field), func(word *syntax.Word) bool {
		var buf bytes.Buffer
		if err := syntax.NewPrinter().Print(&buf, word); err != nil {
			panic(err)
		}
		words = append(words, buf.String())
		return true
	})
	if err != nil || len(words) == 0 {
		return []string{field}
	}
	return words
}

func declStringifiedArrayAssign(as *syntax.Assign) *syntax.DeclAssign {
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, as); err != nil {
		panic(err)
	}
	rendered := buf.String()
	i := strings.IndexByte(rendered, '=')
	if i < 0 {
		panic(fmt.Sprintf("assignment printed without '=': %q", rendered))
	}
	return &syntax.DeclAssign{Assign: &syntax.Assign{
		Append: as.Append,
		Ref:    as.Ref,
		Value: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{
			Value: rendered[i+1:],
		}}},
	}}
}
