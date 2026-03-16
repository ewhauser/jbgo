package builtins

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type TR struct{}

type trOptions struct {
	complement bool
	delete     bool
	squeeze    bool
	truncate   bool
}

type trClass int

const (
	trClassAlnum trClass = iota
	trClassAlpha
	trClassBlank
	trClassCntrl
	trClassDigit
	trClassGraph
	trClassLower
	trClassPrint
	trClassPunct
	trClassSpace
	trClassUpper
	trClassXdigit
)

type trSequenceKind int

const (
	trSequenceChar trSequenceKind = iota
	trSequenceRange
	trSequenceStar
	trSequenceRepeat
	trSequenceClass
)

type trSequence struct {
	kind  trSequenceKind
	char  byte
	start byte
	end   byte
	count int
	class trClass
}

func NewTR() *TR {
	return &TR{}
}

func (c *TR) Name() string {
	return "tr"
}

func (c *TR) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *TR) Spec() CommandSpec {
	return CommandSpec{
		Name:  "tr",
		About: "Translate, squeeze, and/or delete characters from standard input, writing to standard output.",
		Usage: "tr [OPTION]... STRING1 [STRING2]",
		Options: []OptionSpec{
			{Name: "complement", Short: 'c', ShortAliases: []rune{'C'}, Long: "complement", Help: "use the complement of STRING1"},
			{Name: "delete", Short: 'd', Long: "delete", Help: "delete characters in STRING1, do not translate"},
			{Name: "squeeze", Short: 's', Long: "squeeze-repeats", Help: "replace each sequence of a repeated character that is listed in the last specified STRING with a single occurrence of that character"},
			{Name: "truncate", Short: 't', Long: "truncate-set1", Help: "first truncate STRING1 to length of STRING2"},
		},
		Args: []ArgSpec{
			{Name: "set", ValueName: "STRING", Repeatable: true, Required: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *TR) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, sets, err := parseTRMatches(inv, matches)
	if err != nil {
		return err
	}

	if err := warnTrailingBackslash(inv, sets[0]); err != nil {
		return err
	}

	translating := !opts.delete && len(sets) > 1
	set1, set2, err := solveTRSets(sets[0], trSecondSet(sets), opts.complement, opts.truncate && translating, translating)
	if err != nil {
		return exitf(inv, 1, "tr: %s", err.Error())
	}

	data, err := readAllStdin(ctx, inv)
	if err != nil {
		return err
	}

	var out []byte
	switch {
	case opts.delete && opts.squeeze:
		out = trProcessDeleteSqueeze(data, set1, set2)
	case opts.delete:
		out = trProcessDelete(data, set1)
	case opts.squeeze && len(sets) == 1:
		out = trProcessSqueeze(data, set1)
	case opts.squeeze:
		translated, err := trProcessTranslate(data, set1, set2)
		if err != nil {
			return exitf(inv, 1, "tr: %s", err.Error())
		}
		out = trProcessSqueeze(translated, set2)
	default:
		out, err = trProcessTranslate(data, set1, set2)
		if err != nil {
			return exitf(inv, 1, "tr: %s", err.Error())
		}
	}

	if _, err := inv.Stdout.Write(out); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseTRMatches(inv *Invocation, matches *ParsedCommand) (trOptions, []string, error) {
	opts := trOptions{
		complement: matches.Has("complement"),
		delete:     matches.Has("delete"),
		squeeze:    matches.Has("squeeze"),
		truncate:   matches.Has("truncate"),
	}
	sets := matches.Args("set")
	if len(sets) == 0 {
		return trOptions{}, nil, exitf(inv, 1, "tr: missing operand")
	}
	if !opts.delete && !opts.squeeze && len(sets) == 1 {
		return trOptions{}, nil, exitf(inv, 1, "tr: missing operand after %q", sets[0])
	}
	if opts.delete && opts.squeeze && len(sets) == 1 {
		return trOptions{}, nil, exitf(inv, 1, "tr: missing operand after %q", sets[0])
	}
	if len(sets) > 1 && opts.delete && !opts.squeeze {
		return trOptions{}, nil, exitf(inv, 1, "tr: extra operand %q", sets[1])
	}
	if len(sets) > 2 {
		return trOptions{}, nil, exitf(inv, 1, "tr: extra operand %q", sets[2])
	}
	return opts, sets, nil
}

func trSecondSet(sets []string) string {
	if len(sets) < 2 {
		return ""
	}
	return sets[1]
}

func warnTrailingBackslash(inv *Invocation, value string) error {
	if value == "" {
		return nil
	}
	count := 0
	for i := len(value) - 1; i >= 0 && value[i] == '\\'; i-- {
		count++
	}
	if count%2 == 1 {
		if _, err := io.WriteString(inv.Stderr, "tr: warning: an unescaped backslash at end of string is not portable\n"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func solveTRSets(set1Text, set2Text string, complement, truncateSet1, translating bool) (set1Solved, set2Solved []byte, err error) {
	set1Seqs, err := parseTRSequences([]byte(set1Text))
	if err != nil {
		return nil, nil, err
	}
	for _, seq := range set1Seqs {
		if seq.kind == trSequenceStar || seq.kind == trSequenceRepeat {
			return nil, nil, fmt.Errorf("misaligned [:upper:] and/or [:lower:] construct")
		}
	}

	set2Seqs, err := parseTRSequences([]byte(set2Text))
	if err != nil {
		return nil, nil, err
	}
	starCount := 0
	for _, seq := range set2Seqs {
		if seq.kind == trSequenceStar {
			starCount++
		}
		if translating && seq.kind == trSequenceClass && seq.class != trClassLower && seq.class != trClassUpper {
			return nil, nil, fmt.Errorf("when translating, the only character classes that may appear in string2 are 'upper' and 'lower'")
		}
	}
	if starCount > 1 {
		return nil, nil, fmt.Errorf("only one [c*] repeat construct may appear in string2")
	}

	set1Solved = trFlattenSequences(set1Seqs)
	if complement {
		present := [256]bool{}
		for _, b := range set1Solved {
			present[b] = true
		}
		comp := make([]byte, 0, 256-len(set1Solved))
		for i := 0; i <= 0xff; i++ {
			if !present[byte(i)] {
				comp = append(comp, byte(i))
			}
		}
		set1Solved = comp
	}
	set1Len := len(set1Solved)

	set2Len := 0
	for _, seq := range set2Seqs {
		if seq.kind == trSequenceStar {
			continue
		}
		set2Len += trSequenceLen(seq)
	}
	starCompensate := max(set1Len-set2Len, 0)
	resolvedSeqs := make([]trSequence, 0, len(set2Seqs))
	for _, seq := range set2Seqs {
		if seq.kind == trSequenceStar {
			if seq.char == 0 {
				continue
			}
			seq.kind = trSequenceRepeat
			seq.count = starCompensate
		}
		resolvedSeqs = append(resolvedSeqs, seq)
	}
	set2Seqs = resolvedSeqs

	for set2Pos, seq2 := range set2Seqs {
		if seq2.kind != trSequenceClass {
			continue
		}
		prefix2 := 0
		for _, item := range set2Seqs[:set2Pos] {
			prefix2 += trSequenceLen(item)
		}
		classMatched := false
		for set1Pos, seq1 := range set1Seqs {
			if seq1.kind != trSequenceClass {
				continue
			}
			prefix1 := 0
			for _, item := range set1Seqs[:set1Pos] {
				prefix1 += trSequenceLen(item)
			}
			if prefix1 == prefix2 {
				classMatched = true
				break
			}
		}
		if !classMatched {
			return nil, nil, fmt.Errorf("misaligned [:upper:] and/or [:lower:] construct")
		}
	}

	set2Solved = trFlattenSequences(set2Seqs)
	if complement && translating && trHasClass(set1Seqs) {
		unique := map[byte]struct{}{}
		for _, b := range set2Solved {
			unique[b] = struct{}{}
		}
		if len(unique) > 1 || len(set2Solved) > set1Len {
			return nil, nil, fmt.Errorf("when translating with complemented character classes,\nstring2 must map all characters in the domain to one")
		}
	}

	if len(set2Solved) < len(set1Solved) {
		if truncateSet1 {
			set1Solved = set1Solved[:len(set2Solved)]
		} else if len(set2Seqs) > 0 {
			last := set2Seqs[len(set2Seqs)-1]
			if last.kind == trSequenceClass && (last.class == trClassLower || last.class == trClassUpper) {
				return nil, nil, fmt.Errorf("when translating with string1 longer than string2,\nthe latter string must not end with a character class")
			}
		}
	}
	return set1Solved, set2Solved, nil
}

func trHasClass(seqs []trSequence) bool {
	for _, seq := range seqs {
		if seq.kind == trSequenceClass {
			return true
		}
	}
	return false
}

func parseTRSequences(input []byte) ([]trSequence, error) {
	seqs := make([]trSequence, 0)
	for i := 0; i < len(input); {
		if seq, next, ok, err := parseTRCharRange(input, i); ok || err != nil {
			if err != nil {
				return nil, err
			}
			seqs = append(seqs, seq)
			i = next
			continue
		}
		if seq, next, ok, err := parseTRCharStar(input, i); ok || err != nil {
			if err != nil {
				return nil, err
			}
			seqs = append(seqs, seq)
			i = next
			continue
		}
		if seq, next, ok, err := parseTRCharRepeat(input, i); ok || err != nil {
			if err != nil {
				return nil, err
			}
			seqs = append(seqs, seq)
			i = next
			continue
		}
		if seq, next, ok, err := parseTRClass(input, i); ok || err != nil {
			if err != nil {
				return nil, err
			}
			seqs = append(seqs, seq)
			i = next
			continue
		}
		if seq, next, ok, err := parseTRCharEqual(input, i); ok || err != nil {
			if err != nil {
				return nil, err
			}
			seqs = append(seqs, seq)
			i = next
			continue
		}
		b, next, err := parseTRByteWithWarning(input, i)
		if err != nil {
			return nil, err
		}
		seqs = append(seqs, trSequence{kind: trSequenceChar, char: b})
		i = next
	}
	return seqs, nil
}

func parseTRCharRange(input []byte, i int) (seq trSequence, next int, ok bool, err error) {
	left, next, err := parseTRByte(input, i)
	if err != nil {
		return trSequence{}, 0, false, err
	}
	if next >= len(input) || input[next] != '-' {
		return trSequence{}, 0, false, nil
	}
	right, end, err := parseTRByte(input, next+1)
	if err != nil {
		return trSequence{}, 0, false, nil //nolint:nilerr // parse failure means this parser doesn't match
	}
	if left > right {
		return trSequence{}, 0, true, fmt.Errorf("range-endpoints of '%c-%c' are in reverse collating sequence order", left, right)
	}
	return trSequence{kind: trSequenceRange, start: left, end: right}, end, true, nil
}

func parseTRCharStar(input []byte, i int) (seq trSequence, next int, ok bool, err error) {
	if i+4 > len(input) || input[i] != '[' {
		return trSequence{}, 0, false, nil
	}
	ch, next, err := parseTRByte(input, i+1)
	if err != nil {
		return trSequence{}, 0, false, nil //nolint:nilerr // parse failure means this parser doesn't match
	}
	if next+2 <= len(input) && input[next] == '*' && input[next+1] == ']' {
		return trSequence{kind: trSequenceStar, char: ch}, next + 2, true, nil
	}
	return trSequence{}, 0, false, nil
}

func parseTRCharRepeat(input []byte, i int) (seq trSequence, next int, ok bool, err error) {
	if i+5 > len(input) || input[i] != '[' {
		return trSequence{}, 0, false, nil
	}
	ch, next, err := parseTRByte(input, i+1)
	if err != nil || next >= len(input) || input[next] != '*' {
		return trSequence{}, 0, false, nil //nolint:nilerr // parse failure means this parser doesn't match
	}
	end := next + 1
	for end < len(input) && input[end] != ']' && input[end] != '\\' {
		end++
	}
	if end >= len(input) || input[end] != ']' {
		return trSequence{}, 0, false, nil
	}
	countStr := string(input[next+1 : end])
	count, kind, err := parseTRRepeatCount(countStr)
	if err != nil {
		return trSequence{}, 0, true, fmt.Errorf("invalid repeat count '%s' in [c*n] construct", countStr)
	}
	if kind == trSequenceStar {
		return trSequence{kind: trSequenceStar, char: ch}, end + 1, true, nil
	}
	return trSequence{kind: trSequenceRepeat, char: ch, count: count}, end + 1, true, nil
}

func parseTRRepeatCount(value string) (int, trSequenceKind, error) {
	if strings.HasPrefix(value, "0") {
		count, err := strconv.ParseInt(value, 8, 64)
		if err != nil {
			return 0, trSequenceRepeat, err
		}
		if count == 0 {
			return 0, trSequenceStar, nil
		}
		return int(count), trSequenceRepeat, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, trSequenceRepeat, err
	}
	if count == 0 {
		return 0, trSequenceStar, nil
	}
	return count, trSequenceRepeat, nil
}

func parseTRClass(input []byte, i int) (seq trSequence, next int, ok bool, err error) {
	if i+4 > len(input) || string(input[i:i+2]) != "[:" {
		return trSequence{}, 0, false, nil
	}
	end := i + 2
	for end+1 < len(input) && (input[end] != ':' || input[end+1] != ']') {
		end++
	}
	if end+1 >= len(input) {
		return trSequence{}, 0, false, nil
	}
	name := string(input[i+2 : end])
	class, ok := trLookupClass(name)
	if !ok {
		if name == "" {
			return trSequence{}, 0, true, fmt.Errorf("missing character class name '[::]'")
		}
		return trSequence{}, 0, false, nil
	}
	return trSequence{kind: trSequenceClass, class: class}, end + 2, true, nil
}

func parseTRCharEqual(input []byte, i int) (seq trSequence, next int, ok bool, err error) {
	if i+4 > len(input) || string(input[i:i+2]) != "[=" {
		return trSequence{}, 0, false, nil
	}
	if i+4 <= len(input) && string(input[i:i+4]) == "[==]" {
		return trSequence{}, 0, true, fmt.Errorf("missing equivalence class character '[==]'")
	}
	ch, next, err := parseTRByte(input, i+2)
	if err != nil {
		return trSequence{}, 0, false, nil //nolint:nilerr // parse failure means this parser doesn't match
	}
	if next+1 < len(input) && input[next] == '=' && input[next+1] == ']' {
		return trSequence{kind: trSequenceChar, char: ch}, next + 2, true, nil
	}
	end := next
	for end+1 < len(input) && (input[end] != '=' || input[end+1] != ']') {
		end++
	}
	if end+1 >= len(input) {
		return trSequence{}, 0, false, nil
	}
	return trSequence{}, 0, true, fmt.Errorf("equivalence class operand '%s' must be a single character", string(input[i+2:end]))
}

func trLookupClass(name string) (trClass, bool) {
	switch name {
	case "alnum":
		return trClassAlnum, true
	case "alpha":
		return trClassAlpha, true
	case "blank":
		return trClassBlank, true
	case "cntrl":
		return trClassCntrl, true
	case "digit":
		return trClassDigit, true
	case "graph":
		return trClassGraph, true
	case "lower":
		return trClassLower, true
	case "print":
		return trClassPrint, true
	case "punct":
		return trClassPunct, true
	case "space":
		return trClassSpace, true
	case "upper":
		return trClassUpper, true
	case "xdigit":
		return trClassXdigit, true
	default:
		return 0, false
	}
}

func trFlattenSequences(seqs []trSequence) []byte {
	out := make([]byte, 0)
	for _, seq := range seqs {
		switch seq.kind {
		case trSequenceChar:
			out = append(out, seq.char)
		case trSequenceRange:
			for b := seq.start; b <= seq.end; b++ {
				out = append(out, b)
			}
		case trSequenceRepeat:
			for range seq.count {
				out = append(out, seq.char)
			}
		case trSequenceStar:
			out = append(out, seq.char)
		case trSequenceClass:
			out = append(out, trClassBytes(seq.class)...)
		}
	}
	return out
}

func trSequenceLen(seq trSequence) int {
	switch seq.kind {
	case trSequenceChar:
		return 1
	case trSequenceRange:
		return int(seq.end-seq.start) + 1
	case trSequenceRepeat:
		return seq.count
	case trSequenceStar:
		return 1
	case trSequenceClass:
		return len(trClassBytes(seq.class))
	default:
		return 0
	}
}

func trClassBytes(class trClass) []byte {
	var out []byte
	for b := 0; b <= 0xff; b++ {
		c := byte(b)
		switch class {
		case trClassAlnum:
			if isASCIIAlpha(c) || isASCIIDigit(c) {
				out = append(out, c)
			}
		case trClassAlpha:
			if isASCIIAlpha(c) {
				out = append(out, c)
			}
		case trClassBlank:
			if c == ' ' || c == '\t' {
				out = append(out, c)
			}
		case trClassCntrl:
			if c <= 31 || c == 127 {
				out = append(out, c)
			}
		case trClassDigit:
			if isASCIIDigit(c) {
				out = append(out, c)
			}
		case trClassGraph:
			if c >= 33 && c <= 126 {
				out = append(out, c)
			}
		case trClassLower:
			if c >= 'a' && c <= 'z' {
				out = append(out, c)
			}
		case trClassPrint:
			if c >= 32 && c <= 126 {
				out = append(out, c)
			}
		case trClassPunct:
			if (c >= 33 && c <= 47) || (c >= 58 && c <= 64) || (c >= 91 && c <= 96) || (c >= 123 && c <= 126) {
				out = append(out, c)
			}
		case trClassSpace:
			if isASCIISpace(c) {
				out = append(out, c)
			}
		case trClassUpper:
			if c >= 'A' && c <= 'Z' {
				out = append(out, c)
			}
		case trClassXdigit:
			if isASCIIDigit(c) || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f') {
				out = append(out, c)
			}
		}
	}
	return out
}

func parseTRByte(input []byte, i int) (value byte, next int, err error) {
	if i >= len(input) {
		return 0, i, io.EOF
	}
	if input[i] != '\\' {
		return input[i], i + 1, nil
	}
	return parseTRBackslashByte(input, i)
}

func parseTRByteWithWarning(input []byte, i int) (value byte, next int, err error) {
	if i >= len(input) {
		return 0, i, io.EOF
	}
	if input[i] != '\\' {
		return input[i], i + 1, nil
	}
	if i+1 < len(input) && isOctalDigit(input[i+1]) {
		end := i + 1
		for end < len(input) && end < i+4 && isOctalDigit(input[end]) {
			end++
		}
		if end-i == 4 {
			if value, err := strconv.ParseUint(string(input[i+1:end]), 8, 8); err == nil {
				return byte(value), end, nil
			}
			if end-1 > i+1 {
				value, err := strconv.ParseUint(string(input[i+1:end-1]), 8, 8)
				if err == nil {
					return byte(value), end - 1, nil
				}
			}
		}
	}
	return parseTRBackslashByte(input, i)
}

func parseTRBackslashByte(input []byte, i int) (value byte, next int, err error) {
	if i+1 >= len(input) {
		return '\\', i + 1, nil
	}
	switch input[i+1] {
	case 'a':
		return '\a', i + 2, nil
	case 'b':
		return '\b', i + 2, nil
	case 'f':
		return '\f', i + 2, nil
	case 'n':
		return '\n', i + 2, nil
	case 'r':
		return '\r', i + 2, nil
	case 't':
		return '\t', i + 2, nil
	case 'v':
		return '\v', i + 2, nil
	}
	if isOctalDigit(input[i+1]) {
		end := i + 1
		for end < len(input) && end < i+4 && isOctalDigit(input[end]) {
			end++
		}
		value, err := strconv.ParseUint(string(input[i+1:end]), 8, 8)
		if err != nil {
			return 0, i, err
		}
		return byte(value), end, nil
	}
	return input[i+1], i + 2, nil
}

func trProcessDelete(data, set []byte) []byte {
	table := trBitmap(set)
	out := make([]byte, 0, len(data))
	for _, b := range data {
		if !table[b] {
			out = append(out, b)
		}
	}
	return out
}

func trProcessSqueeze(data, set []byte) []byte {
	table := trBitmap(set)
	out := make([]byte, 0, len(data))
	var prev byte
	hasPrev := false
	for _, b := range data {
		if table[b] && hasPrev && prev == b {
			continue
		}
		out = append(out, b)
		prev = b
		hasPrev = true
	}
	return out
}

func trProcessDeleteSqueeze(data, deleteSet, squeezeSet []byte) []byte {
	deletes := trBitmap(deleteSet)
	squeezes := trBitmap(squeezeSet)
	out := make([]byte, 0, len(data))
	var prev byte
	hasPrev := false
	for _, b := range data {
		if deletes[b] {
			continue
		}
		if squeezes[b] && hasPrev && prev == b {
			continue
		}
		out = append(out, b)
		prev = b
		hasPrev = true
	}
	return out
}

func trProcessTranslate(data, set1, set2 []byte) ([]byte, error) {
	table := [256]byte{}
	for i := range 256 {
		table[i] = byte(i)
	}
	if len(set2) == 0 {
		if len(set1) == 0 {
			return append([]byte(nil), data...), nil
		}
		return nil, fmt.Errorf("when not truncating set1, string2 must be non-empty")
	}
	fallback := set2[len(set2)-1]
	for i, from := range set1 {
		to := fallback
		if i < len(set2) {
			to = set2[i]
		}
		table[from] = to
	}
	out := make([]byte, len(data))
	for i, b := range data {
		out[i] = table[b]
	}
	return out, nil
}

func trBitmap(set []byte) [256]bool {
	var table [256]bool
	for _, b := range set {
		table[b] = true
	}
	return table
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func isOctalDigit(b byte) bool {
	return b >= '0' && b <= '7'
}

var _ Command = (*TR)(nil)
var _ SpecProvider = (*TR)(nil)
var _ ParsedRunner = (*TR)(nil)
