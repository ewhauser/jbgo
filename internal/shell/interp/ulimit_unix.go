//go:build unix && !js && !wasip1

package interp

import (
	"fmt"
	"strconv"
	"syscall"
)

func ulimitBuiltinLines(mode ulimitBuiltinMode) []string {
	lines := make([]string, 0, len(ulimitBuiltinSpecs()))
	for _, spec := range ulimitBuiltinSpecs() {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(spec.resource, &limit); err != nil {
			continue
		}
		value := limit.Cur
		if mode == ulimitBuiltinHard {
			value = limit.Max
		}
		lines = append(lines, fmt.Sprintf("%-25s (%s, -%c) %s", spec.label, spec.unit, spec.option, formatUlimitValue(value, spec.scale)))
	}
	return lines
}

func ulimitBuiltinSpecs() []ulimitResourceSpec {
	return []ulimitResourceSpec{
		{label: "core file size", option: 'c', unit: "blocks", scale: 512, resource: syscall.RLIMIT_CORE},
		{label: "data seg size", option: 'd', unit: "kbytes", scale: 1024, resource: syscall.RLIMIT_DATA},
		{label: "file size", option: 'f', unit: "blocks", scale: 512, resource: syscall.RLIMIT_FSIZE},
		{label: "open files", option: 'n', unit: "", scale: 1, resource: syscall.RLIMIT_NOFILE},
		{label: "stack size", option: 's', unit: "kbytes", scale: 1024, resource: syscall.RLIMIT_STACK},
		{label: "cpu time", option: 't', unit: "seconds", scale: 1, resource: syscall.RLIMIT_CPU},
	}
}

func formatUlimitValue(value, scale uint64) string {
	if value == ^uint64(0) {
		return "unlimited"
	}
	if scale <= 1 {
		return strconv.FormatUint(value, 10)
	}
	return strconv.FormatUint(value/scale, 10)
}
