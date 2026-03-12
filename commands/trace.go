package commands

import (
	"errors"
	"time"

	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/trace"
)

func recordPolicyDenied(rec trace.Recorder, err error, action policy.FileAction, path, command string, exitCode int) {
	if rec == nil || !policy.IsDenied(err) {
		return
	}

	denied := &policy.DeniedError{}
	if !errors.As(err, &denied) {
		return
	}

	rec.Record(&trace.Event{
		Kind: trace.EventPolicyDenied,
		At:   time.Now().UTC(),
		Policy: &trace.PolicyEvent{
			Subject:  denied.Subject,
			Reason:   denied.Reason,
			Action:   string(action),
			Path:     path,
			Command:  command,
			ExitCode: exitCode,
		},
		Error: err.Error(),
	})
}

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
