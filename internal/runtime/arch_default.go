package runtime

import goruntime "runtime"

func archMachineFromGOARCH() string {
	switch goruntime.GOARCH {
	case "amd64":
		return "x86_64"
	case "386":
		return "i686"
	case "arm64":
		return "aarch64"
	default:
		return goruntime.GOARCH
	}
}
