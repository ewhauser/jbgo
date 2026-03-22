package interp

import (
	"bytes"
	"io"
	"testing"
)

func BenchmarkVirtualPipeRoundTrip(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 48*1024)
	readBuf := make([]byte, 8*1024)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		pr, pw := NewVirtualPipe()
		if n, err := pw.Write(payload); err != nil {
			b.Fatalf("Write() error = %v", err)
		} else if n != len(payload) {
			b.Fatalf("Write() n = %d, want %d", n, len(payload))
		}
		if err := pw.Close(); err != nil {
			b.Fatalf("Close() error = %v", err)
		}

		read := 0
		for {
			n, err := pr.Read(readBuf)
			read += n
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("Read() error = %v", err)
			}
		}
		if read != len(payload) {
			b.Fatalf("read = %d, want %d", read, len(payload))
		}
	}
}
