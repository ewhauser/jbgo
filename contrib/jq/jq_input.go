package jq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ewhauser/gbash/commands"
)

type jqInputValue struct {
	name  string
	value any
}

type jqIter interface {
	Next() (any, bool)
	Close() error
	Name() string
}

type jqSliceIter struct {
	items []jqInputValue
	index int
	name  string
}

func (i *jqSliceIter) Next() (any, bool) {
	if i == nil || i.index >= len(i.items) {
		return nil, false
	}
	item := i.items[i.index]
	i.index++
	i.name = item.name
	return item.value, true
}

func (i *jqSliceIter) Close() error {
	if i == nil {
		return nil
	}
	i.index = len(i.items)
	i.name = ""
	return nil
}

func (i *jqSliceIter) Name() string {
	if i == nil {
		return ""
	}
	return i.name
}

type jqLazyIter struct {
	ctx      context.Context
	inv      *commands.Invocation
	opts     jqOptions
	inputs   []string
	loaded   *jqSliceIter
	loadErr  error
	errSent  bool
	isClosed bool
}

func (i *jqLazyIter) Load() error {
	if i == nil || i.isClosed {
		return nil
	}
	if i.loaded != nil || i.loadErr != nil {
		return i.loadErr
	}

	sources, err := readJQInputSources(i.ctx, i.inv, i.inputs)
	if err != nil {
		i.loadErr = err
		return err
	}

	values, err := buildJQInputValues(i.inv, &i.opts, sources)
	if err != nil {
		i.loadErr = err
		return err
	}

	i.loaded = &jqSliceIter{items: values}
	return nil
}

func (i *jqLazyIter) Next() (any, bool) {
	if i == nil || i.isClosed {
		return nil, false
	}
	if err := i.Load(); err != nil {
		if i.errSent {
			return nil, false
		}
		i.errSent = true
		return err, true
	}
	return i.loaded.Next()
}

func (i *jqLazyIter) Close() error {
	if i == nil {
		return nil
	}
	i.isClosed = true
	i.errSent = true
	if i.loaded != nil {
		return i.loaded.Close()
	}
	return nil
}

func (i *jqLazyIter) Name() string {
	if i == nil || i.loaded == nil {
		return ""
	}
	return i.loaded.Name()
}

type jqNullIter struct {
	done bool
}

func newJQNullInputIter() *jqNullIter {
	return &jqNullIter{}
}

func (i *jqNullIter) Next() (any, bool) {
	if i == nil || i.done {
		return nil, false
	}
	i.done = true
	return nil, true
}

func (i *jqNullIter) Close() error {
	if i != nil {
		i.done = true
	}
	return nil
}

func (*jqNullIter) Name() string {
	return ""
}

func newJQInputIter(ctx context.Context, inv *commands.Invocation, opts *jqOptions, inputs []string) *jqLazyIter {
	clonedInputs := make([]string, len(inputs))
	copy(clonedInputs, inputs)

	var clonedOpts jqOptions
	if opts != nil {
		clonedOpts = *opts
	}

	return &jqLazyIter{
		ctx:    ctx,
		inv:    inv,
		opts:   clonedOpts,
		inputs: clonedInputs,
	}
}

func buildJQInputValues(inv *commands.Invocation, opts *jqOptions, sources *jqSources) ([]jqInputValue, error) {
	var (
		values []jqInputValue
		err    error
	)

	switch {
	case opts.rawInput:
		values = collectRawJQInputValues(sources)
	case opts.seq:
		values = collectSeqJQInputValues(inv, opts, sources)
	case opts.stream:
		values, err = collectStreamJQInputValues(inv, opts, sources)
	default:
		values, err = collectJSONJQInputValues(inv, sources)
	}
	if err != nil {
		return nil, err
	}

	if !opts.slurp {
		return values, nil
	}
	if opts.rawInput {
		var builder bytes.Buffer
		for _, source := range sources.data {
			builder.Write(source)
		}
		return []jqInputValue{{name: lastJQSourceName(sources), value: builder.String()}}, nil
	}

	slurped := make([]any, 0, len(values))
	for _, value := range values {
		slurped = append(slurped, value.value)
	}
	return []jqInputValue{{name: lastJQSourceName(sources), value: slurped}}, nil
}

func collectRawJQInputValues(sources *jqSources) []jqInputValue {
	if sources == nil {
		return nil
	}
	values := make([]jqInputValue, 0)
	for i, data := range sources.data {
		for _, value := range rawLines(data) {
			values = append(values, jqInputValue{name: sources.names[i], value: value})
		}
	}
	return values
}

func collectJSONJQInputValues(inv *commands.Invocation, sources *jqSources) ([]jqInputValue, error) {
	if sources == nil {
		return nil, nil
	}
	values := make([]jqInputValue, 0)
	for i, data := range sources.data {
		decoded, err := decodeJQJSON(data)
		if err != nil {
			return nil, exitf(inv, 5, "jq: parse error in %s: %v", sources.names[i], err)
		}
		for _, value := range decoded {
			values = append(values, jqInputValue{name: sources.names[i], value: value})
		}
	}
	return values, nil
}

func collectStreamJQInputValues(inv *commands.Invocation, opts *jqOptions, sources *jqSources) ([]jqInputValue, error) {
	if sources == nil {
		return nil, nil
	}
	values := make([]jqInputValue, 0)
	for i, data := range sources.data {
		decoded, err := parseJQStreamSource(inv, sources.names[i], data, opts.streamErrors)
		if err != nil {
			return nil, err
		}
		for _, value := range decoded {
			values = append(values, jqInputValue{name: sources.names[i], value: value})
		}
	}
	return values, nil
}

func collectSeqJQInputValues(inv *commands.Invocation, opts *jqOptions, sources *jqSources) []jqInputValue {
	if sources == nil {
		return nil
	}
	values := make([]jqInputValue, 0)
	for i, data := range sources.data {
		decoded := parseJQSeqSource(inv, sources.names[i], data, opts)
		values = append(values, decoded...)
	}
	return values
}

func lastJQSourceName(sources *jqSources) string {
	if sources == nil || len(sources.names) == 0 {
		return ""
	}
	return sources.names[len(sources.names)-1]
}

func parseJQStreamSource(inv *commands.Invocation, name string, data []byte, emitErrors bool) ([]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	stream := newJQJSONStream(decoder)

	values := make([]any, 0)
	for {
		value, err := stream.next()
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			if emitErrors {
				values = append(values, []any{formatJQStreamError(data, err), stream.errorPath()})
				return values, nil
			}
			return nil, exitf(inv, 5, "jq: parse error in %s: %v", name, err)
		}
		values = append(values, value)
	}
}

const jqSeqRecordSeparator = byte(0x1e)

func parseJQSeqSource(inv *commands.Invocation, name string, data []byte, opts *jqOptions) []jqInputValue {
	values := make([]jqInputValue, 0)
	for offset := 0; offset < len(data); {
		if data[offset] != jqSeqRecordSeparator {
			next := bytes.IndexByte(data[offset:], jqSeqRecordSeparator)
			limit := len(data)
			if next >= 0 {
				limit = offset + next
			}
			if len(bytes.TrimSpace(data[offset:limit])) > 0 {
				writeJQSeqWarning(inv, data[:limit])
			}
			offset = limit
			continue
		}

		offset++
		next := bytes.IndexByte(data[offset:], jqSeqRecordSeparator)
		limit := len(data)
		if next >= 0 {
			limit = offset + next
		}
		record := bytes.TrimSpace(data[offset:limit])
		offset = limit
		if len(record) == 0 {
			continue
		}

		if opts.stream {
			streamValues, err := parseJQStreamSource(inv, name, record, opts.streamErrors)
			if err != nil {
				writeJQSeqWarning(inv, data[:limit])
				continue
			}
			for _, value := range streamValues {
				values = append(values, jqInputValue{name: name, value: value})
			}
			continue
		}

		value, err := decodeSingleJQJSON(record)
		if err != nil {
			writeJQSeqWarning(inv, data[:limit])
			continue
		}
		values = append(values, jqInputValue{name: name, value: value})
	}
	return values
}

func writeJQSeqWarning(inv *commands.Invocation, data []byte) {
	if inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "jq: ignoring parse error: %s\n", formatJQSeqAbandonedTextError(data))
}

func formatJQStreamError(data []byte, err error) string {
	line, column := jqLineColumnAtOffset(data, jqJSONErrorOffset(data, err))
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Sprintf("Unfinished JSON term at EOF at line %d, column %d", line, column)
	}
	return fmt.Sprintf("%v at line %d, column %d", err, line, column)
}

func formatJQSeqAbandonedTextError(data []byte) string {
	line, column := jqLineColumnAtOffset(data, len(data))
	return fmt.Sprintf("Unfinished abandoned text at EOF at line %d, column %d", line, column)
}

func jqJSONErrorOffset(data []byte, err error) int {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		offset := int(syntaxErr.Offset)
		if offset > 0 {
			return offset - 1
		}
		return 0
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return len(data)
	}
	return len(data)
}

func jqLineColumnAtOffset(data []byte, offset int) (line, column int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(data) {
		offset = len(data)
	}
	line = 1
	for i := 0; i < offset; i++ {
		if data[i] == '\n' {
			line++
			column = 0
			continue
		}
		column++
	}
	return line, column
}

type jqJSONStream struct {
	decoder *json.Decoder
	path    []any
	states  []int
}

func newJQJSONStream(decoder *json.Decoder) *jqJSONStream {
	return &jqJSONStream{
		decoder: decoder,
		path:    []any{},
		states:  []int{jqJSONStateTopValue},
	}
}

const (
	jqJSONStateTopValue = iota
	jqJSONStateArrayStart
	jqJSONStateArrayValue
	jqJSONStateArrayEnd
	jqJSONStateArrayEmptyEnd
	jqJSONStateObjectStart
	jqJSONStateObjectKey
	jqJSONStateObjectValue
	jqJSONStateObjectEnd
	jqJSONStateObjectEmptyEnd
)

func (s *jqJSONStream) next() (any, error) {
	switch s.states[len(s.states)-1] {
	case jqJSONStateArrayEnd, jqJSONStateObjectEnd:
		s.path = s.path[:len(s.path)-1]
		fallthrough
	case jqJSONStateArrayEmptyEnd, jqJSONStateObjectEmptyEnd:
		s.states = s.states[:len(s.states)-1]
	}
	if s.decoder.More() {
		switch s.states[len(s.states)-1] {
		case jqJSONStateArrayValue:
			s.path[len(s.path)-1] = s.path[len(s.path)-1].(int) + 1
		case jqJSONStateObjectValue:
			s.path = s.path[:len(s.path)-1]
		}
	}
	for {
		token, err := s.decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) && s.states[len(s.states)-1] != jqJSONStateTopValue {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '[', '{':
				switch s.states[len(s.states)-1] {
				case jqJSONStateArrayStart:
					s.states[len(s.states)-1] = jqJSONStateArrayValue
				case jqJSONStateObjectKey:
					s.states[len(s.states)-1] = jqJSONStateObjectValue
				}
				if delim == '[' {
					s.states = append(s.states, jqJSONStateArrayStart)
					s.path = append(s.path, 0)
				} else {
					s.states = append(s.states, jqJSONStateObjectStart)
				}
			case ']':
				if s.states[len(s.states)-1] == jqJSONStateArrayStart {
					s.states[len(s.states)-1] = jqJSONStateArrayEmptyEnd
					s.path = s.path[:len(s.path)-1]
					return []any{s.copyPath(), []any{}}, nil
				}
				s.states[len(s.states)-1] = jqJSONStateArrayEnd
				return []any{s.copyPath()}, nil
			case '}':
				if s.states[len(s.states)-1] == jqJSONStateObjectStart {
					s.states[len(s.states)-1] = jqJSONStateObjectEmptyEnd
					return []any{s.copyPath(), map[string]any{}}, nil
				}
				s.states[len(s.states)-1] = jqJSONStateObjectEnd
				return []any{s.copyPath()}, nil
			}
		} else {
			switch s.states[len(s.states)-1] {
			case jqJSONStateArrayStart:
				s.states[len(s.states)-1] = jqJSONStateArrayValue
				fallthrough
			case jqJSONStateArrayValue:
				return []any{s.copyPath(), token}, nil
			case jqJSONStateObjectStart, jqJSONStateObjectValue:
				s.states[len(s.states)-1] = jqJSONStateObjectKey
				s.path = append(s.path, token)
			case jqJSONStateObjectKey:
				s.states[len(s.states)-1] = jqJSONStateObjectValue
				return []any{s.copyPath(), token}, nil
			default:
				s.states[len(s.states)-1] = jqJSONStateTopValue
				return []any{s.copyPath(), token}, nil
			}
		}
	}
}

func (s *jqJSONStream) errorPath() []any {
	if len(s.path) == 0 {
		return []any{nil}
	}
	return s.copyPath()
}

func (s *jqJSONStream) copyPath() []any {
	path := make([]any, len(s.path))
	copy(path, s.path)
	return path
}
