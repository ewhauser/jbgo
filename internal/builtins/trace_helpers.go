package builtins

import (
	"time"

	"github.com/ewhauser/gbash/trace"
)

func recordFileMutation(rec trace.Recorder, action, path, fromPath, toPath string) {
	if rec == nil {
		return
	}

	rec.Record(&trace.Event{
		Kind: trace.EventFileMutation,
		At:   time.Now().UTC(),
		File: &trace.FileEvent{
			Action:   action,
			Path:     path,
			FromPath: fromPath,
			ToPath:   toPath,
		},
	})
}
