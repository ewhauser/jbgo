//go:build !js

package shell

import "github.com/ewhauser/gbash/third_party/mvdan-sh/interp"

func runnerDirOption(dir string) interp.RunnerOption {
	return interp.Dir(dir)
}
