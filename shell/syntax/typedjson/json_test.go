// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package typedjson_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shell/syntax/typedjson"
)

var update = flag.Bool("u", false, "update output files")

func TestRoundtrip(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("testdata", "roundtrip")
	shellPaths, err := filepath.Glob(filepath.Join(dir, "*.sh"))
	qt.Assert(t, qt.IsNil(err))
	for _, shellPath := range shellPaths {
		name := strings.TrimSuffix(filepath.Base(shellPath), ".sh")
		jsonPath := filepath.Join(dir, name+".json")
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shellInput, err := os.ReadFile(shellPath)
			qt.Assert(t, qt.IsNil(err))
			jsonInput, err := os.ReadFile(jsonPath)
			if !*update { // allow it to not exist
				qt.Assert(t, qt.IsNil(err))
			}
			sb := new(strings.Builder)

			// Parse the shell source and check that it is well formatted.
			parser := syntax.NewParser(syntax.KeepComments(true))
			node, err := parser.Parse(bytes.NewReader(shellInput), "")
			qt.Assert(t, qt.IsNil(err))

			printer := syntax.NewPrinter()
			sb.Reset()
			err = printer.Print(sb, node)
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.Equals(sb.String(), string(shellInput)))

			// Validate writing the pretty JSON.
			sb.Reset()
			encOpts := typedjson.EncodeOptions{Indent: "\t"}
			err = encOpts.Encode(sb, node)
			qt.Assert(t, qt.IsNil(err))
			got := sb.String()
			if *update {
				err := os.WriteFile(jsonPath, []byte(got), 0o666)
				qt.Assert(t, qt.IsNil(err))
				jsonInput = []byte(got)
			} else {
				qt.Assert(t, qt.Equals(got, string(jsonInput)))
			}

			// Ensure we don't use the originally parsed node again.
			node = nil

			// Validate reading the pretty JSON and check that it formats the same.
			node2, err := typedjson.Decode(bytes.NewReader(jsonInput))
			qt.Assert(t, qt.IsNil(err))

			sb.Reset()
			err = printer.Print(sb, node2)
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.Equals(sb.String(), string(shellInput)))

			// Validate that emitting the JSON again produces the same result.
			sb.Reset()
			err = encOpts.Encode(sb, node2)
			qt.Assert(t, qt.IsNil(err))
			got = sb.String()
			qt.Assert(t, qt.Equals(got, string(jsonInput)))
		})
	}
}

func TestEncodeSubscriptKind(t *testing.T) {
	t.Parallel()

	node := &syntax.Subscript{
		Kind: syntax.SubscriptStar,
		Mode: syntax.SubscriptAssociative,
		Expr: &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: "*"},
		}},
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(buf.Len() > 0))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	sub, ok := decoded.(*syntax.Subscript)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(sub.Kind, syntax.SubscriptStar))
	qt.Assert(t, qt.Equals(sub.Mode, syntax.SubscriptAssociative))
}

func TestEncodeVarRefContext(t *testing.T) {
	t.Parallel()

	node := &syntax.VarRef{
		Name:    &syntax.Lit{Value: "assoc"},
		Index:   &syntax.Subscript{Kind: syntax.SubscriptExpr, Mode: syntax.SubscriptAssociative, Expr: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "k"}}}},
		Context: syntax.VarRefVarSet,
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	ref, ok := decoded.(*syntax.VarRef)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(ref.Context, syntax.VarRefVarSet))
	qt.Assert(t, qt.Equals(ref.Index.Mode, syntax.SubscriptAssociative))
}

func TestEncodeHeredocDelimiter(t *testing.T) {
	t.Parallel()

	node := &syntax.HeredocDelim{
		Parts: []syntax.WordPart{
			&syntax.DblQuoted{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "EOF"},
			}},
			&syntax.Lit{Value: "2"},
		},
		Value:       "EOF2",
		Quoted:      true,
		BodyExpands: false,
		ClosePos:    syntax.NewPos(10, 3, 1),
		CloseEnd:    syntax.NewPos(15, 3, 6),
		CloseRaw:    "\tEOF2",
		CloseCandidate: &syntax.HeredocCloseCandidate{
			Pos:               syntax.NewPos(11, 3, 2),
			End:               syntax.NewPos(15, 3, 6),
			Raw:               "EOF2",
			DelimOffset:       1,
			LeadingWhitespace: "\t",
		},
		Matched:      true,
		TrailingText: "",
		IndentMode:   syntax.HeredocIndentStripTabs,
		IndentTabs:   1,
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsTrue(buf.Len() > 0))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	delim, ok := decoded.(*syntax.HeredocDelim)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(delim.Value, "EOF2"))
	qt.Assert(t, qt.Equals(delim.Quoted, true))
	qt.Assert(t, qt.Equals(delim.BodyExpands, false))
	qt.Assert(t, qt.Equals(delim.ClosePos, syntax.NewPos(10, 3, 1)))
	qt.Assert(t, qt.Equals(delim.CloseEnd, syntax.NewPos(15, 3, 6)))
	qt.Assert(t, qt.Equals(delim.CloseRaw, "\tEOF2"))
	qt.Assert(t, qt.IsTrue(delim.CloseCandidate != nil))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.Pos, syntax.NewPos(11, 3, 2)))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.End, syntax.NewPos(15, 3, 6)))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.Raw, "EOF2"))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.DelimOffset, uint(1)))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.LeadingWhitespace, "\t"))
	qt.Assert(t, qt.Equals(delim.CloseCandidate.RawTokenMismatch, false))
	qt.Assert(t, qt.Equals(delim.Matched, true))
	qt.Assert(t, qt.Equals(delim.EOFTerminated, false))
	qt.Assert(t, qt.Equals(delim.IndentMode, syntax.HeredocIndentStripTabs))
	qt.Assert(t, qt.Equals(delim.IndentTabs, uint16(1)))
	qt.Assert(t, qt.Equals(len(delim.Parts), 2))
}

func TestEncodeArrayModes(t *testing.T) {
	t.Parallel()

	node := &syntax.ArrayExpr{
		Mode: syntax.ArrayExprAssociative,
		Elems: []*syntax.ArrayElem{{
			Kind:  syntax.ArrayElemKeyedAppend,
			Index: &syntax.Subscript{Kind: syntax.SubscriptExpr, Mode: syntax.SubscriptAssociative, Expr: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "k"}}}},
			Value: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "v"}}},
		}},
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	arr, ok := decoded.(*syntax.ArrayExpr)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(arr.Mode, syntax.ArrayExprAssociative))
	qt.Assert(t, qt.Equals(len(arr.Elems), 1))
	qt.Assert(t, qt.Equals(arr.Elems[0].Kind, syntax.ArrayElemKeyedAppend))
	qt.Assert(t, qt.Equals(arr.Elems[0].Index.Mode, syntax.SubscriptAssociative))
}

func TestEncodeIfClauseKind(t *testing.T) {
	t.Parallel()

	node := &syntax.IfClause{
		Kind: syntax.IfClauseIf,
		Else: &syntax.IfClause{
			Kind: syntax.IfClauseElif,
			Else: &syntax.IfClause{
				Kind: syntax.IfClauseElse,
			},
		},
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	ifClause, ok := decoded.(*syntax.IfClause)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(ifClause.Kind, syntax.IfClauseIf))
	qt.Assert(t, qt.IsNotNil(ifClause.Else))
	qt.Assert(t, qt.Equals(ifClause.Else.Kind, syntax.IfClauseElif))
	qt.Assert(t, qt.IsNotNil(ifClause.Else.Else))
	qt.Assert(t, qt.Equals(ifClause.Else.Else.Kind, syntax.IfClauseElse))
}

func TestEncodePatternGroup(t *testing.T) {
	t.Parallel()

	node := &syntax.Pattern{
		Parts: []syntax.PatternPart{
			&syntax.PatternGroup{
				Patterns: []*syntax.Pattern{
					{Parts: []syntax.PatternPart{&syntax.Lit{Value: "foo"}}},
					{Parts: []syntax.PatternPart{&syntax.Lit{Value: "bar"}}},
				},
			},
			&syntax.PatternAny{},
		},
	}

	var buf bytes.Buffer
	err := typedjson.Encode(&buf, node)
	qt.Assert(t, qt.IsNil(err))

	decoded, err := typedjson.Decode(bytes.NewReader(buf.Bytes()))
	qt.Assert(t, qt.IsNil(err))

	pat, ok := decoded.(*syntax.Pattern)
	qt.Assert(t, qt.IsTrue(ok))
	group, ok := pat.Parts[0].(*syntax.PatternGroup)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.HasLen(group.Patterns, 2))

	var printed bytes.Buffer
	err = syntax.NewPrinter().Print(&printed, &syntax.File{Stmts: []*syntax.Stmt{{
		Cmd: &syntax.TestClause{
			X: &syntax.CondBinary{
				Op: syntax.TsMatch,
				X:  &syntax.CondWord{Word: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "x"}}}},
				Y:  &syntax.CondPattern{Pattern: pat},
			},
		},
	}}})
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(printed.String(), "[[ x == (foo|bar)* ]]\n"))
}
