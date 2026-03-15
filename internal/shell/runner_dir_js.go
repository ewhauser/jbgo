//go:build js

package shell

import "mvdan.cc/sh/v3/interp"

func runnerDirOption(dir string) interp.RunnerOption {
	return func(r *interp.Runner) error {
		r.Dir = dir
		return nil
	}
}
