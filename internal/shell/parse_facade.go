package shell

import (
	"bytes"
	"errors"
	"io"
	"iter"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// InteractiveParser exposes shell-owned interactive parsing without leaking
// syntax parser/printer setup into non-shell packages.
type InteractiveParser struct {
	name    string
	parser  *syntax.Parser
	printer *syntax.Printer
}

func (p *InteractiveParser) ensureInitialized() {
	if p == nil {
		return
	}
	if p.parser == nil {
		p.parser = syntax.NewParser()
	}
	if p.printer == nil {
		p.printer = syntax.NewPrinter()
	}
}

// NewInteractiveParser builds a shell-owned interactive parser/printer pair.
func NewInteractiveParser(name string) *InteractiveParser {
	return &InteractiveParser{
		name:    name,
		parser:  syntax.NewParser(),
		printer: syntax.NewPrinter(),
	}
}

// Incomplete reports whether the most recent interactive parse needs a continuation line.
func (p *InteractiveParser) Incomplete() bool {
	return p != nil && p.parser != nil && p.parser.Incomplete()
}

// Seq parses interactive input and yields rendered script chunks ready for execution.
func (p *InteractiveParser) Seq(r io.Reader) iter.Seq2[string, error] {
	parser := p
	if parser == nil {
		parser = &InteractiveParser{}
	}
	parser.ensureInitialized()
	return func(yield func(string, error) bool) {
		parser.parser.InteractiveSeq(r)(func(stmts []*syntax.Stmt, err error) bool {
			if err != nil || parser.parser.Incomplete() || len(stmts) == 0 {
				return yield("", err)
			}
			script, renderErr := parser.render(stmts)
			if renderErr != nil {
				return yield("", renderErr)
			}
			return yield(script, err)
		})
	}
}

func (p *InteractiveParser) render(stmts []*syntax.Stmt) (string, error) {
	if len(stmts) == 0 {
		return "", nil
	}
	printer := p.printer
	if printer == nil {
		printer = syntax.NewPrinter()
	}
	var buf bytes.Buffer
	file := &syntax.File{
		Name:  p.name,
		Stmts: stmts,
	}
	if err := printer.Print(&buf, file); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// IsUserSyntaxError reports whether err is a user-facing shell syntax error.
func IsUserSyntaxError(err error) bool {
	if err == nil {
		return false
	}

	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		return true
	}

	var quoteErr syntax.QuoteError
	if errors.As(err, &quoteErr) {
		return true
	}

	var langErr syntax.LangError
	return errors.As(err, &langErr)
}
