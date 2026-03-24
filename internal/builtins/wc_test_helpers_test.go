package builtins_test

import (
	"fmt"
	goruntime "runtime"
	"strings"
)

func wcExpectedMinimumWidth() int {
	if goruntime.GOOS == "darwin" {
		return 8
	}
	return 7
}

func wcExpectedField(value int) string {
	return fmt.Sprintf("%*d", wcExpectedMinimumWidth(), value)
}

func wcExpectedLine(label string, values ...int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, wcExpectedField(value))
	}
	line := strings.Join(parts, " ")
	if label != "" {
		line += " " + label
	}
	return line + "\n"
}
