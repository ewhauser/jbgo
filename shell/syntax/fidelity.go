package syntax

func wordUnquotedBytes(w *Word) ([]byte, bool) {
	if w == nil {
		return nil, false
	}
	return wordUnquotedBytesRaw(w, []byte(w.raw))
}

func wordUnquotedBytesRaw(w *Word, raw []byte) ([]byte, bool) {
	buf := make([]byte, 0, 4)
	if w == nil {
		return buf, false
	}
	didUnquote := false
	rawBase := nodeRawBase(w)
	for _, wp := range w.Parts {
		var quoted bool
		buf, quoted = appendUnquotedWordPart(buf, wp, false, raw, rawBase)
		didUnquote = didUnquote || quoted
	}
	return buf, didUnquote
}

func appendUnquotedWordPart(buf []byte, wp WordPart, quotes bool, raw []byte, rawBase uint) (_ []byte, quoted bool) {
	switch wp := wp.(type) {
	case *Lit:
		for i := 0; i < len(wp.Value); i++ {
			if b := wp.Value[i]; b == '\\' && !quotes {
				if i++; i < len(wp.Value) {
					buf = append(buf, wp.Value[i])
				}
				quoted = true
			} else {
				buf = append(buf, b)
			}
		}
	case *SglQuoted:
		buf = append(buf, []byte(wp.Value)...)
		quoted = true
	case *DblQuoted:
		for _, wp2 := range wp.Parts {
			buf, _ = appendUnquotedWordPart(buf, wp2, true, raw, rawBase)
		}
		quoted = true
	case *BraceExp:
		buf = append(buf, '{')
		for i, elem := range wp.Elems {
			if i > 0 {
				if wp.Sequence {
					buf = append(buf, '.', '.')
				} else {
					buf = append(buf, ',')
				}
			}
			for _, elemPart := range elem.Parts {
				buf, _ = appendUnquotedWordPart(buf, elemPart, quotes, raw, rawBase)
			}
		}
		buf = append(buf, '}')
	default:
		if partRaw, ok := nodeRawBytes(raw, rawBase, wp); ok {
			buf = append(buf, partRaw...)
		} else {
			buf = append(buf, wordPartString(wp)...)
		}
	}
	return buf, quoted
}

func patternUnquotedBytes(pat *Pattern) ([]byte, bool) {
	if pat == nil {
		return nil, false
	}
	return patternUnquotedBytesRaw(pat, []byte(pat.raw))
}

func patternUnquotedBytesRaw(pat *Pattern, raw []byte) ([]byte, bool) {
	buf := make([]byte, 0, 4)
	if pat == nil {
		return buf, false
	}
	didUnquote := false
	rawBase := nodeRawBase(pat)
	for _, part := range pat.Parts {
		var quoted bool
		buf, quoted = appendUnquotedPatternPart(buf, part, false, raw, rawBase)
		didUnquote = didUnquote || quoted
	}
	return buf, didUnquote
}

func appendUnquotedPatternPart(buf []byte, part PatternPart, quotes bool, raw []byte, rawBase uint) (_ []byte, quoted bool) {
	switch part := part.(type) {
	case *Lit:
		for i := 0; i < len(part.Value); i++ {
			if b := part.Value[i]; b == '\\' && !quotes {
				if i++; i < len(part.Value) {
					buf = append(buf, part.Value[i])
				}
				quoted = true
			} else {
				buf = append(buf, b)
			}
		}
	case *SglQuoted:
		buf = append(buf, []byte(part.Value)...)
		quoted = true
	case *DblQuoted:
		for _, wp := range part.Parts {
			buf, _ = appendUnquotedWordPart(buf, wp, true, raw, rawBase)
		}
		quoted = true
	case *PatternAny:
		if partRaw, ok := nodeRawBytes(raw, rawBase, part); ok {
			buf = append(buf, partRaw...)
		} else {
			buf = append(buf, '*')
		}
	case *PatternSingle:
		if partRaw, ok := nodeRawBytes(raw, rawBase, part); ok {
			buf = append(buf, partRaw...)
		} else {
			buf = append(buf, '?')
		}
	case *PatternCharClass:
		if partRaw, ok := nodeRawBytes(raw, rawBase, part); ok {
			buf = append(buf, partRaw...)
		} else {
			buf = append(buf, part.Value...)
		}
	case *ExtGlob:
		buf = append(buf, globOperatorText(part.Op), '(')
		for i, pat := range part.Patterns {
			if i > 0 {
				buf = append(buf, '|')
			}
			var patRaw []byte
			if rawPart, ok := nodeRawBytes(raw, rawBase, pat); ok {
				patRaw = rawPart
			} else if pat != nil && pat.raw != "" {
				patRaw = []byte(pat.raw)
			}
			patBuf, patQuoted := patternUnquotedBytesRaw(pat, patRaw)
			buf = append(buf, patBuf...)
			quoted = quoted || patQuoted
		}
		buf = append(buf, ')')
	default:
		if partRaw, ok := nodeRawBytes(raw, rawBase, part); ok {
			buf = append(buf, partRaw...)
		} else if wp, ok := part.(WordPart); ok {
			buf = append(buf, wordPartString(wp)...)
		}
	}
	return buf, quoted
}

func globOperatorText(op GlobOperator) byte {
	switch op {
	case GlobZeroOrOne:
		return '?'
	case GlobZeroOrMore:
		return '*'
	case GlobOneOrMore:
		return '+'
	case GlobOne:
		return '@'
	case GlobExcept:
		return '!'
	default:
		return 0
	}
}

func nodeRawBase(node Node) uint {
	if node == nil || !node.Pos().IsValid() {
		return 0
	}
	return node.Pos().Offset()
}

func nodeRawBytes(raw []byte, rawBase uint, node Node) ([]byte, bool) {
	if len(raw) == 0 || node == nil || !node.Pos().IsValid() || !node.End().IsValid() {
		return nil, false
	}
	start := int(node.Pos().Offset() - rawBase)
	end := int(node.End().Offset() - rawBase)
	if start < 0 || end < start || end > len(raw) {
		return nil, false
	}
	return raw[start:end], true
}
