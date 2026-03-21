// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// Braces performs brace expansion on a word, given that it contains any
// [syntax.BraceExp] parts. For example, the word with a brace expansion
// "foo{bar,baz}" will return two literal words, "foobar" and "foobaz".
//
// Note that the resulting words may share word parts. Invalid sequence forms
// may surface expansion errors.
func Braces(word *syntax.Word) ([]*syntax.Word, error) {
	var all []*syntax.Word
	var left []syntax.WordPart
	for i, wp := range word.Parts {
		br, ok := wp.(*syntax.BraceExp)
		if !ok {
			left = append(left, wp)
			continue
		}
		if br.Sequence {
			chars := false

			fromLit := br.Elems[0].Lit()
			toLit := br.Elems[1].Lit()

			from, err1 := strconv.Atoi(fromLit)
			to, err2 := strconv.Atoi(toLit)
			fixedWidth := false
			targetWidth := 0
			if err1 != nil || err2 != nil {
				chars = true
				from = int(br.Elems[0].Lit()[0])
				to = int(br.Elems[1].Lit()[0])
				if mixedCaseBraceRange(br.Elems[0].Lit()[0], br.Elems[1].Lit()[0]) {
					suffix := wordPartsSource(word.Parts[i+1:])
					if suffix != "" {
						return nil, mixedCaseBraceRangeError(suffix)
					}
				}
			} else if hasLeadingZero(fromLit) || hasLeadingZero(toLit) {
				fixedWidth = true
				targetWidth = max(len(fromLit), len(toLit))
			}
			upward := from <= to
			incr := 1
			if !upward {
				incr = -1
			}
			if len(br.Elems) > 2 {
				n, ok := braceSequenceStep(br.Elems[2].Lit())
				if !ok {
					return literalBrace(word, left, i, br)
				}
				if n != 0 {
					incr = absInt(n)
					if !upward {
						incr = -incr
					}
				}
			}
			n := from
			for {
				if upward && n > to {
					break
				}
				if !upward && n < to {
					break
				}
				next := *word
				next.Parts = next.Parts[i+1:]
				lit := &syntax.Lit{}
				if chars {
					lit.Value = string(rune(n))
				} else {
					lit.Value = formatBraceSequenceNumber(n, fixedWidth, targetWidth)
				}
				parts := make([]syntax.WordPart, 0, 1+len(next.Parts))
				parts = append(parts, lit)
				parts = append(parts, next.Parts...)
				next.Parts = parts
				exp, err := Braces(&next)
				if err != nil {
					return nil, err
				}
				for _, w := range exp {
					w.Parts = prependWordParts(left, w.Parts)
				}
				all = append(all, exp...)
				n += incr
			}
			return all, nil
		}
		for _, elem := range br.Elems {
			next := *word
			next.Parts = next.Parts[i+1:]
			parts := make([]syntax.WordPart, 0, len(elem.Parts)+len(next.Parts))
			parts = append(parts, elem.Parts...)
			parts = append(parts, next.Parts...)
			next.Parts = parts
			exp, err := Braces(&next)
			if err != nil {
				return nil, err
			}
			for _, w := range exp {
				w.Parts = prependWordParts(left, w.Parts)
			}
			all = append(all, exp...)
		}
		return all, nil
	}
	return []*syntax.Word{{Parts: left}}, nil
}

func formatBraceSequenceNumber(n int, fixedWidth bool, targetWidth int) string {
	if !fixedWidth {
		return strconv.Itoa(n)
	}
	return fmt.Sprintf("%0*d", targetWidth, n)
}

func prependWordParts(prefix, parts []syntax.WordPart) []syntax.WordPart {
	combined := make([]syntax.WordPart, 0, len(prefix)+len(parts))
	combined = append(combined, prefix...)
	combined = append(combined, parts...)
	return combined
}

func reparseBraceWord(word *syntax.Word) (*syntax.Word, error) {
	if len(word.Parts) == 0 {
		return word, nil
	}
	src, err := braceWordSource(word)
	if err != nil {
		return nil, err
	}
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader("x "+src+"\n"), "")
	if err != nil {
		return nil, err
	}
	if len(file.Stmts) != 1 {
		return nil, fmt.Errorf("brace reparse: unexpected statement count %d", len(file.Stmts))
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 {
		return nil, fmt.Errorf("brace reparse: unexpected parse shape")
	}
	return call.Args[1], nil
}

func braceWordSource(word *syntax.Word) (string, error) {
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	for _, part := range word.Parts {
		if err := printer.Print(&buf, part); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

func hasLeadingZero(s string) bool {
	if len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	return len(s) > 1 && s[0] == '0'
}

func mixedCaseBraceRange(from, to byte) bool {
	return asciiLower(from) && asciiUpper(to) || asciiUpper(from) && asciiLower(to)
}

func wordPartsSource(parts []syntax.WordPart) string {
	if len(parts) == 0 {
		return ""
	}
	var buf bytes.Buffer
	_ = syntax.NewPrinter().Print(&buf, &syntax.Word{Parts: parts})
	return buf.String()
}

func mixedCaseBraceRangeError(suffix string) error {
	return fmt.Errorf("bad substitution: no closing %q in `%s", "`", suffix)
}

func literalBrace(word *syntax.Word, left []syntax.WordPart, i int, br *syntax.BraceExp) ([]*syntax.Word, error) {
	next := *word
	next.Parts = next.Parts[i+1:]
	next.Parts = prependWordParts([]syntax.WordPart{&syntax.Lit{Value: wordPartsSource([]syntax.WordPart{br})}}, next.Parts)
	exp, err := Braces(&next)
	if err != nil {
		return nil, err
	}
	for _, w := range exp {
		w.Parts = prependWordParts(left, w.Parts)
	}
	return exp, nil
}

func braceSequenceStep(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n == minInt() {
		return 0, false
	}
	return n, true
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func minInt() int {
	return -maxInt() - 1
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func asciiLower(b byte) bool {
	return b >= 'a' && b <= 'z'
}

func asciiUpper(b byte) bool {
	return b >= 'A' && b <= 'Z'
}
