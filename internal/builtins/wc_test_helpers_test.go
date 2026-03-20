package builtins_test

import (
	"fmt"
	goruntime "runtime"
)

func wcExpectedMinimumWidth() int {
	if goruntime.GOOS == "darwin" {
		return 8
	}
	return 1
}

func wcExpectedField(value int) string {
	return fmt.Sprintf("%*d", wcExpectedMinimumWidth(), value)
}
