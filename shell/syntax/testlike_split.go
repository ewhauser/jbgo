package syntax

func wordTestLikeSplit(w *Word) *TestLikeSplit {
	if w == nil || len(w.Parts) == 0 {
		return nil
	}

	literalPrefixOnly := true
	return wordTestLikeSplitParts(w, w.Parts, &literalPrefixOnly)
}

func wordTestLikeSplitParts(w *Word, parts []WordPart, literalPrefixOnly *bool) *TestLikeSplit {
	if !*literalPrefixOnly {
		return nil
	}
	for _, part := range parts {
		if !*literalPrefixOnly {
			return nil
		}
		switch part := part.(type) {
		case *Lit:
			if split := wordTestLikeSplitLiteral(w, part, part.Pos(), part.Value); split != nil {
				return split
			}
		case *SglQuoted:
			if split := wordTestLikeSplitLiteral(w, part, quotedContentStart(part.Left, part.Dollar), part.Value); split != nil {
				return split
			}
		case *DblQuoted:
			if split := wordTestLikeSplitParts(w, part.Parts, literalPrefixOnly); split != nil {
				return split
			}
		default:
			*literalPrefixOnly = false
		}
	}
	return nil
}

func wordTestLikeSplitLiteral(w *Word, part WordPart, start Pos, text string) *TestLikeSplit {
	sourceMap := literalValueSourceMap(w, part, text)
	for i := 0; i < len(text); i++ {
		op := matchTestLikeOperator(text[i:])
		if op == "" {
			continue
		}
		opPos := posAddCol(start, i)
		opEnd := posAddCol(opPos, len(op))
		if sourceMap.ok {
			opPos = posAtSourceOffset(start, sourceMap.raw, sourceMap.offsets[i])
			opEnd = posAtSourceOffset(start, sourceMap.raw, sourceMap.offsets[i+len(op)])
		}
		if split := buildTestLikeSplit(w, op, opPos, opEnd); split != nil {
			return split
		}
	}
	return nil
}

func matchTestLikeOperator(text string) string {
	switch {
	case len(text) >= 2 && text[:2] == "==":
		return "=="
	case len(text) >= 2 && text[:2] == "!=":
		return "!="
	case len(text) >= 2 && text[:2] == "=~":
		return "=~"
	case len(text) >= 1 && text[0] == '=':
		return "="
	default:
		return ""
	}
}

func buildTestLikeSplit(w *Word, op string, opPos, opEnd Pos) *TestLikeSplit {
	leftParts, rightParts, ok := splitWordPartsForSpan(w, w.Parts, opPos, opEnd)
	if !ok || len(leftParts) == 0 || len(rightParts) == 0 {
		return nil
	}
	split := &TestLikeSplit{
		Left:        newSyntheticWord(leftParts, sliceWordRaw(w, leftParts[0].Pos(), opPos)),
		Operator:    op,
		OperatorPos: opPos,
		OperatorEnd: opEnd,
		Right:       newSyntheticWord(rightParts, sliceWordRaw(w, rightParts[0].Pos(), w.End())),
	}
	if split.Left == nil || split.Right == nil {
		return nil
	}
	if split.Left.UnquotedText() == "" || split.Right.UnquotedText() == "" {
		return nil
	}
	return split
}

func newSyntheticWord(parts []WordPart, raw string) *Word {
	if len(parts) == 0 {
		return nil
	}
	return &Word{
		Parts: parts,
		raw:   raw,
	}
}

func sliceWordRaw(w *Word, start, end Pos) string {
	if w == nil || w.raw == "" || !w.Pos().IsValid() || !start.IsValid() || !end.IsValid() {
		return ""
	}
	rawBase := w.Pos().Offset()
	lo := int(start.Offset() - rawBase)
	hi := int(end.Offset() - rawBase)
	if lo < 0 || hi < lo || hi > len(w.raw) {
		return ""
	}
	return w.raw[lo:hi]
}

func splitWordPartsForSpan(w *Word, parts []WordPart, start, end Pos) (left, right []WordPart, ok bool) {
	found := false
	for _, part := range parts {
		if found {
			right = append(right, part)
			continue
		}
		if part.End().Offset() <= start.Offset() {
			left = append(left, part)
			continue
		}
		if part.Pos().Offset() >= end.Offset() {
			return nil, nil, false
		}

		leftPart, rightPart, partOK := splitWordPartForSpan(w, part, start, end)
		if !partOK {
			return nil, nil, false
		}
		if leftPart != nil {
			left = append(left, leftPart)
		}
		if rightPart != nil {
			right = append(right, rightPart)
		}
		found = true
	}
	return left, right, found
}

func splitWordPartForSpan(w *Word, part WordPart, start, end Pos) (left, right WordPart, ok bool) {
	switch part := part.(type) {
	case *Lit:
		return splitLitForSpan(w, part, start, end)
	case *SglQuoted:
		return splitSglQuotedForSpan(w, part, start, end)
	case *DblQuoted:
		innerLeft, innerRight, ok := splitWordPartsForSpan(w, part.Parts, start, end)
		if !ok {
			return nil, nil, false
		}
		if len(innerLeft) > 0 {
			left = &DblQuoted{
				Left:   part.Left,
				Right:  posAddCol(start, -1),
				Dollar: part.Dollar,
				Parts:  innerLeft,
			}
		}
		if len(innerRight) > 0 {
			right = &DblQuoted{
				Left:   end,
				Right:  part.Right,
				Dollar: part.Dollar,
				Parts:  innerRight,
			}
		}
		return left, right, true
	default:
		return nil, nil, false
	}
}

func splitLitForSpan(w *Word, lit *Lit, start, end Pos) (left, right WordPart, ok bool) {
	startIdx, endIdx, ok := literalSplitIndexes(w, lit, lit.Value, start, end)
	if startIdx < 0 || endIdx < startIdx || endIdx > len(lit.Value) {
		return nil, nil, false
	}
	if startIdx > 0 {
		left = &Lit{
			ValuePos: lit.ValuePos,
			ValueEnd: start,
			Value:    lit.Value[:startIdx],
		}
	}
	if endIdx < len(lit.Value) {
		right = &Lit{
			ValuePos: end,
			ValueEnd: lit.ValueEnd,
			Value:    lit.Value[endIdx:],
		}
	}
	return left, right, true
}

func splitSglQuotedForSpan(w *Word, part *SglQuoted, start, end Pos) (left, right WordPart, ok bool) {
	startIdx, endIdx, ok := literalSplitIndexes(w, part, part.Value, start, end)
	if startIdx < 0 || endIdx < startIdx || endIdx > len(part.Value) {
		return nil, nil, false
	}
	if startIdx > 0 {
		left = &SglQuoted{
			Left:   part.Left,
			Right:  posAddCol(start, -1),
			Dollar: part.Dollar,
			Value:  part.Value[:startIdx],
		}
	}
	if endIdx < len(part.Value) {
		right = &SglQuoted{
			Left:   end,
			Right:  part.Right,
			Dollar: part.Dollar,
			Value:  part.Value[endIdx:],
		}
	}
	return left, right, true
}

func quotedContentStart(left Pos, dollar bool) Pos {
	if dollar {
		return posAddCol(left, 2)
	}
	return posAddCol(left, 1)
}

func literalSplitIndexes(w *Word, part WordPart, text string, start, end Pos) (startIdx, endIdx int, ok bool) {
	valueStart := part.Pos()
	if sgl, ok := part.(*SglQuoted); ok {
		valueStart = quotedContentStart(sgl.Left, sgl.Dollar)
	}

	startIdx = int(start.Offset() - valueStart.Offset())
	endIdx = int(end.Offset() - valueStart.Offset())

	sourceMap := literalValueSourceMap(w, part, text)
	if !sourceMap.ok {
		return startIdx, endIdx, true
	}

	startRaw := int(start.Offset() - valueStart.Offset())
	endRaw := int(end.Offset() - valueStart.Offset())
	startIdx, ok = literalValueIndexForSourceOffset(sourceMap.offsets, startRaw)
	if !ok {
		return 0, 0, false
	}
	endIdx, ok = literalValueIndexForSourceOffset(sourceMap.offsets, endRaw)
	if !ok {
		return 0, 0, false
	}
	return startIdx, endIdx, true
}

type literalSourceMap struct {
	offsets []int
	raw     []byte
	ok      bool
}

func literalValueSourceMap(w *Word, part WordPart, text string) literalSourceMap {
	switch part := part.(type) {
	case *Lit:
		raw, ok := wordPartRawBytes(w, part)
		if !ok {
			return identityLiteralSourceMap(text)
		}
		offsets, ok := shellLiteralSourceOffsets(raw, text)
		return literalSourceMap{offsets: offsets, raw: raw, ok: ok}
	case *SglQuoted:
		raw, ok := sglQuotedContentRawBytes(w, part)
		if !ok {
			return identityLiteralSourceMap(text)
		}
		return literalSourceMap{offsets: identitySourceOffsets(text), raw: raw, ok: true}
	default:
		return literalSourceMap{offsets: identitySourceOffsets(text)}
	}
}

func identityLiteralSourceMap(text string) literalSourceMap {
	return literalSourceMap{
		offsets: identitySourceOffsets(text),
		raw:     []byte(text),
		ok:      true,
	}
}

func wordPartRawBytes(w *Word, part WordPart) ([]byte, bool) {
	if w == nil {
		return nil, false
	}
	return nodeRawBytes([]byte(w.raw), nodeRawBase(w), part)
}

func sglQuotedContentRawBytes(w *Word, part *SglQuoted) ([]byte, bool) {
	raw, ok := wordPartRawBytes(w, part)
	if !ok {
		return nil, false
	}
	if part.Dollar {
		if len(raw) < 3 {
			return nil, false
		}
		return raw[2 : len(raw)-1], true
	}
	if len(raw) < 2 {
		return nil, false
	}
	return raw[1 : len(raw)-1], true
}

func shellLiteralSourceOffsets(raw []byte, text string) ([]int, bool) {
	offsets := make([]int, 0, len(text)+1)
	rawIdx := 0
	for len(offsets) < len(text) {
		rawIdx = skipShellLineContinuations(raw, rawIdx)
		if rawIdx >= len(raw) {
			return identitySourceOffsets(text), false
		}
		offsets = append(offsets, rawIdx)
		rawIdx++
	}
	rawIdx = skipShellLineContinuations(raw, rawIdx)
	offsets = append(offsets, rawIdx)
	return offsets, len(offsets) == len(text)+1
}

func identitySourceOffsets(text string) []int {
	offsets := make([]int, len(text)+1)
	for i := range len(offsets) {
		offsets[i] = i
	}
	return offsets
}

func literalValueIndexForSourceOffset(offsets []int, rawOffset int) (int, bool) {
	for i, offset := range offsets {
		if offset == rawOffset {
			return i, true
		}
	}
	return 0, false
}

func skipShellLineContinuations(raw []byte, rawIdx int) int {
	for rawIdx < len(raw) && raw[rawIdx] == '\\' {
		if rawIdx+1 < len(raw) && raw[rawIdx+1] == '\n' {
			rawIdx += 2
			continue
		}
		if rawIdx+2 < len(raw) && raw[rawIdx+1] == '\r' && raw[rawIdx+2] == '\n' {
			rawIdx += 3
			continue
		}
		break
	}
	return rawIdx
}

func posAtSourceOffset(start Pos, raw []byte, rawOffset int) Pos {
	if !start.IsValid() || rawOffset < 0 || rawOffset > len(raw) {
		return start
	}

	offset := start.Offset() + uint(rawOffset)
	line := start.Line()
	col := start.Col()
	for i := 0; i < rawOffset; i++ {
		if raw[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return NewPos(offset, line, col)
}
