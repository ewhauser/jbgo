// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import (
	"slices"
	"strconv"
	"strings"
)

type braceSepKind uint8

const (
	braceSepComma braceSepKind = iota
	braceSepDots
)

type braceSep struct {
	kind braceSepKind
	lit  *Lit
}

type braceCandidate struct {
	Lbrace *Lit
	Rbrace *Lit
	Seps   []braceSep
	Elems  []*Word
}

func splitBraceWordParts(parts []WordPart) []WordPart {
	if !slices.ContainsFunc(parts, func(part WordPart) bool {
		lit, ok := part.(*Lit)
		return ok && strings.Contains(lit.Value, "{")
	}) {
		return parts
	}

	top := &Word{}
	acc := top
	var cur *braceCandidate
	open := []*braceCandidate{}

	pop := func() *braceCandidate {
		old := cur
		open = open[:len(open)-1]
		if len(open) == 0 {
			cur = nil
			acc = top
		} else {
			cur = open[len(open)-1]
			acc = cur.Elems[len(cur.Elems)-1]
		}
		return old
	}
	addLit := func(lit *Lit) {
		if lit != nil {
			if n := len(acc.Parts); n > 0 {
				if prev, ok := acc.Parts[n-1].(*Lit); ok && prev != nil &&
					prev.ValueEnd.IsValid() && lit.ValuePos.IsValid() && prev.ValueEnd == lit.ValuePos {
					prev.Value += lit.Value
					prev.ValueEnd = lit.ValueEnd
					return
				}
			}
			acc.Parts = append(acc.Parts, lit)
		}
	}
	addPart := func(part WordPart) {
		switch part := part.(type) {
		case nil:
		case *Lit:
			addLit(part)
		default:
			acc.Parts = append(acc.Parts, part)
		}
	}
	addSplitLit := func(lit *Lit, start, end int) {
		addLit(splitLit(lit, start, end))
	}
	addCandidateLiteral := func(br *braceCandidate) {
		addLit(br.Lbrace)
		for i, elem := range br.Elems {
			if i > 0 {
				addLit(br.Seps[i-1].lit)
			}
			for _, part := range elem.Parts {
				addPart(part)
			}
		}
		addLit(br.Rbrace)
	}
	closeBrace := func(br *braceCandidate) {
		if exp, ok := braceCandidateNode(br); ok {
			addPart(exp)
			return
		}
		addCandidateLiteral(br)
	}

	for _, wp := range parts {
		lit, ok := wp.(*Lit)
		if !ok {
			addPart(wp)
			continue
		}
		last := 0
		for j := 0; j < len(lit.Value); j++ {
			switch lit.Value[j] {
			case '{':
				addSplitLit(lit, last, j)
				acc = &Word{}
				cur = &braceCandidate{
					Lbrace: splitLit(lit, j, j+1),
					Elems:  []*Word{acc},
				}
				open = append(open, cur)
				last = j + 1
			case ',':
				if cur == nil {
					continue
				}
				addSplitLit(lit, last, j)
				cur.Seps = append(cur.Seps, braceSep{
					kind: braceSepComma,
					lit:  splitLit(lit, j, j+1),
				})
				acc = &Word{}
				cur.Elems = append(cur.Elems, acc)
				last = j + 1
			case '.':
				if cur == nil || j+1 >= len(lit.Value) || lit.Value[j+1] != '.' {
					continue
				}
				addSplitLit(lit, last, j)
				cur.Seps = append(cur.Seps, braceSep{
					kind: braceSepDots,
					lit:  splitLit(lit, j, j+2),
				})
				acc = &Word{}
				cur.Elems = append(cur.Elems, acc)
				j++
				last = j + 1
			case '}':
				if cur == nil {
					continue
				}
				addSplitLit(lit, last, j)
				cur.Rbrace = splitLit(lit, j, j+1)
				closeBrace(pop())
				last = j + 1
			}
		}
		if last == 0 {
			addPart(lit)
			continue
		}
		addSplitLit(lit, last, len(lit.Value))
	}

	for acc != top {
		addCandidateLiteral(pop())
	}
	return top.Parts
}

func splitLit(lit *Lit, start, end int) *Lit {
	if lit == nil || start >= end {
		return nil
	}
	l2 := *lit
	l2.Value = l2.Value[start:end]
	l2.ValuePos = posAddCol(lit.ValuePos, start)
	l2.ValueEnd = posAddCol(lit.ValuePos, end)
	return &l2
}

func braceCandidateNode(br *braceCandidate) (*BraceExp, bool) {
	if br == nil || br.Rbrace == nil || len(br.Elems) <= 1 {
		return nil, false
	}
	exp := &BraceExp{
		Lbrace: br.Lbrace.ValuePos,
		Rbrace: br.Rbrace.ValuePos,
	}
	if slices.ContainsFunc(br.Seps, func(sep braceSep) bool {
		return sep.kind == braceSepComma
	}) {
		exp.Elems = braceCommaElems(br)
		return exp, true
	}
	exp.Elems = br.Elems
	exp.Sequence = true
	if !validBraceSequence(exp.Elems) {
		return nil, false
	}
	return exp, true
}

func braceCommaElems(br *braceCandidate) []*Word {
	elems := []*Word{{Parts: append([]WordPart(nil), br.Elems[0].Parts...)}}
	for i, sep := range br.Seps {
		if sep.kind == braceSepComma {
			elems = append(elems, &Word{Parts: append([]WordPart(nil), br.Elems[i+1].Parts...)})
			continue
		}
		cur := elems[len(elems)-1]
		cur.Parts = append(cur.Parts, sep.lit)
		cur.Parts = append(cur.Parts, br.Elems[i+1].Parts...)
	}
	return elems
}

func validBraceSequence(elems []*Word) bool {
	if len(elems) != 2 && len(elems) != 3 {
		return false
	}
	var chars [2]bool
	for i, elem := range elems[:2] {
		val := elem.Lit()
		if _, err := strconv.Atoi(val); err == nil {
			continue
		}
		if len(val) == 1 && asciiLetter(val[0]) {
			chars[i] = true
			continue
		}
		return false
	}
	if chars[0] != chars[1] {
		return false
	}
	if len(elems) == 3 {
		if _, err := strconv.Atoi(elems[2].Lit()); err != nil {
			return false
		}
	}
	return true
}
