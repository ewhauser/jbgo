package syntax

import (
	"fmt"
	"strings"
)

// ParseErrorKind describes the structural class of a parse diagnostic.
type ParseErrorKind string

const (
	ParseErrorKindMissing    ParseErrorKind = "missing"
	ParseErrorKindUnexpected ParseErrorKind = "unexpected"
	ParseErrorKindUnclosed   ParseErrorKind = "unclosed"
	ParseErrorKindUnmatched  ParseErrorKind = "unmatched"
)

// ParseErrorSymbol is the typed shell symbol carried by [ParseError].
//
// Concrete syntax uses canonical shell spellings such as "then", "do", "(",
// or "}". Parser sentinel values use stable string markers such as "EOF",
// "newline", "word", and "statement-list".
type ParseErrorSymbol string

const (
	ParseErrorSymbolEOF           ParseErrorSymbol = "EOF"
	ParseErrorSymbolNewline       ParseErrorSymbol = "newline"
	ParseErrorSymbolWord          ParseErrorSymbol = "word"
	ParseErrorSymbolPattern       ParseErrorSymbol = "pattern"
	ParseErrorSymbolExpression    ParseErrorSymbol = "expression"
	ParseErrorSymbolStatement     ParseErrorSymbol = "statement"
	ParseErrorSymbolStatementList ParseErrorSymbol = "statement-list"
	ParseErrorSymbolHereDocument  ParseErrorSymbol = "here-document"

	ParseErrorSymbolSingleQuote  ParseErrorSymbol = "'"
	ParseErrorSymbolDoubleQuote  ParseErrorSymbol = "\""
	ParseErrorSymbolBackquote    ParseErrorSymbol = "`"
	ParseErrorSymbolLeftBrace    ParseErrorSymbol = "{"
	ParseErrorSymbolRightBrace   ParseErrorSymbol = "}"
	ParseErrorSymbolLeftParen    ParseErrorSymbol = "("
	ParseErrorSymbolRightParen   ParseErrorSymbol = ")"
	ParseErrorSymbolLeftBracket  ParseErrorSymbol = "["
	ParseErrorSymbolRightBracket ParseErrorSymbol = "]"
	ParseErrorSymbolSemicolon    ParseErrorSymbol = ";"

	ParseErrorSymbolThen ParseErrorSymbol = "then"
	ParseErrorSymbolDo   ParseErrorSymbol = "do"
	ParseErrorSymbolFi   ParseErrorSymbol = "fi"
	ParseErrorSymbolDone ParseErrorSymbol = "done"
	ParseErrorSymbolEsac ParseErrorSymbol = "esac"
	ParseErrorSymbolIn   ParseErrorSymbol = "in"
)

type parseErrorMetadata struct {
	kind          ParseErrorKind
	construct     ParseErrorSymbol
	unexpected    ParseErrorSymbol
	expected      []ParseErrorSymbol
	isRecoverable bool
}

func (m parseErrorMetadata) apply(pe *ParseError) {
	pe.Kind = m.kind
	pe.Construct = m.construct
	pe.Unexpected = m.unexpected
	pe.IsRecoverable = m.isRecoverable
	if len(m.expected) == 0 {
		pe.Expected = nil
		return
	}
	pe.Expected = append([]ParseErrorSymbol(nil), m.expected...)
}

func parseErrorSymbolFromToken(tok token) ParseErrorSymbol {
	switch tok {
	case _EOF:
		return ParseErrorSymbolEOF
	case _Newl:
		return ParseErrorSymbolNewline
	default:
		return parseErrorSymbolFromText(tok.String())
	}
}

func parseErrorSymbolFromReserved(word reservedWord) ParseErrorSymbol {
	if word == "" {
		return ""
	}
	return parseErrorSymbolFromText(string(word))
}

func parseErrorSymbolFromText(text string) ParseErrorSymbol {
	text = strings.TrimSpace(text)
	switch text {
	case "":
		return ""
	case "a word", "word":
		return ParseErrorSymbolWord
	case "a pattern", "pattern":
		return ParseErrorSymbolPattern
	case "an expression", "expression":
		return ParseErrorSymbolExpression
	case "a statement", "statement":
		return ParseErrorSymbolStatement
	case "a statement list", "statement list":
		return ParseErrorSymbolStatementList
	case "newline":
		return ParseErrorSymbolNewline
	case "EOF":
		return ParseErrorSymbolEOF
	case "here-document":
		return ParseErrorSymbolHereDocument
	default:
		return ParseErrorSymbol(text)
	}
}

func parseErrorSymbolFromAny(value any) ParseErrorSymbol {
	switch x := value.(type) {
	case ParseErrorSymbol:
		return x
	case reservedWord:
		return parseErrorSymbolFromReserved(x)
	case token:
		return parseErrorSymbolFromToken(x)
	case noQuote:
		return parseErrorSymbolFromText(string(x))
	case string:
		return parseErrorSymbolFromText(x)
	case fmt.Stringer:
		return parseErrorSymbolFromText(x.String())
	default:
		return parseErrorSymbolFromText(fmt.Sprint(value))
	}
}

func parseErrorSymbols(values ...any) []ParseErrorSymbol {
	symbols := make([]ParseErrorSymbol, 0, len(values))
	for _, value := range values {
		if symbol := parseErrorSymbolFromAny(value); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}

func (p *Parser) currentUnexpectedTokenSymbol() ParseErrorSymbol {
	switch p.tok {
	case _Lit, _LitWord, _LitRedir:
		return parseErrorSymbolFromText(p.val)
	default:
		return parseErrorSymbolFromToken(p.tok)
	}
}

func (p *Parser) currentUnexpectedTokenDiagnostic() (ParseErrorSymbol, string) {
	switch p.tok {
	case _Lit, _LitWord:
		return p.currentUnexpectedTokenSymbol(), bashQuoteString(p.val)
	default:
		return p.currentUnexpectedTokenSymbol(), p.tok.bashQuote()
	}
}

func missingCloseKind(unexpected ParseErrorSymbol) ParseErrorKind {
	switch unexpected {
	case ParseErrorSymbolEOF:
		return ParseErrorKindUnclosed
	default:
		return ParseErrorKindUnmatched
	}
}
