package commands

import (
	"context"
	"io"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// ReaderWithContext arranges for command stdin reads to observe context
// cancellation. When the underlying reader is closable, cancellation also
// closes it so blocked reads can unblock promptly.
func ReaderWithContext(ctx context.Context, reader io.Reader) io.Reader {
	if ctx == nil || reader == nil {
		return reader
	}
	if closer, ok := reader.(io.Closer); ok {
		context.AfterFunc(ctx, func() {
			_ = closer.Close()
		})
	}
	return &contextReader{ctx: ctx, reader: reader}
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if n == 0 && err == nil {
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
	}
	return n, err
}

func (r *contextReader) Close() error {
	closer, ok := r.reader.(io.Closer)
	if !ok {
		return nil
	}
	return closer.Close()
}
