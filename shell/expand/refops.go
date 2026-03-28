package expand

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ewhauser/gbash/shell/syntax"
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

func emptySubscript(sub *syntax.Subscript) bool {
	if sub == nil || sub.AllElements() {
		return false
	}
	word, ok := sub.Expr.(*syntax.Word)
	if !ok || len(word.Parts) != 1 {
		return false
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	return ok && lit.Value == ""
}

func printNode(node syntax.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, node); err != nil {
		return ""
	}
	return buf.String()
}
