//go:build !unix

package gbash

import "context"

func withHostProcessGroup(ctx context.Context) context.Context {
	return ctx
}
