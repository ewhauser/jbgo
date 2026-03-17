package interp

import (
	"strconv"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
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
	if !pos.IsValid() || r.currentExecFile() == "" {
		return 0
	}
	return int(pos.Line())
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

// SetFuncSource overrides the execution source tracked for a declared shell function.
func (r *Runner) SetFuncSource(name, source string) {
	r.setFuncSource(name, source)
}

func (r *Runner) delFunc(name string) {
	if r.Funcs != nil {
		delete(r.Funcs, name)
	}
	if r.funcSources != nil {
		delete(r.funcSources, name)
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
