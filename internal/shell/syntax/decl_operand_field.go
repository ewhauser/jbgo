// Copyright (c) 2016, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import (
	"io"
	"strings"
)

// DeclOperandField parses a runtime-expanded Bash-style declaration operand.
//
// Unlike [Parser.DeclOperand], scalar assignment values are kept as literal
// text after the assignment operator, and array element values are reparsed
// structurally without re-enabling expansion syntax inside those values.
func (p *Parser) DeclOperandField(r io.Reader) (DeclOperand, error) {
	var srcBuilder strings.Builder
	if _, err := io.Copy(&srcBuilder, r); err != nil {
		return nil, err
	}
	src := srcBuilder.String()

	p.reset()
	p.f = &File{}
	p.src = strings.NewReader(src)
	p.rune()
	p.next()
	op := p.declOperand()
	if p.err != nil {
		return op, p.err
	}
	if looksLikeDeclFlagSource(src) {
		switch typed := op.(type) {
		case nil:
			op = &DeclFlag{Word: &Word{Parts: []WordPart{&Lit{Value: src}}}}
		case *DeclDynamicWord:
			op = &DeclFlag{Word: literalizeDeclFieldWord(src, typed.Word)}
		}
	}

	op = literalizeDeclOperandField(src, op)
	if p.err == nil && p.tok != _EOF && !declOperandFieldAcceptsTrailing(op) {
		p.curErr("unexpected token in declaration operand: %#q", p.tok)
	}
	return op, p.err
}

func looksLikeDeclFlagSource(src string) bool {
	src = strings.TrimSpace(src)
	return src != "" &&
		(src[0] == '-' || src[0] == '+') &&
		!strings.ContainsAny(src, " \t\r\n")
}

func declOperandFieldAcceptsTrailing(op DeclOperand) bool {
	as, ok := op.(*DeclAssign)
	return ok && as != nil && as.Assign != nil && as.Assign.Array == nil
}

func literalizeDeclOperandField(src string, op DeclOperand) DeclOperand {
	switch op := op.(type) {
	case *DeclFlag:
		op.Word = literalizeDeclFieldWord(src, op.Word)
	case *DeclAssign:
		op.Assign = literalizeDeclFieldAssign(src, op.Assign)
	case *DeclDynamicWord:
		op.Word = literalizeDeclFieldWord(src, op.Word)
	}
	return op
}

func literalizeDeclFieldAssign(src string, as *Assign) *Assign {
	if as == nil {
		return nil
	}
	if as.Array != nil {
		return as
	}

	value, ok := declAssignFieldTail(src, as)
	if !ok {
		return as
	}
	if value == "" {
		as.Value = nil
		return as
	}
	as.Value = &Word{Parts: []WordPart{&Lit{Value: value}}}
	as.literalValue = true
	return as
}

func declAssignFieldTail(src string, as *Assign) (string, bool) {
	if as == nil || as.Ref == nil {
		return "", false
	}
	offset := int(as.Ref.End().Offset())
	if offset >= len(src) {
		return "", false
	}
	if src[offset] == '+' {
		offset++
	}
	if offset >= len(src) || src[offset] != '=' {
		return "", false
	}
	return src[offset+1:], true
}

func literalizeDeclFieldWord(src string, word *Word) *Word {
	if word == nil {
		return nil
	}
	frozen := &Word{Parts: make([]WordPart, 0, len(word.Parts))}
	for _, part := range word.Parts {
		frozen.Parts = append(frozen.Parts, literalizeDeclFieldWordPart(src, part))
	}
	return frozen
}

func literalizeDeclFieldWordPart(src string, part WordPart) WordPart {
	switch part := part.(type) {
	case *Lit:
		dup := *part
		return &dup
	case *SglQuoted:
		dup := *part
		return &dup
	case *DblQuoted:
		dup := *part
		dup.Parts = make([]WordPart, 0, len(part.Parts))
		for _, inner := range part.Parts {
			dup.Parts = append(dup.Parts, literalizeDeclFieldWordPart(src, inner))
		}
		return &dup
	default:
		return &Lit{Value: declFieldSource(src, part)}
	}
}

func declFieldSource(src string, node Node) string {
	start := int(node.Pos().Offset())
	end := int(node.End().Offset())
	if 0 <= start && start <= end && end <= len(src) {
		return src[start:end]
	}

	var sb strings.Builder
	if err := NewPrinter().Print(&sb, node); err != nil {
		return ""
	}
	return sb.String()
}
