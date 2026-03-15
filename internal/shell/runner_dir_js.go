//go:build js

package shell

import "github.com/ewhauser/gbash/third_party/mvdan-sh/interp"

func runnerDirOption(dir string) interp.RunnerOption {
	return func(r *interp.Runner) error {
		r.Dir = dir
		return nil
	}
}
