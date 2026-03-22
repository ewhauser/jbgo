package interp

import (
	"maps"

	"github.com/ewhauser/gbash/internal/shell/expand"
)

func shareMapForSubshell[K comparable, V any](m map[K]V, parentShared *bool) bool {
	if m == nil {
		return false
	}
	*parentShared = true
	return true
}

func cloneMapOnWrite[K comparable, V any](m map[K]V, shared *bool) map[K]V {
	if *shared {
		m = maps.Clone(m)
		*shared = false
	}
	return m
}

func shareSliceForSubshell[T any](s []T, parentShared *bool) bool {
	if cap(s) == 0 {
		return false
	}
	*parentShared = true
	return true
}

func cloneSliceOnWrite[T any](s []T, shared *bool) []T {
	if *shared {
		s = append([]T(nil), s...)
		*shared = false
	}
	return s
}

func (r *Runner) ensureOwnFuncs() {
	r.funcs = cloneMapOnWrite(r.funcs, &r.funcsShared)
	if r.funcs == nil {
		r.funcs = make(map[string]funcInfo, 4)
	}
}

func (r *Runner) ensureOwnAlias() {
	r.alias = cloneMapOnWrite(r.alias, &r.aliasShared)
	if r.alias == nil {
		r.alias = make(map[string]alias)
	}
}

func (r *Runner) clearAlias() {
	if r.alias == nil {
		return
	}
	if r.aliasShared {
		r.alias = make(map[string]alias)
		r.aliasShared = false
		return
	}
	clear(r.alias)
}

func (r *Runner) ensureOwnCommandHash() {
	r.commandHash = cloneMapOnWrite(r.commandHash, &r.commandHashShared)
	if r.commandHash == nil {
		r.commandHash = make(map[string]commandHashEntry)
	}
}

func (r *Runner) clearCommandHash() {
	if r.commandHash == nil {
		return
	}
	if r.commandHashShared {
		r.commandHash = make(map[string]commandHashEntry)
		r.commandHashShared = false
		return
	}
	clear(r.commandHash)
}

func (r *Runner) ensureOwnHiddenReadonlyArrayDecl() {
	r.hiddenReadonlyArrayDecl = cloneMapOnWrite(r.hiddenReadonlyArrayDecl, &r.hiddenReadonlyArrayDeclShared)
	if r.hiddenReadonlyArrayDecl == nil {
		r.hiddenReadonlyArrayDecl = make(map[string]expand.ValueKind)
	}
}

func (r *Runner) ensureOwnNamedFDReleased() {
	r.namedFDReleased = cloneMapOnWrite(r.namedFDReleased, &r.namedFDReleasedShared)
	if r.namedFDReleased == nil {
		r.namedFDReleased = make(map[string]bool)
	}
}

func (r *Runner) ensureOwnFrames() {
	r.frames = cloneSliceOnWrite(r.frames, &r.framesShared)
}

func (r *Runner) ensureOwnDirStack() {
	r.dirStack = cloneSliceOnWrite(r.dirStack, &r.dirStackShared)
}
