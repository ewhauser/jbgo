//go:build !js

package shell

import "mvdan.cc/sh/v3/interp"

func runnerDirOption(dir string) interp.RunnerOption {
	return interp.Dir(dir)
}
