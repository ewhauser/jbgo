package syntax

import (
	"runtime"
	"slices"
	"sync"
	"weak"
)

// CallExprSeparator describes the parsed separator trivia between two adjacent
// simple-command operands.
//
// A separator is only preserved for trees built directly by [Parser.Parse].
// Synthetic or typedjson-decoded trees return the zero value, which reports
// [CallExprSeparator.IsValid] as false.
type CallExprSeparator struct {
	valid   bool
	spaces  uint32
	tabs    uint32
	newline bool
}

// IsValid reports whether parse-time separator metadata is available.
func (s CallExprSeparator) IsValid() bool { return s.valid }

// SpaceCount reports the number of ASCII space bytes in the separator.
func (s CallExprSeparator) SpaceCount() int { return int(s.spaces) }

// TabCount reports the number of tab bytes in the separator.
func (s CallExprSeparator) TabCount() int { return int(s.tabs) }

// HasNewline reports whether the separator crossed a newline.
//
// Escaped line continuations such as backslash-newline are reported as
// newline-bearing separators.
func (s CallExprSeparator) HasNewline() bool { return s.newline }

// HasMultipleSpacesOnSameLine reports whether the separator is made of at
// least two ASCII spaces on the same line, with no tabs or newlines.
func (s CallExprSeparator) HasMultipleSpacesOnSameLine() bool {
	return s.valid && !s.newline && s.tabs == 0 && s.spaces >= 2
}

// ArgSeparator reports the separator between Args[i] and Args[i+1].
func (c *CallExpr) ArgSeparator(i int) CallExprSeparator {
	if c == nil || i < 0 || i+1 >= len(c.Args) {
		return CallExprSeparator{}
	}
	return c.OperandSeparator(len(c.Assigns) + i)
}

// OperandSeparator reports the separator between operand i and operand i+1 in
// lexical simple-command order: all prefix assignments, then argv words.
func (c *CallExpr) OperandSeparator(i int) CallExprSeparator {
	if c == nil || i < 0 {
		return CallExprSeparator{}
	}
	callExprSeparatorStore.RLock()
	seps := callExprSeparatorStore.byKey[weak.Make(c)]
	callExprSeparatorStore.RUnlock()
	if i >= len(seps) {
		return CallExprSeparator{}
	}
	return seps[i]
}

var callExprSeparatorStore struct {
	sync.RWMutex
	byKey map[weak.Pointer[CallExpr]][]CallExprSeparator
}

func setCallExprSeparators(c *CallExpr, seps []CallExprSeparator) {
	if c == nil || len(seps) == 0 {
		return
	}
	key := weak.Make(c)
	callExprSeparatorStore.Lock()
	if callExprSeparatorStore.byKey == nil {
		callExprSeparatorStore.byKey = make(map[weak.Pointer[CallExpr]][]CallExprSeparator)
	}
	callExprSeparatorStore.byKey[key] = slices.Clone(seps)
	callExprSeparatorStore.Unlock()
	runtime.AddCleanup(c, clearCallExprSeparators, key)
}

func clearCallExprSeparators(key weak.Pointer[CallExpr]) {
	callExprSeparatorStore.Lock()
	delete(callExprSeparatorStore.byKey, key)
	callExprSeparatorStore.Unlock()
}

func combineCallExprSeparators(parts ...CallExprSeparator) CallExprSeparator {
	var sep CallExprSeparator
	for _, part := range parts {
		if !part.valid {
			continue
		}
		sep.valid = true
		sep.spaces += part.spaces
		sep.tabs += part.tabs
		sep.newline = sep.newline || part.newline
	}
	return sep
}
