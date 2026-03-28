package interp

import (
	"bytes"
	"strings"

	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

func printSyntaxNode(node syntax.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, node); err != nil {
		panic(err)
	}
	return buf.String()
}

func quoteTraceValue(value string) string {
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err != nil {
		panic(err)
	}
	return quoted
}

func quoteTraceArrayValue(value string) string {
	quoted := quoteTraceValue(value)
	if quoted == value {
		return "'" + value + "'"
	}
	return quoted
}

func traceAssignFieldRaw(ref *syntax.VarRef, vr expand.Variable, appendValue bool) string {
	op := "="
	if appendValue {
		op = "+="
	}
	return printVarRef(ref) + op + vr.String()
}

func (r *Runner) traceAssignString(ref *syntax.VarRef, vr expand.Variable, appendValue bool) string {
	op := "="
	if appendValue {
		op = "+="
	}
	return printVarRef(ref) + op + quoteTraceValue(vr.String())
}

func traceExpandedArrayAssign(ref *syntax.VarRef, appendAssign bool, elems []expandedArrayElem) string {
	var b strings.Builder
	b.WriteString(printVarRef(ref))
	if appendAssign {
		b.WriteByte('+')
	}
	b.WriteString("=(")
	first := true
	for _, elem := range elems {
		switch elem.kind {
		case syntax.ArrayElemSequential:
			for _, field := range elem.fields {
				if !first {
					b.WriteByte(' ')
				}
				b.WriteString(quoteTraceArrayValue(field))
				first = false
			}
		case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
			if !first {
				b.WriteByte(' ')
			}
			if elem.index != nil {
				b.WriteString(printSyntaxNode(elem.index))
				if elem.kind == syntax.ArrayElemKeyedAppend {
					b.WriteString("+=")
				} else {
					b.WriteByte('=')
				}
			}
			b.WriteString(quoteTraceArrayValue(elem.value))
			first = false
		}
	}
	b.WriteByte(')')
	return b.String()
}
