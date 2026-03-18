package interp

import (
	"strconv"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

const internalBootstrapSource = "<gbash-prelude>"

type frameKind uint8

const (
	frameKindMain frameKind = iota + 1
	frameKindSource
	frameKindFunction
)

type execFrame struct {
	kind       frameKind
	label      string
	execFile   string
	bashSource string
	callLine   int
	internal   bool
}

func (r *Runner) pushFrame(frame execFrame) func() {
	r.frames = append(r.frames, frame)
	return func() {
		r.frames = r.frames[:len(r.frames)-1]
	}
}

func (r *Runner) currentExecFile() string {
	if len(r.frames) == 0 {
		return ""
	}
	return r.frames[len(r.frames)-1].execFile
}

func (r *Runner) currentInternal() bool {
	if len(r.frames) == 0 {
		return r.internalRun
	}
	return r.frames[len(r.frames)-1].internal
}

func (r *Runner) currentCallFile() string {
	if execFile := r.currentExecFile(); execFile != "" {
		return execFile
	}
	return r.filename
}

func (r *Runner) functionCallLine(pos syntax.Pos) int {
	if !pos.IsValid() || r.currentCallFile() == "" {
		return 0
	}
	return int(pos.Line())
}

func (r *Runner) sourceCallLine(pos syntax.Pos) int {
	if !pos.IsValid() || r.currentCallFile() == "" {
		return 0
	}
	return int(pos.Line())
}

func (r *Runner) currentDefinitionSource() string {
	if execFile := r.currentExecFile(); execFile != "" {
		return execFile
	}
	if r.currentInternal() {
		return internalBootstrapSource
	}
	return ""
}

func (r *Runner) funcSource(name string) string {
	if r == nil || r.funcSources == nil {
		return ""
	}
	return r.funcSources[name]
}

func (r *Runner) setFuncSource(name, source string) {
	if r.funcSources == nil {
		r.funcSources = make(map[string]string, 4)
	}
	r.funcSources[name] = source
}

func (r *Runner) funcInternal(name string) bool {
	if r == nil || r.funcInternals == nil {
		return false
	}
	return r.funcInternals[name]
}

func (r *Runner) setFuncInternal(name string, internal bool) {
	if !internal {
		if r.funcInternals != nil {
			delete(r.funcInternals, name)
		}
		return
	}
	if r.funcInternals == nil {
		r.funcInternals = make(map[string]bool, 4)
	}
	r.funcInternals[name] = true
}

func (r *Runner) delFunc(name string) {
	if r.funcs != nil {
		delete(r.funcs, name)
	}
	if r.funcSources != nil {
		delete(r.funcSources, name)
	}
	if r.funcInternals != nil {
		delete(r.funcInternals, name)
	}
}

func (r *Runner) hasFunctionFrame() bool {
	for i := len(r.frames) - 1; i >= 0; i-- {
		if r.frames[i].kind == frameKindFunction {
			return true
		}
	}
	return false
}

func (r *Runner) funcNameStack() []string {
	if !r.hasFunctionFrame() {
		return nil
	}
	stack := make([]string, 0, len(r.frames))
	for i := len(r.frames) - 1; i >= 0; i-- {
		if r.frames[i].label == "" {
			continue
		}
		stack = append(stack, r.frames[i].label)
	}
	return stack
}

func (r *Runner) bashSourceStack() []string {
	stack := make([]string, 0, len(r.frames))
	for i := len(r.frames) - 1; i >= 0; i-- {
		if r.frames[i].bashSource == "" {
			continue
		}
		stack = append(stack, r.frames[i].bashSource)
	}
	if len(stack) == 0 {
		return nil
	}
	return stack
}

func (r *Runner) bashLineNoStack() []string {
	stack := make([]string, 0, len(r.frames))
	for i := len(r.frames) - 1; i >= 0; i-- {
		stack = append(stack, strconv.Itoa(r.frames[i].callLine))
	}
	if len(stack) == 0 {
		return nil
	}
	return stack
}

func (r *Runner) callerFrame(depth int) (execFrame, bool) {
	if depth < 0 || len(r.frames) < 2 {
		return execFrame{}, false
	}
	seen := 0
	for i := len(r.frames) - 2; i >= 0; i-- {
		frame := r.frames[i]
		if frame.internal {
			continue
		}
		if seen == depth {
			return frame, true
		}
		seen++
	}
	return execFrame{}, false
}
