package interp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/syntax"
)

// ChunkTransformer can mutate a parsed chunk before execution and optionally
// return any synthetic pipeline metadata for that chunk.
type ChunkTransformer func(file *syntax.File) (map[*syntax.Stmt]*syntax.Stmt, error)

// RunReaderWithMetadata parses and executes shell input incrementally so alias
// and shopt state can affect later complete commands in the same script.
func (r *Runner) RunReaderWithMetadata(ctx context.Context, reader io.Reader, name, topLevelScriptPath string, transform ChunkTransformer) error {
	return r.runChunked(ctx, reader, name, topLevelScriptPath, transform, true, nil)
}

func (r *Runner) runShellReader(ctx context.Context, reader io.Reader, name string, frame *execFrame) error {
	return r.runChunked(ctx, reader, name, "", nil, false, frame)
}

func (r *Runner) runChunked(ctx context.Context, reader io.Reader, name, topLevelScriptPath string, transform ChunkTransformer, runExitTrap bool, frame *execFrame) error {
	if reader == nil {
		reader = strings.NewReader("")
	}
	if !r.didReset {
		r.Reset()
	}

	input := bufio.NewReader(reader)
	var pending strings.Builder
	var chunkStartOffset uint
	chunkStartLine := uint(1)
	totalOffset := uint(0)
	totalLine := uint(1)
	chunkIndex := 0

	var restoreFrame func()
	framePushed := false
	pushFrame := func() {
		if framePushed {
			return
		}
		switch {
		case frame != nil:
			restoreFrame = r.pushFrame(*frame)
			framePushed = true
		case !r.internalRun && topLevelScriptPath != "" && name == topLevelScriptPath:
			restoreFrame = r.pushFrame(execFrame{
				kind:       frameKindMain,
				label:      "main",
				execFile:   name,
				bashSource: name,
				callLine:   0,
				internal:   false,
			})
			framePushed = true
		}
	}
	popFrame := func() {
		if !framePushed {
			return
		}
		restoreFrame()
		restoreFrame = nil
		framePushed = false
	}
	finish := func(err error) error {
		popFrame()
		var exitResult trapRunResult
		if runExitTrap {
			exitResult = r.runTrap(ctx, trapIDExit, r.currentStmtLine, r.exit.code)
		}
		if err != nil {
			if exitResult.handler.exiting || exitResult.handler.fatalExit {
				var parseErr syntax.ParseError
				if errors.As(err, &parseErr) {
					io.WriteString(r.stderr, parseErr.BashError())
					io.WriteString(r.stderr, "\n")
					return r.currentRunError()
				}
			}
			return err
		}
		return r.currentRunError()
	}

	for {
		line, readErr := input.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return finish(readErr)
		}
		if line == "" && readErr == io.EOF && pending.Len() == 0 {
			break
		}
		if r.opts[optVerbose] && line != "" {
			io.WriteString(r.stderr, line)
		}

		if pending.Len() == 0 {
			chunkStartOffset = totalOffset
			chunkStartLine = totalLine
		}
		pending.WriteString(line)
		totalOffset += uint(len(line))
		totalLine += uint(strings.Count(line, "\n"))
		if lineContinues(line) {
			continue
		}
		parser := r.newParser()
		file, err := parser.Parse(strings.NewReader(pending.String()), name)
		if err != nil {
			if chunkParseIncomplete(err, parser) && readErr != io.EOF {
				continue
			}
			err = attachChunkParseErrorSourceLine(err, pending.String())
			err = decorateCommandStringParseError(err, r, name)
			err = shiftChunkError(err, chunkStartOffset, chunkStartLine)
			if recoverable, ok := recoverableParseError(err); ok {
				_, _ = io.WriteString(r.stderr, recoverable.BashError())
				_, _ = io.WriteString(r.stderr, "\n")
				r.exit.code = 1
				pending.Reset()
				if readErr == io.EOF {
					break
				}
				continue
			}
			return finish(err)
		}
		shiftChunkPositions(file, chunkStartOffset, chunkStartLine)
		if len(file.Stmts) == 0 {
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			continue
		}
		prevFile := r.analysisFileStart(analysis.FileMetadata{
			Name:         name,
			TopLevelPath: topLevelScriptPath,
			ChunkIndex:   chunkIndex,
			ChunkLine:    chunkStartLine,
			ChunkOffset:  chunkStartOffset,
		})
		chunkIndex++

		synthetic := map[*syntax.Stmt]*syntax.Stmt(nil)
		if transform != nil {
			var transformErr error
			synthetic, transformErr = transform(file)
			if transformErr != nil {
				r.analysisFileFinish(r.analysisStatusForError(transformErr))
				r.analysisRestoreFile(prevFile)
				return finish(transformErr)
			}
		}

		pushFrame()
		prevTopLevel := r.topLevelScriptPath
		prevSynthetic := r.syntheticPipelineStmts
		prevChunkSource := r.currentChunkSource
		prevChunkSourceBase := r.currentChunkSourceBase
		r.topLevelScriptPath = topLevelScriptPath
		r.syntheticPipelineStmts = synthetic
		r.currentChunkSource = pending.String()
		r.currentChunkSourceBase = chunkStartOffset
		err = r.run(ctx, file, false, false)
		r.analysisFileFinish(r.AnalysisStatus())
		r.analysisRestoreFile(prevFile)
		r.topLevelScriptPath = prevTopLevel
		r.syntheticPipelineStmts = prevSynthetic
		r.currentChunkSource = prevChunkSource
		r.currentChunkSourceBase = prevChunkSourceBase
		pending.Reset()

		if err != nil {
			var status ExitStatus
			if !errors.As(err, &status) {
				return finish(err)
			}
		}
		if r.Exited() || readErr == io.EOF {
			break
		}
	}

	return finish(nil)
}

func decorateCommandStringParseError(err error, r *Runner, name string) error {
	if err == nil || r == nil || !r.interactive || !r.commandString {
		return err
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		return err
	}
	return parseErr.WithInteractiveCommandStringPrefix(name)
}

func recoverableParseError(err error) (syntax.ParseError, bool) {
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) || !parseErr.Recoverable() {
		return syntax.ParseError{}, false
	}
	return parseErr, true
}

func chunkParseIncomplete(err error, parser *syntax.Parser) bool {
	if err == nil {
		return false
	}
	if syntax.IsIncomplete(err) || (parser != nil && parser.Incomplete()) {
		return true
	}
	var parseErr syntax.ParseError
	return errors.As(err, &parseErr) && strings.HasPrefix(parseErr.Text, "unclosed here-document")
}

func attachChunkParseErrorSourceLine(err error, script string) error {
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		return err
	}
	if parseErr.SourceLine != "" || !parseErr.WantsSourceLine() {
		return err
	}
	sourceLine := chunkSourceLineAt(script, parseErr.Pos.Line())
	if sourceLine == "" {
		return err
	}
	parseErr.SourceLine = sourceLine
	return parseErr
}

var posType = reflect.TypeFor[syntax.Pos]()

func shiftChunkPositions(node syntax.Node, offsetBase, lineBase uint) {
	if node == nil || (offsetBase == 0 && lineBase <= 1) {
		return
	}
	shiftValuePositions(reflect.ValueOf(node), offsetBase, lineBase-1, make(map[uintptr]struct{}))
}

func shiftChunkError(err error, offsetBase, lineBase uint) error {
	if err == nil || (offsetBase == 0 && lineBase <= 1) {
		return err
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) || !parseErr.Pos.IsValid() {
		return err
	}
	shiftPos := func(pos syntax.Pos) syntax.Pos {
		if !pos.IsValid() {
			return pos
		}
		return syntax.NewPos(pos.Offset()+offsetBase, pos.Line()+lineBase-1, pos.Col())
	}
	parseErr.Pos = shiftPos(parseErr.Pos)
	parseErr.SecondaryPos = shiftPos(parseErr.SecondaryPos)
	parseErr.SourceLinePos = shiftPos(parseErr.SourceLinePos)
	return parseErr
}

func lineContinues(line string) bool {
	if !strings.HasSuffix(line, "\n") || len(line) < 2 || line[len(line)-2] != '\\' {
		return false
	}

	const (
		lineStateTop = iota
		lineStateSingle
		lineStateDouble
		lineStateBacktick
		lineStateComment
	)

	state := lineStateTop
	escaped := false
	commentAllowed := true
	paramDepth := 0
	for i := 0; i < len(line)-1; i++ {
		ch := line[i]
		switch state {
		case lineStateComment:
			continue
		case lineStateSingle:
			if ch == '\'' {
				state = lineStateTop
			}
			commentAllowed = false
			continue
		case lineStateDouble:
			if escaped {
				escaped = false
				commentAllowed = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				state = lineStateTop
			}
			commentAllowed = false
			continue
		case lineStateBacktick:
			if escaped {
				escaped = false
				commentAllowed = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '`':
				state = lineStateTop
			}
			commentAllowed = false
			continue
		}

		if escaped {
			escaped = false
			commentAllowed = false
			continue
		}

		switch ch {
		case '\\':
			escaped = true
			commentAllowed = false
		case '$':
			if i+1 < len(line)-1 && line[i+1] == '{' {
				paramDepth++
				i++
			}
			commentAllowed = false
		case '\'':
			state = lineStateSingle
			commentAllowed = false
		case '"':
			state = lineStateDouble
			commentAllowed = false
		case '`':
			state = lineStateBacktick
			commentAllowed = false
		case '#':
			if commentAllowed && paramDepth == 0 {
				state = lineStateComment
				continue
			}
			commentAllowed = false
		case '}':
			if paramDepth > 0 {
				paramDepth--
			}
			commentAllowed = false
		case ' ', '\t', '\r':
			commentAllowed = true
		case '!', '&', '(', ')', ';', '<', '>', '|':
			commentAllowed = true
		default:
			commentAllowed = false
		}
	}

	return state != lineStateComment && escaped
}

func chunkSourceLineAt(script string, lineNum uint) string {
	if lineNum == 0 {
		return ""
	}
	lines := strings.Split(script, "\n")
	idx := int(lineNum) - 1
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}

func shiftValuePositions(val reflect.Value, offsetBase, lineDelta uint, seen map[uintptr]struct{}) {
	if !val.IsValid() {
		return
	}
	switch val.Kind() {
	case reflect.Pointer:
		if val.IsNil() {
			return
		}
		ptr := val.Pointer()
		if _, ok := seen[ptr]; ok {
			return
		}
		seen[ptr] = struct{}{}
		shiftValuePositions(val.Elem(), offsetBase, lineDelta, seen)
	case reflect.Interface:
		if val.IsNil() {
			return
		}
		shiftValuePositions(val.Elem(), offsetBase, lineDelta, seen)
	case reflect.Struct:
		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			if field.Type() == posType {
				pos := field.Interface().(syntax.Pos)
				if !pos.IsValid() {
					continue
				}
				field.Set(reflect.ValueOf(syntax.NewPos(pos.Offset()+offsetBase, pos.Line()+lineDelta, pos.Col())))
				continue
			}
			shiftValuePositions(field, offsetBase, lineDelta, seen)
		}
	case reflect.Slice:
		for i := 0; i < val.Len(); i++ {
			shiftValuePositions(val.Index(i), offsetBase, lineDelta, seen)
		}
	}
}
