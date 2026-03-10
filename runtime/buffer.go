package runtime

import "bytes"

type captureBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func newCaptureBuffer(limit int64) *captureBuffer {
	return &captureBuffer{limit: limit}
}

func (b *captureBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}

	remaining := int(b.limit) - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}

	if len(p) <= remaining {
		return b.buf.Write(p)
	}

	b.truncated = true
	_, _ = b.buf.Write(p[:remaining])
	return len(p), nil
}

func (b *captureBuffer) String() string {
	return b.buf.String()
}

func (b *captureBuffer) Truncated() bool {
	return b.truncated
}
