package runtime

import (
	"fmt"
	"sync/atomic"
)

var traceIDCounter atomic.Uint64

func nextTraceID(prefix string) string {
	return fmt.Sprintf("%s-%06d", prefix, traceIDCounter.Add(1))
}
