package builtins

import (
	"context"

	"github.com/ewhauser/gbash/internal/shellstate"
)

func completionStateFromContext(ctx context.Context) *shellstate.CompletionState {
	if state := shellstate.CompletionStateFromContext(ctx); state != nil {
		return state
	}
	return shellstate.NewCompletionState()
}
