// Copyright (c) 2016, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import "bytes"

// ArithmExprString returns the compact shell source form of an arithmetic
// expression.
func ArithmExprString(expr ArithmExpr) string {
	if expr == nil {
		return ""
	}

	var buf bytes.Buffer
	printer := NewPrinter(Minify(true))
	if err := printer.Print(&buf, expr); err != nil {
		panic(err)
	}
	return buf.String()
}

func wordPartString(part WordPart) string {
	if part == nil {
		return ""
	}

	var buf bytes.Buffer
	printer := NewPrinter()
	if err := printer.Print(&buf, part); err != nil {
		panic(err)
	}
	return buf.String()
}
