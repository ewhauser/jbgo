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
	kind        frameKind
	label       string
	execFile    string
	bashSource  string
	callLine    int
	internal    bool
	allowErr    bool
	allowDebug  bool
	allowReturn bool
}

func (r *Runner) pushFrame(frame execFrame) func() {
	r.ensureOwnFrames()
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
	if r.filename != "" && r.filename != "stdin" {
		return r.filename
	}
	return ""
}

func (r *Runner) funcInfo(name string) (funcInfo, bool) {
	if r == nil || r.funcs == nil {
		return funcInfo{}, false
	}
	info, ok := r.funcs[name]
	return info, ok
}

func (r *Runner) funcBody(name string) *syntax.Stmt {
	info, ok := r.funcInfo(name)
	if !ok {
		return nil
	}
	return info.body
}

func (r *Runner) funcSource(name string) string {
	info, ok := r.funcInfo(name)
	if !ok {
		return ""
	}
	return info.definitionSource
}

func (r *Runner) funcBodySource(name string) (funcSourceSpan, bool) {
	info, ok := r.funcInfo(name)
	if !ok || !info.hasBodySource {
		return funcSourceSpan{}, false
	}
	return info.bodySource, true
}

func (r *Runner) funcInternal(name string) bool {
	info, ok := r.funcInfo(name)
	if !ok {
		return false
	}
	return info.internal
}

func (r *Runner) funcTrace(name string) bool {
	info, ok := r.funcInfo(name)
	if !ok {
		return false
	}
	return info.trace
}

func (r *Runner) setFuncInfo(name string, info funcInfo) {
	r.ensureOwnFuncs()
	r.funcs[name] = info
}

func (r *Runner) delFunc(name string) {
	if r.funcs != nil {
		r.funcs = cloneMapOnWrite(r.funcs, &r.funcsShared)
		delete(r.funcs, name)
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

func (r *Runner) callerFrame(depth int) (int, execFrame, bool) {
	if depth < 0 {
		return 0, execFrame{}, false
	}
	// Walk the frame stack once, counting only non-internal frames.
	// We need frames at positions depth and depth+1.
	var atDepth, atDepthPlus1 execFrame
	seen := 0
	for i := len(r.frames) - 1; i >= 0; i-- {
		frame := r.frames[i]
		if frame.internal {
			continue
		}
		if seen == depth {
			atDepth = frame
		}
		if seen == depth+1 {
			atDepthPlus1 = frame
			return atDepth.callLine, atDepthPlus1, true
		}
		seen++
	}
	return 0, execFrame{}, false
}

func (r *Runner) currentTrapFrame() (execFrame, bool) {
	for i := len(r.frames) - 1; i >= 0; i-- {
		frame := r.frames[i]
		if frame.kind != frameKindFunction && frame.kind != frameKindSource {
			continue
		}
		return frame, true
	}
	return execFrame{allowErr: true, allowDebug: true, allowReturn: true}, false
}

func (r *Runner) errTrapAllowed() bool {
	frame, ok := r.currentTrapFrame()
	if !ok {
		return true
	}
	return frame.allowErr
}

func (r *Runner) debugTrapAllowed() bool {
	frame, ok := r.currentTrapFrame()
	if !ok {
		return true
	}
	return frame.allowDebug
}

func (r *Runner) returnTrapAllowed() bool {
	frame, ok := r.currentTrapFrame()
	if !ok {
		return true
	}
	return frame.allowReturn
}
