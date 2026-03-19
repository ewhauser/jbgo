package expand

import (
	"errors"
	"fmt"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// VarRefWriter is an optional environment extension that can write directly
// through parsed variable references such as a[1] or assoc[key].
type VarRefWriter interface {
	Environ
	SetVarRef(ref *syntax.VarRef, vr Variable, appendValue bool) error
}

// BadArraySubscriptError reports a bash-style invalid array subscript.
type BadArraySubscriptError struct {
	Name string
}

func (e BadArraySubscriptError) Error() string {
	return fmt.Sprintf("%s: bad array subscript", e.Name)
}

func isBadArraySubscript(err error) bool {
	var target BadArraySubscriptError
	return errors.As(err, &target)
}
